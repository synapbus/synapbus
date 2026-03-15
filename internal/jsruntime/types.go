package jsruntime

import (
	"context"
	"fmt"
	"time"
)

// ToolCaller is implemented by the action registry to bridge call() invocations
// from JavaScript code to the host application.
type ToolCaller interface {
	Call(ctx context.Context, actionName string, args map[string]any) (any, error)
}

// ExecuteOptions configures a single code execution.
type ExecuteOptions struct {
	Timeout     time.Duration // Maximum execution time. Default 120s.
	MaxCalls    int           // Maximum number of call() invocations. Default 50.
	MaxMemoryMB int           // Memory limit hint in MB. Default 128.
}

// defaults fills in zero-value fields with sensible defaults.
func (o ExecuteOptions) defaults() ExecuteOptions {
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	if o.MaxCalls <= 0 {
		o.MaxCalls = 50
	}
	if o.MaxMemoryMB <= 0 {
		o.MaxMemoryMB = 128
	}
	return o
}

// ExecuteResult holds the output of a successful execution.
type ExecuteResult struct {
	Value     any           // Final expression value (JSON-serializable)
	CallCount int           // Number of call() invocations made
	Duration  time.Duration // Wall-clock execution time
}

// ExecError wraps execution errors with structured context.
type ExecError struct {
	Code    string // SYNTAX_ERROR, RUNTIME_ERROR, TIMEOUT, MAX_CALLS_EXCEEDED, TRANSPILE_ERROR
	Message string // Human-readable error description
	Stack   string // JS stack trace if available
	Line    int    // Source line if available
	Column  int    // Source column if available
}

// Error implements the error interface.
func (e *ExecError) Error() string {
	if e.Stack != "" {
		return fmt.Sprintf("%s: %s\n%s", e.Code, e.Message, e.Stack)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Error code constants.
const (
	CodeSyntaxError       = "SYNTAX_ERROR"
	CodeRuntimeError      = "RUNTIME_ERROR"
	CodeTimeout           = "TIMEOUT"
	CodeMaxCallsExceeded  = "MAX_CALLS_EXCEEDED"
	CodeTranspileError    = "TRANSPILE_ERROR"
)
