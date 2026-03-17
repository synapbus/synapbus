package api

import (
	"net/http"
)

// VersionHandler serves version information.
// The version is set at build time via -ldflags.
type VersionHandler struct {
	version string
}

// NewVersionHandler creates a new version handler.
func NewVersionHandler(version string) *VersionHandler {
	return &VersionHandler{version: version}
}

// GetVersion handles GET /api/version.
func (h *VersionHandler) GetVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": h.version,
		"repo":    "https://github.com/synapbus/synapbus",
	})
}
