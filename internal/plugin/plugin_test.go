package plugin_test

import (
	"testing"

	"github.com/synapbus/synapbus/internal/plugin"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"wiki", true},
		{"runner_docker", true},
		{"hello_world_42", true},
		{"a1", true},
		{"", false},
		{"X", false},
		{"1foo", false},
		{"foo-bar", false},
		{"foo bar", false},
		{"FOO", false},
		{"toolongname_toolongname_toolongname_toolongname", false}, // 47 chars
		{"a", false}, // requires 2+ chars per regex
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := plugin.ValidateName(tc.name)
			if got != tc.want {
				t.Fatalf("ValidateName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
