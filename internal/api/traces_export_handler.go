package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/smart-mcp-proxy/synapbus/internal/trace"
)

// ExportTraces handles GET /api/traces/export.
func (h *TracesHandler) ExportTraces(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	filter := trace.TraceFilter{
		OwnerID:   fmt.Sprintf("%d", ownerID),
		AgentName: r.URL.Query().Get("agent_name"),
		Action:    r.URL.Query().Get("action"),
	}

	if since := r.URL.Query().Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			http.Error(w, `{"error":"invalid 'since' parameter"}`, http.StatusBadRequest)
			return
		}
		filter.Since = &t
	}

	if until := r.URL.Query().Get("until"); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			http.Error(w, `{"error":"invalid 'until' parameter"}`, http.StatusBadRequest)
			return
		}
		filter.Until = &t
	}

	// Determine format
	format := r.URL.Query().Get("format")
	if format == "" {
		accept := r.Header.Get("Accept")
		switch accept {
		case "text/csv":
			format = "csv"
		default:
			format = "json"
		}
	}

	dateStr := time.Now().Format("2006-01-02")

	switch format {
	case "csv":
		h.exportCSV(w, r, filter, dateStr)
	default:
		h.exportJSON(w, r, filter, dateStr)
	}
}

func (h *TracesHandler) exportJSON(w http.ResponseWriter, r *http.Request, filter trace.TraceFilter, dateStr string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traces-%s.json"`, dateStr))
	w.Header().Set("Transfer-Encoding", "chunked")

	// Write opening bracket
	w.Write([]byte("["))

	first := true
	err := h.store.QueryStream(r.Context(), filter, func(t trace.Trace) error {
		if !first {
			w.Write([]byte(","))
		}
		first = false

		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})

	if err != nil {
		// Best effort — headers already sent
		w.Write([]byte(fmt.Sprintf(`]`)))
		return
	}

	w.Write([]byte("]"))
}

func (h *TracesHandler) exportCSV(w http.ResponseWriter, r *http.Request, filter trace.TraceFilter, dateStr string) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traces-%s.csv"`, dateStr))
	w.Header().Set("Transfer-Encoding", "chunked")

	cw := csv.NewWriter(w)
	defer cw.Flush()

	// Write header
	cw.Write([]string{"id", "agent_name", "action", "details", "timestamp"})

	h.store.QueryStream(r.Context(), filter, func(t trace.Trace) error {
		return cw.Write([]string{
			strconv.FormatInt(t.ID, 10),
			t.AgentName,
			t.Action,
			string(t.Details),
			t.Timestamp.Format(time.RFC3339),
		})
	})
}
