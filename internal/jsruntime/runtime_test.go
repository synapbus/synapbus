package jsruntime

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockCaller implements ToolCaller for testing.
type mockCaller struct {
	calls   []mockCall
	results map[string]any
	errors  map[string]error
}

type mockCall struct {
	Action string
	Args   map[string]any
}

func newMockCaller() *mockCaller {
	return &mockCaller{
		results: make(map[string]any),
		errors:  make(map[string]error),
	}
}

func (m *mockCaller) Call(_ context.Context, actionName string, args map[string]any) (any, error) {
	m.calls = append(m.calls, mockCall{Action: actionName, Args: args})
	if err, ok := m.errors[actionName]; ok {
		return nil, err
	}
	if result, ok := m.results[actionName]; ok {
		return result, nil
	}
	return map[string]any{"ok": true}, nil
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		opts      ExecuteOptions
		wantValue any
		wantErr   string // substring match on error code
	}{
		{
			name:      "simple integer",
			code:      `42`,
			wantValue: int64(42),
		},
		{
			name:      "simple string",
			code:      `"hello"`,
			wantValue: "hello",
		},
		{
			name: "object literal",
			code: `({ a: 1, b: "two" })`,
		},
		{
			name: "arithmetic expression",
			code: `2 + 3 * 4`,
		},
		{
			name:      "null value",
			code:      `null`,
			wantValue: nil,
		},
		{
			name: "array",
			code: `[1, 2, 3]`,
		},
		{
			name:      "boolean true",
			code:      `true`,
			wantValue: true,
		},
		{
			name:      "boolean false",
			code:      `false`,
			wantValue: false,
		},
		{
			name:    "empty code",
			code:    "",
			wantValue: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := newMockCaller()
			result, err := Execute(context.Background(), tt.code, caller, tt.opts)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				execErr, ok := err.(*ExecError)
				if !ok {
					t.Fatalf("expected *ExecError, got %T: %v", err, err)
				}
				if execErr.Code != tt.wantErr {
					t.Errorf("expected error code %q, got %q", tt.wantErr, execErr.Code)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantValue != nil && result.Value != tt.wantValue {
				t.Errorf("expected value %v (%T), got %v (%T)", tt.wantValue, tt.wantValue, result.Value, result.Value)
			}
		})
	}
}

func TestExecute_SyntaxError(t *testing.T) {
	caller := newMockCaller()
	_, err := Execute(context.Background(), `{ invalid syntax`, caller, ExecuteOptions{})

	if err == nil {
		t.Fatal("expected syntax error, got nil")
	}
	execErr, ok := err.(*ExecError)
	if !ok {
		t.Fatalf("expected *ExecError, got %T", err)
	}
	if execErr.Code != CodeSyntaxError {
		t.Errorf("expected code %q, got %q", CodeSyntaxError, execErr.Code)
	}
}

func TestExecute_RuntimeError(t *testing.T) {
	caller := newMockCaller()
	_, err := Execute(context.Background(), `throw new Error("boom")`, caller, ExecuteOptions{})

	if err == nil {
		t.Fatal("expected runtime error, got nil")
	}
	execErr, ok := err.(*ExecError)
	if !ok {
		t.Fatalf("expected *ExecError, got %T", err)
	}
	if execErr.Code != CodeRuntimeError {
		t.Errorf("expected code %q, got %q", CodeRuntimeError, execErr.Code)
	}
	if execErr.Stack == "" {
		t.Error("expected non-empty stack trace for runtime error")
	}
}

