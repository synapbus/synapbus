package trace

import (
	"encoding/json"
	"time"
)

// Trace represents a single recorded agent action.
type Trace struct {
	ID        int64           `json:"id"`
	OwnerID   string          `json:"owner_id"`
	AgentName string          `json:"agent_name"`
	Action    string          `json:"action"`
	Details   json.RawMessage `json:"details"`
	Error     string          `json:"error,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// TraceFilter defines query parameters for filtering traces.
type TraceFilter struct {
	OwnerID   string     `json:"owner_id"`
	AgentName string     `json:"agent_name,omitempty"`
	Action    string     `json:"action,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Until     *time.Time `json:"until,omitempty"`
	Page      int        `json:"page"`
	PageSize  int        `json:"page_size"`
}

// Normalize sets defaults for missing filter values.
func (f *TraceFilter) Normalize() {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.PageSize > 200 {
		f.PageSize = 200
	}
}

// Offset returns the SQL offset for the current page.
func (f *TraceFilter) Offset() int {
	return (f.Page - 1) * f.PageSize
}

// TraceExportFormat defines the output format for trace exports.
type TraceExportFormat string

const (
	ExportFormatJSON TraceExportFormat = "json"
	ExportFormatCSV  TraceExportFormat = "csv"
)
