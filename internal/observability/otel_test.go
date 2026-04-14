package observability_test

import (
	"context"
	"testing"
	"time"

	"github.com/synapbus/synapbus/internal/observability"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestConfigFromEnv_DefaultDisabled(t *testing.T) {
	cfg := observability.ConfigFromEnv(func(string) string { return "" })
	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
	if cfg.ServiceName != "synapbus" {
		t.Errorf("ServiceName default = %q, want synapbus", cfg.ServiceName)
	}
	if !cfg.Insecure {
		t.Error("default Insecure should be true (LAN-friendly)")
	}
}

func TestConfigFromEnv_Overrides(t *testing.T) {
	env := map[string]string{
		"SYNAPBUS_OTEL_ENABLED":      "1",
		"SYNAPBUS_OTEL_ENDPOINT":     "otel.kubic.home.arpa:4318",
		"SYNAPBUS_OTEL_INSECURE":     "false",
		"SYNAPBUS_OTEL_SERVICE_NAME": "synapbus-dev",
	}
	cfg := observability.ConfigFromEnv(func(k string) string { return env[k] })
	if !cfg.Enabled {
		t.Error("Enabled=1 not honoured")
	}
	if cfg.Endpoint != "otel.kubic.home.arpa:4318" {
		t.Errorf("Endpoint = %q", cfg.Endpoint)
	}
	if cfg.Insecure {
		t.Error("Insecure=false not honoured")
	}
	if cfg.ServiceName != "synapbus-dev" {
		t.Errorf("ServiceName = %q", cfg.ServiceName)
	}
}

func TestInit_DisabledIsNoopShutdown(t *testing.T) {
	shutdown, err := observability.Init(context.Background(), observability.Config{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("Init err = %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown err = %v", err)
	}
}

// TestInit_EnabledResourceMerge is a regression test for a schema-URL
// conflict: semconv/v1.21 + resource.Default() (which can be 1.26 in
// newer SDKs) used to fail resource.Merge with "conflicting Schema URL".
// Now we use resource.NewSchemaless so the merge is always accepted.
// Points at a dead endpoint so the OTLP client never sends anything.
func TestInit_EnabledSucceeds(t *testing.T) {
	cfg := observability.Config{
		Enabled:        true,
		Endpoint:       "127.0.0.1:1", // unroutable; Init must still return successfully
		Insecure:       true,
		ServiceName:    "synapbus-test",
		ServiceVersion: "0.0.0-test",
	}
	shutdown, err := observability.Init(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Init err = %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	// Shutdown is best-effort and may legitimately fail to export
	// pending spans against a dead endpoint; surface it as a log not
	// a test failure.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Logf("shutdown non-fatal: %v", err)
	}
}

func TestInit_EnabledWithNoVersion(t *testing.T) {
	// ServiceVersion omitted — the attribute list must not include an
	// empty string for service.version.
	cfg := observability.Config{
		Enabled:     true,
		Endpoint:    "127.0.0.1:1",
		Insecure:    true,
		ServiceName: "synapbus-test",
	}
	shutdown, err := observability.Init(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Init err = %v", err)
	}
	_ = shutdown(context.Background())
}

func TestInjectTraceContext_NoSpan(t *testing.T) {
	dst := map[string]string{}
	observability.InjectTraceContext(context.Background(), dst)
	// No span → no traceparent written (TextMapPropagator sees empty
	// span context). Assert the function didn't panic; dst may be empty
	// or contain TRACEPARENT with all-zero trace id depending on setup.
}

func TestInjectTraceContext_WithSpan(t *testing.T) {
	// Install a TracerProvider with an in-memory exporter so spans
	// have real trace ids.
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	// Install the propagator (normally done by observability.Init)
	_, _ = observability.Init(context.Background(), observability.Config{Enabled: false}, nil)

	ctx, span := tp.Tracer("test").Start(context.Background(), "outer")
	defer span.End()

	dst := map[string]string{}
	observability.InjectTraceContext(ctx, dst)
	if _, ok := dst["TRACEPARENT"]; !ok {
		t.Errorf("TRACEPARENT missing from dst: %v", dst)
	}

	tid := observability.TraceIDFromContext(ctx)
	if tid == "" {
		t.Error("TraceIDFromContext returned empty")
	}
}

func TestTraceIDFromContext_NoSpan(t *testing.T) {
	tid := observability.TraceIDFromContext(context.Background())
	if tid != "" {
		t.Errorf("TraceIDFromContext with no span = %q, want empty", tid)
	}
}