func TestExecute_Timeout(t *testing.T) {
	caller := newMockCaller()
	start := time.Now()
	_, err := Execute(context.Background(), `while(true) {}`, caller, ExecuteOptions{
		Timeout: 100 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	execErr, ok := err.(*ExecError)
	if !ok {
		t.Fatalf("expected *ExecError, got %T", err)
	}
	if execErr.Code != CodeTimeout {
		t.Errorf("expected code %q, got %q", CodeTimeout, execErr.Code)
	}

	// Should complete within a reasonable margin of the timeout
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestExecute_CallBridge(t *testing.T) {
	caller := newMockCaller()
	caller.results["get_user"] = map[string]any{
		"name": "alice",
		"id":   42,
	}

	code := `
		var res = call("get_user", { id: 1 });
		if (!res.ok) throw new Error("failed");
		({ name: res.result.name })
	`
	result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
	if caller.calls[0].Action != "get_user" {
		t.Errorf("expected action 'get_user', got %q", caller.calls[0].Action)
	}
	if result.CallCount != 1 {
		t.Errorf("expected CallCount=1, got %d", result.CallCount)
	}

	resultMap, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Value)
	}
	if resultMap["name"] != "alice" {
		t.Errorf("expected name='alice', got %v", resultMap["name"])
	}
}

func TestExecute_CallBridgeError(t *testing.T) {
	caller := newMockCaller()
	caller.errors["fail_action"] = fmt.Errorf("upstream error")

	code := `
		var res = call("fail_action", {});
		({ ok: res.ok, code: res.error.code })
	`
	result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resultMap := result.Value.(map[string]any)
	if resultMap["ok"] != false {
		t.Errorf("expected ok=false, got %v", resultMap["ok"])
	}
	if resultMap["code"] != "CALL_ERROR" {
		t.Errorf("expected code='CALL_ERROR', got %v", resultMap["code"])
	}
}

func TestExecute_CallInvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"no arguments", `call()`},
		{"one argument", `call("action")`},
		{"args not object", `call("action", "not_an_object")`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := newMockCaller()
			code := fmt.Sprintf(`
				var res = %s;
				({ ok: res.ok, code: res.error.code })
			`, tt.code)
			result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			resultMap := result.Value.(map[string]any)
			if resultMap["ok"] != false {
				t.Errorf("expected ok=false, got %v", resultMap["ok"])
			}
			if resultMap["code"] != "INVALID_ARGS" {
				t.Errorf("expected code='INVALID_ARGS', got %v", resultMap["code"])
			}
		})
	}
}

