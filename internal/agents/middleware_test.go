package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

func TestAuthMiddleware(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteAgentStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	svc := NewAgentService(store, tracer)

	// Register an agent to get a valid API key
	_, apiKey, err := svc.Register(t.Context(), "mw-bot", "Middleware Bot", "ai", json.RawMessage("{}"), 1)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	middleware := AuthMiddleware(svc)

	// Protected handler
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, ok := AgentFromContext(r.Context())
		if !ok {
			t.Error("expected agent in context")
			http.Error(w, "no agent", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(agent.Name))
	}))

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "valid API key",
			authHeader: "Bearer " + apiKey,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid key",
			authHeader: "Bearer invalid-key",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed header",
			authHeader: "NotBearer " + apiKey,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "bearer only no key",
			authHeader: "Bearer",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}
