package jsruntime

import (
	"strings"
	"testing"
)

func TestTranspileTypeScript(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		wantContain string // substring that must be in the output
		wantAbsent  string // substring that must NOT be in the output
		wantErr     bool
	}{
		{
			name:        "basic type annotation",
			code:        `const x: number = 42; x;`,
			wantContain: "42",
			wantAbsent:  ": number",
		},
		{
			name:        "interface removed",
			code:        "interface User { name: string; age: number; }\nconst u: User = { name: \"Alice\", age: 30 }; u;",
			wantContain: "Alice",
			wantAbsent:  "interface",
		},
		{
			name:        "generics stripped",
			code:        "function identity<T>(arg: T): T { return arg; }\nconst r = identity<number>(42); r;",
			wantContain: "42",
			wantAbsent:  "<T>",
		},
		{
			name:        "enum produces JS",
			code:        "enum Dir { Up = \"UP\", Down = \"DOWN\" }\nconst d: Dir = Dir.Up; d;",
			wantContain: "UP",
			wantAbsent:  ": Dir",
		},
		{
			name:        "type alias removed",
			code:        "type ID = string | number;\nconst id: ID = \"abc\"; id;",
			wantContain: "abc",
			wantAbsent:  "type ID",
		},
		{
			name:        "as expression stripped",
			code:        `const x = (42 as number); x;`,
			wantContain: "42",
		},
		{
			name:        "plain JS passthrough",
			code:        `var x = 42; x;`,
			wantContain: "42",
		},
		{
			name:    "empty code",
			code:    "",
			wantContain: "",
		},
		{
			name:    "invalid code",
			code:    `const x: number = ;`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := TranspileTypeScript(tt.code)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				execErr, ok := err.(*ExecError)
				if !ok {
					t.Fatalf("expected *ExecError, got %T", err)
				}
				if execErr.Code != CodeTranspileError {
					t.Errorf("expected code %q, got %q", CodeTranspileError, execErr.Code)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantContain != "" && !strings.Contains(result, tt.wantContain) {
				t.Errorf("output should contain %q, got: %s", tt.wantContain, result)
			}

			if tt.wantAbsent != "" && strings.Contains(result, tt.wantAbsent) {
				t.Errorf("output should NOT contain %q, got: %s", tt.wantAbsent, result)
			}
		})
	}
}

func TestTranspileTypeScript_ErrorDetails(t *testing.T) {
	_, err := TranspileTypeScript(`const x: number = ;`)
	if err == nil {
		t.Fatal("expected error")
	}

	execErr := err.(*ExecError)

	// Should include line info
	if execErr.Line == 0 && execErr.Column == 0 {
		// esbuild may or may not provide location for all errors;
		// at minimum the message should be informative
		if execErr.Message == "" {
			t.Error("expected non-empty error message")
		}
	}
}

func TestLooksLikeTypeScript(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{"plain JS", `var x = 42;`, false},
		{"type annotation string", `const x: string = "hi";`, true},
		{"type annotation number", `const x: number = 1;`, true},
		{"type annotation boolean", `const x: boolean = true;`, true},
		{"type annotation any", `const x: any = null;`, true},
		{"interface", `interface Foo { bar: string; }`, true},
		{"type alias", `type ID = string`, true},
		{"generic T", `function id<T>(x: T): T { return x; }`, true},
		{"as expression", `const x = 42 as number;`, true},
		{"no false positive on colon in object", `var x = { a: 1, b: 2 };`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeTypeScript(tt.code)
			if got != tt.want {
				t.Errorf("looksLikeTypeScript(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