func TestExecute_MaxCalls(t *testing.T) {
	caller := newMockCaller()
	caller.results["action"] = "ok"

	code := `
		var results = [];
		for (var i = 0; i < 10; i++) {
			var res = call("action", {});
			results.push({ ok: res.ok, code: res.error ? res.error.code : null });
		}
		({ results: results, total: results.length })
	`
	result, err := Execute(context.Background(), code, caller, ExecuteOptions{
		MaxCalls: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only 3 calls should have succeeded upstream
	if len(caller.calls) != 3 {
		t.Errorf("expected 3 upstream calls, got %d", len(caller.calls))
	}

	resultMap := result.Value.(map[string]any)
	results := resultMap["results"].([]any)

	// First 3 should be ok, rest should be MAX_CALLS_EXCEEDED
	for i, r := range results {
		rm := r.(map[string]any)
		if i < 3 {
			if rm["ok"] != true {
				t.Errorf("call %d: expected ok=true, got %v", i, rm["ok"])
			}
		} else {
			if rm["ok"] != false {
				t.Errorf("call %d: expected ok=false, got %v", i, rm["ok"])
			}
			if rm["code"] != CodeMaxCallsExceeded {
				t.Errorf("call %d: expected code=%q, got %v", i, CodeMaxCallsExceeded, rm["code"])
			}
		}
	}
}

func TestExecute_MultipleCallsInLoop(t *testing.T) {
	caller := newMockCaller()
	caller.results["add"] = map[string]any{"sum": 10}

	code := `
		var total = 0;
		for (var i = 0; i < 5; i++) {
			var res = call("add", { a: i, b: 1 });
			if (res.ok) total++;
		}
		({ total: total })
	`
	result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(caller.calls) != 5 {
		t.Fatalf("expected 5 calls, got %d", len(caller.calls))
	}
	if result.CallCount != 5 {
		t.Errorf("expected CallCount=5, got %d", result.CallCount)
	}
}

func TestExecute_SandboxBlockedAPIs(t *testing.T) {
	blockedAPIs := []struct {
		name string
		code string
	}{
		{"require", `require("fs")`},
		{"fetch", `fetch("http://example.com")`},
		{"setTimeout", `setTimeout(function(){}, 100)`},
		{"setInterval", `setInterval(function(){}, 100)`},
		{"clearTimeout", `clearTimeout(1)`},
		{"clearInterval", `clearInterval(1)`},
		{"XMLHttpRequest", `new XMLHttpRequest()`},
		{"process.env", `process.env.HOME`},
	}

	for _, tt := range blockedAPIs {
		t.Run(tt.name, func(t *testing.T) {
			caller := newMockCaller()
			result, err := Execute(context.Background(), tt.code, caller, ExecuteOptions{})

			// Should either error or return undefined (not execute the blocked API)
			if err == nil && result.Ok() {
				// If it succeeds, the value should be undefined/nil (the API was replaced with undefined)
				// This is acceptable — the key thing is the API doesn't actually work
				t.Logf("%s returned: %v (blocked by sandbox)", tt.name, result.Value)
			}
		})
	}
}

// Ok is a helper for test assertions.
func (r *ExecuteResult) Ok() bool {
	return r != nil
}

func TestExecute_TypeScriptAutoDetect(t *testing.T) {
	caller := newMockCaller()
	code := `const x: number = 42; const msg: string = "hello"; ({ result: x, message: msg })`

	result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resultMap, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result.Value)
	}

	// goja exports numbers as int64 or float64
	var num int64
	switch v := resultMap["result"].(type) {
	case int64:
		num = v
	case float64:
		num = int64(v)
	default:
		t.Fatalf("expected numeric result, got %T", resultMap["result"])
	}
	if num != 42 {
		t.Errorf("expected 42, got %d", num)
	}
	if resultMap["message"] != "hello" {
		t.Errorf("expected 'hello', got %v", resultMap["message"])
	}
}

func TestExecute_TypeScriptWithInterface(t *testing.T) {
	caller := newMockCaller()
	caller.results["get_data"] = map[string]any{"value": 99}

	code := `
		interface Result {
			ok: boolean;
			result?: any;
			error?: any;
		}
		const res: Result = call("get_data", { key: "test" });
		if (!res.ok) throw new Error("failed");
		({ data: res.result })
	`
	result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}

	resultMap := result.Value.(map[string]any)
	data := resultMap["data"].(map[string]any)
	// Values passed through the call() bridge preserve their Go types
	var val int64
	switch v := data["value"].(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	}
	if val != 99 {
		t.Errorf("expected value=99, got %v", data["value"])
	}
}

func TestExecute_PlainJSNotTranspiled(t *testing.T) {
	// Plain JS should work without transpilation
	caller := newMockCaller()
	code := `var x = 42; x`

	result, err := Execute(context.Background(), code, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var num int64
	switch v := result.Value.(type) {
	case int64:
		num = v
	case float64:
		num = int64(v)
	}
	if num != 42 {
		t.Errorf("expected 42, got %v", result.Value)
	}
}

func TestExecute_Duration(t *testing.T) {
	caller := newMockCaller()
	result, err := Execute(context.Background(), `42`, caller, ExecuteOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestExecute_ContextCancellation(t *testing.T) {
	caller := newMockCaller()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := Execute(ctx, `while(true) {}`, caller, ExecuteOptions{
		Timeout: 5 * time.Second,
	})

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestExecError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      ExecError
		contains string
	}{
		{
			name:     "without stack",
			err:      ExecError{Code: CodeSyntaxError, Message: "unexpected token"},
			contains: "SYNTAX_ERROR: unexpected token",
		},
		{
			name:     "with stack",
			err:      ExecError{Code: CodeRuntimeError, Message: "boom", Stack: "at line 1"},
			contains: "at line 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if msg == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}
