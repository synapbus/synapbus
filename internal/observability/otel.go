// Package observability initialises OpenTelemetry tracing for SynapBus.
// It is opt-in via SYNAPBUS_OTEL_ENABLED=1 and degrades to no-op
// behaviour when disabled, so every caller can unconditionally use
// the otel.Tracer API.
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the library-level identifier used by all SynapBus
// packages when getting a tracer. Keep it stable — it becomes the
// otel.library.name attribute on every span.
const TracerName = "github.com/synapbus/synapbus"

// Config tunes the tracer provider. Zero-value config has Enabled=false
// and causes Init to return a noop shutdown func.
type Config struct {
	// Enabled turns tracing on. Default: false.
	Enabled bool

	// Endpoint is the OTLP HTTP target (host:port). Empty means
	// "otlptracehttp default" — usually localhost:4318.
	Endpoint string

	// Insecure switches to http:// instead of https://. Default true
	// because most LAN collectors on kubic are TLS-less.
	Insecure bool

	// ServiceName overrides the service.name resource attribute.
	// Defaults to "synapbus".
	ServiceName string

	// ServiceVersion adds a service.version attribute when non-empty.
	ServiceVersion string

	// Sampler chooses the sampling rate. Values in [0,1] are treated
	// as a TraceIDRatio. 0 or negative = AlwaysSample. 1 = AlwaysSample.
	Sampler float64
}

// ConfigFromEnv reads SYNAPBUS_OTEL_* variables from the environment.
func ConfigFromEnv(getenv func(string) string) Config {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := Config{
		Enabled:        parseBoolEnv(getenv, "SYNAPBUS_OTEL_ENABLED", false),
		Endpoint:       getenv("SYNAPBUS_OTEL_ENDPOINT"),
		Insecure:       parseBoolEnv(getenv, "SYNAPBUS_OTEL_INSECURE", true),
		ServiceName:    getenv("SYNAPBUS_OTEL_SERVICE_NAME"),
		ServiceVersion: getenv("SYNAPBUS_OTEL_SERVICE_VERSION"),
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "synapbus"
	}
	return cfg
}

// Init wires a TracerProvider and a W3C text-map propagator. Returns
// a shutdown func the caller must defer. When cfg.Enabled is false
// the returned shutdown func is a no-op.
func Init(ctx context.Context, cfg Config, logger *slog.Logger) (func(context.Context) error, error) {
	if logger == nil {
		logger = slog.Default()
	}
	// Always install the propagator so trace context crosses our
	// HTTP boundaries even when we're not the ones exporting spans.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if !cfg.Enabled {
		logger.Info("otel tracing disabled (set SYNAPBUS_OTEL_ENABLED=1 to enable)")
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracehttp.Option{}
	if cfg.Endpoint != "" {
		// otlptracehttp expects host[:port], not a full URL.
		clean := strings.TrimPrefix(cfg.Endpoint, "http://")
		clean = strings.TrimPrefix(clean, "https://")
		clean = strings.TrimSuffix(clean, "/")
		opts = append(opts, otlptracehttp.WithEndpoint(clean))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("observability: create OTLP HTTP exporter: %w", err)
	}

	// Merge our per-run attributes into the SDK's default resource.
	// NewSchemaless avoids embedding our own schema URL, so the merge
	// never conflicts with whatever version resource.Default() uses
	// (which varies between otel/sdk releases — 1.21, 1.26, …).
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(pickSampler(cfg.Sampler)),
	)
	otel.SetTracerProvider(tp)

	logger.Info("otel tracing enabled",
		"endpoint", cfg.Endpoint,
		"service", cfg.ServiceName,
	)
	return tp.Shutdown, nil
}

// InjectTraceContext writes the W3C traceparent / tracestate headers
// from ctx into dst as env-var style keys (TRACEPARENT, TRACESTATE).
// Harnesses call this to push trace context into child processes and
// outbound HTTP requests in a single uniform shape.
func InjectTraceContext(ctx context.Context, dst map[string]string) {
	if dst == nil {
		return
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for k, v := range carrier {
		dst[strings.ToUpper(k)] = v
	}
}

// TraceIDFromContext extracts the trace id hex from the span currently
// attached to ctx, or returns the empty string if there isn't one.
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

func parseBoolEnv(getenv func(string) string, key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(getenv(key)))
	switch v {
	case "":
		return def
	case "1", "t", "true", "yes", "on":
		return true
	case "0", "f", "false", "no", "off":
		return false
	}
	return def
}

func pickSampler(rate float64) sdktrace.Sampler {
	if rate <= 0 || rate >= 1 {
		return sdktrace.AlwaysSample()
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))
}
