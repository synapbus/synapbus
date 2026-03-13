package mcp

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthStatus represents the health check response.
type HealthStatus struct {
	Status            string `json:"status"`
	Version           string `json:"version"`
	Uptime            string `json:"uptime"`
	ActiveConnections int    `json:"active_connections"`
}

// NewHealthHandler creates an HTTP handler for health checks.
func NewHealthHandler(connMgr *ConnectionManager, version string, startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := HealthStatus{
			Status:            "ok",
			Version:           version,
			Uptime:            time.Since(startTime).Round(time.Second).String(),
			ActiveConnections: connMgr.Count(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(status)
	}
}
