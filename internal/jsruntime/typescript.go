package jsruntime

import (
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// TranspileTypeScript transpiles TypeScript code to JavaScript using esbuild.
// It performs type-stripping only (no bundling, no type checking).
// Target is ES2020 for compatibility with goja.
func TranspileTypeScript(code string) (string, error) {
	result := api.Transform(code, api.TransformOptions{
		Loader: api.LoaderTS,
		Target: api.ES2020,
	})

	if len(result.Errors) > 0 {
		msg := result.Errors[0]
		e := &ExecError{
			Code:    CodeTranspileError,
			Message: fmt.Sprintf("TypeScript transpilation failed: %s", msg.Text),
		}
		if msg.Location != nil {
			e.Line = msg.Location.Line
			e.Column = msg.Location.Column
			e.Message = fmt.Sprintf("TypeScript transpilation failed at line %d, column %d: %s",
				msg.Location.Line, msg.Location.Column, msg.Text)
		}
		return "", e
	}

	return string(result.Code), nil
}

// looksLikeTypeScript uses simple heuristics to detect TypeScript code.
// It checks for common TypeScript-only syntax patterns.
func looksLikeTypeScript(code string) bool {
	// Check for type annotations like `: string`, `: number`, `: boolean`, `: any`
	typeAnnotationPatterns := []string{
		": string",
		": number",
		": boolean",
		": any",
		": void",
		": never",
		": unknown",
	}
	for _, pattern := range typeAnnotationPatterns {
		if strings.Contains(code, pattern) {
			return true
		}
	}

	// Check for interface declarations
	if strings.Contains(code, "interface ") && strings.Contains(code, "{") {
		return true
	}

	// Check for type aliases
	if strings.Contains(code, "type ") && strings.Contains(code, "=") {
		return true
	}

	// Check for generic type parameters like <T> or <T,U>
	// Simple heuristic: look for <identifier> patterns not preceded by comparison operators
	if strings.Contains(code, "<T>") || strings.Contains(code, "<T,") || strings.Contains(code, "<T ") {
		return true
	}

	// Check for 'as' type assertions
	if strings.Contains(code, " as ") {
		return true
	}

	return false
}
