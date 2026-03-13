package health

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// Checker provides Kubernetes-style health check endpoints.
type Checker struct {
	db      *sql.DB
	version string
}

// NewChecker creates a new health Checker with the given database and version.
func NewChecker(db *sql.DB, version string) *Checker {
	return &Checker{db: db, version: version}
}

// Healthz is the liveness probe - returns 200 if process is alive.
func (c *Checker) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Readyz is the readiness probe - returns 200 only if DB is accessible.
func (c *Checker) Readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := c.db.PingContext(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "ready", "version": c.version})
}
