package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestHTTPRequestsTotal(t *testing.T) {
	// Increment counter
	HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()

	// Verify it was registered and can be collected
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	found := false
	for _, mf := range gathered {
		if mf.GetName() == "synapbus_http_requests_total" {
			found = true
			break
		}
	}
	if !found {
		t.Error("synapbus_http_requests_total metric not found in gathered metrics")
	}
}

func TestHTTPRequestDuration(t *testing.T) {
	HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0.5)

	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	found := false
	for _, mf := range gathered {
		if mf.GetName() == "synapbus_http_request_duration_seconds" {
			found = true
			break
		}
	}
	if !found {
		t.Error("synapbus_http_request_duration_seconds metric not found in gathered metrics")
	}
}

func TestMessagesTotal(t *testing.T) {
	MessagesTotal.Inc()

	var m dto.Metric
	if err := MessagesTotal.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	if m.GetCounter().GetValue() < 1 {
		t.Error("expected messages_total counter >= 1")
	}
}

func TestAgentsActive(t *testing.T) {
	AgentsActive.Set(5)

	var m dto.Metric
	if err := AgentsActive.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	if m.GetGauge().GetValue() != 5 {
		t.Errorf("expected agents_active = 5, got %v", m.GetGauge().GetValue())
	}
}

func TestActiveConnections(t *testing.T) {
	ActiveConnections.Set(10)

	var m dto.Metric
	if err := ActiveConnections.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	if m.GetGauge().GetValue() != 10 {
		t.Errorf("expected active_connections = 10, got %v", m.GetGauge().GetValue())
	}
}

func TestMiddleware(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("POST", "/api/messages", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}

	// Verify the counter was incremented
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	found := false
	for _, mf := range gathered {
		if mf.GetName() == "synapbus_http_requests_total" {
			for _, m := range mf.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "status" && l.GetValue() == "201" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("expected http_requests_total with status=201 after middleware")
	}
}

func TestResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	if rw.Unwrap() != rec {
		t.Error("Unwrap should return the underlying ResponseWriter")
	}
}

func TestResponseWriterDefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	// Write without calling WriteHeader - should default to 200
	rw.Write([]byte("hello"))

	if rw.statusCode != http.StatusOK {
		t.Errorf("expected default status 200, got %d", rw.statusCode)
	}
}
