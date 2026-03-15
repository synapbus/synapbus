package jsruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// Execute runs JavaScript or TypeScript code in a sandboxed goja VM.
//
// If the code looks like TypeScript (contains type annotations, interfaces,
// generics, etc.), it is automatically transpiled to JavaScript before execution.
//
// A global `call(actionName, args)` function is provided that bridges to the
// supplied ToolCaller. It returns {ok: true, result: ...} or {ok: false, error: ...}.
//
// The value of the last expression in the code is returned as ExecuteResult.Value.
func Execute(ctx context.Context, code string, caller ToolCaller, opts ExecuteOptions) (*ExecuteResult, error) {
	opts = opts.defaults()
	start := time.Now()

	// Auto-detect and transpile TypeScript
	if looksLikeTypeScript(code) {
		transpiled, err := TranspileTypeScript(code)
		if err != nil {
			return nil, err
		}
		code = transpiled
	}

	// Handle empty code
	if len(code) == 0 {
		return &ExecuteResult{
			Value:     nil,
			CallCount: 0,
			Duration:  time.Since(start),
		}, nil
	}

	// Create VM and set up sandbox
	vm := goja.New()
	setupSandbox(vm)

	// Track call count
	var callCount int32

	// Register call() global function
	callFn := makeCallFunction(ctx, vm, caller, opts.MaxCalls, &callCount)
	if err := vm.Set("call", callFn); err != nil {
		return nil, &ExecError{
			Code:    CodeRuntimeError,
			Message: fmt.Sprintf("failed to register call() function: %v", err),
		}
	}

	// Set up timeout via context
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// Run in a goroutine so we can enforce timeout
	type execResult struct {
		value goja.Value
		err   error
	}
	resultCh := make(chan execResult, 1)

	go func() {
		// Compile first to get better syntax error reporting
		prog, compileErr := goja.Compile("", code, false)
		if compileErr != nil {
			resultCh <- execResult{err: compileErr}
			return
		}
		val, runErr := vm.RunProgram(prog)
		resultCh <- execResult{value: val, err: runErr}
	}()

	// Monitor for timeout — interrupt the VM
	go func() {
		<-timeoutCtx.Done()
		if timeoutCtx.Err() == context.DeadlineExceeded {
			vm.Interrupt("execution timeout")
		}
	}()

	select {
	case res := <-resultCh:
		duration := time.Since(start)

		if res.err != nil {
			return nil, classifyError(res.err)
		}

		// Export result
		exported := res.value.Export()

		// Validate JSON serializability
		if err := validateSerializable(exported); err != nil {
			return nil, &ExecError{
				Code:    CodeRuntimeError,
				Message: fmt.Sprintf("result is not JSON-serializable: %v", err),
			}
		}

		return &ExecuteResult{
			Value:     exported,
			CallCount: int(atomic.LoadInt32(&callCount)),
			Duration:  duration,
		}, nil

	case <-timeoutCtx.Done():
		vm.Interrupt("execution timeout")
		return nil, &ExecError{
			Code:    CodeTimeout,
			Message: fmt.Sprintf("execution exceeded timeout of %s", opts.Timeout),
		}
	}
}

// setupSandbox disables dangerous global APIs in the VM.
func setupSandbox(vm *goja.Runtime) {
	// Disable module loading
	vm.Set("require", goja.Undefined())
	vm.Set("import", goja.Undefined())

	// Disable async operations
	vm.Set("setTimeout", goja.Undefined())
	vm.Set("setInterval", goja.Undefined())
	vm.Set("clearTimeout", goja.Undefined())
	vm.Set("clearInterval", goja.Undefined())

	// Disable network access
	vm.Set("fetch", goja.Undefined())
	vm.Set("XMLHttpRequest", goja.Undefined())

	// Disable process/system access
	vm.Set("process", goja.Undefined())

	// Note: goja does not provide filesystem or network access by default,
	// so we only need to block APIs that could be expected by JS code.
}

// makeCallFunction creates the call(actionName, args) bridge function.
func makeCallFunction(
	ctx context.Context,
	vm *goja.Runtime,
	caller ToolCaller,
	maxCalls int,
	callCount *int32,
) func(goja.FunctionCall) goja.Value {
	return func(fc goja.FunctionCall) goja.Value {
		// Validate arguments
		if len(fc.Arguments) < 2 {
			return vm.ToValue(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code":    "INVALID_ARGS",
					"message": "call() requires 2 arguments: actionName (string), args (object)",
				},
			})
		}

		// Extract and validate actionName
		actionName := fc.Arguments[0].String()
		if actionName == "" || actionName == "undefined" {
			return vm.ToValue(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code":    "INVALID_ARGS",
					"message": "actionName must be a non-empty string",
				},
			})
		}

		// Extract and validate args
		argsExported := fc.Arguments[1].Export()
		args, ok := argsExported.(map[string]any)
		if !ok {
			return vm.ToValue(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code":    "INVALID_ARGS",
					"message": "args must be an object",
				},
			})
		}

		// Enforce MaxCalls limit
		count := atomic.AddInt32(callCount, 1)
		if int(count) > maxCalls {
			return vm.ToValue(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code":    CodeMaxCallsExceeded,
					"message": fmt.Sprintf("exceeded maximum of %d call() invocations", maxCalls),
				},
			})
		}

		// Bridge to ToolCaller with context propagation
		result, err := caller.Call(ctx, actionName, args)
		if err != nil {
			slog.Warn("call() bridge error",
				"action", actionName,
				"error", err,
			)
			return vm.ToValue(map[string]any{
				"ok": false,
				"error": map[string]any{
					"code":    "CALL_ERROR",
					"message": err.Error(),
				},
			})
		}

		return vm.ToValue(map[string]any{
			"ok":     true,
			"result": result,
		})
	}
}

// classifyError converts a goja error into a structured ExecError.
func classifyError(err error) *ExecError {
	if err == nil {
		return nil
	}

	// Check for interrupt (timeout)
	if interrupted, ok := err.(*goja.InterruptedError); ok {
		return &ExecError{
			Code:    CodeTimeout,
			Message: interrupted.Error(),
		}
	}

	// Check for syntax error from compilation
	if syntaxErr, ok := err.(*goja.CompilerSyntaxError); ok {
		return &ExecError{
			Code:    CodeSyntaxError,
			Message: syntaxErr.Error(),
		}
	}

	// Check for JS exception (runtime error)
	if exception, ok := err.(*goja.Exception); ok {
		return &ExecError{
			Code:    CodeRuntimeError,
			Message: exception.Error(),
			Stack:   exception.String(),
		}
	}

	// Generic error
	return &ExecError{
		Code:    CodeRuntimeError,
		Message: err.Error(),
	}
}

// validateSerializable checks whether the value can be marshaled to JSON.
func validateSerializable(value any) error {
	if value == nil {
		return nil
	}
	_, err := json.Marshal(value)
	return err
}
