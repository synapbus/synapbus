package k8s

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizeJobName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple lowercase",
			input: "synapbus-agent-123",
			want:  "synapbus-agent-123",
		},
		{
			name:  "uppercase converted to lowercase",
			input: "SynapBus-Agent-ABC",
			want:  "synapbus-agent-abc",
		},
		{
			name:  "special characters replaced with hyphens",
			input: "synapbus_agent.test@foo",
			want:  "synapbus-agent-test-foo",
		},
		{
			name:  "leading hyphens trimmed",
			input: "---leading",
			want:  "leading",
		},
		{
			name:  "trailing hyphens trimmed",
			input: "trailing---",
			want:  "trailing",
		},
		{
			name:  "max 63 characters",
			input: strings.Repeat("a", 100),
			want:  strings.Repeat("a", 63),
		},
		{
			name:  "long name with special chars truncated to 63",
			input: "synapbus-" + strings.Repeat("x", 100) + "-final",
			want:  "synapbus-" + strings.Repeat("x", 54),
		},
		{
			name:  "alphanumeric preserved",
			input: "abc123def456",
			want:  "abc123def456",
		},
		{
			name:  "spaces become hyphens",
			input: "my job name",
			want:  "my-job-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeJobName(tt.input)

			if got != tt.want {
				t.Errorf("sanitizeJobName(%q) = %q, want %q", tt.input, got, tt.want)
			}

			// Verify invariants
			if len(got) > 63 {
				t.Errorf("result length %d exceeds 63", len(got))
			}
			if got != strings.ToLower(got) {
				t.Errorf("result %q is not all lowercase", got)
			}
			if strings.HasPrefix(got, "-") {
				t.Errorf("result %q starts with hyphen", got)
			}
			if strings.HasSuffix(got, "-") {
				t.Errorf("result %q ends with hyphen", got)
			}
			// Check DNS-safe: only a-z, 0-9, -
			for _, r := range got {
				if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
					t.Errorf("result %q contains invalid rune %q", got, string(r))
				}
			}
		})
	}
}

func TestTruncateBody(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		maxLen int
		want   string
	}{
		{
			name:   "short body unchanged",
			body:   "hello",
			maxLen: 100,
			want:   "hello",
		},
		{
			name:   "exact length unchanged",
			body:   "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "long body truncated",
			body:   "hello world this is a long message",
			maxLen: 11,
			want:   "hello world",
		},
		{
			name:   "empty body",
			body:   "",
			maxLen: 100,
			want:   "",
		},
		{
			name:   "max zero truncates all",
			body:   "hello",
			maxLen: 0,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBody(tt.body, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateBody(%q, %d) = %q, want %q", tt.body, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestNoopRunner(t *testing.T) {
	runner := NewNoopRunner()

	t.Run("IsAvailable returns false", func(t *testing.T) {
		if runner.IsAvailable() {
			t.Error("NoopRunner.IsAvailable() should return false")
		}
	})

	t.Run("GetNamespace returns empty string", func(t *testing.T) {
		if ns := runner.GetNamespace(); ns != "" {
			t.Errorf("NoopRunner.GetNamespace() = %q, want empty string", ns)
		}
	})

	t.Run("CreateJob returns error", func(t *testing.T) {
		ctx := context.Background()
		handler := &K8sHandler{AgentName: "test", Image: "test:v1"}
		msg := &JobMessage{MessageID: 1, Body: "test"}
		_, err := runner.CreateJob(ctx, handler, msg)
		if err == nil {
			t.Error("NoopRunner.CreateJob() should return error")
		}
	})

	t.Run("GetJobLogs returns error", func(t *testing.T) {
		ctx := context.Background()
		_, err := runner.GetJobLogs(ctx, "ns", "job-1")
		if err == nil {
			t.Error("NoopRunner.GetJobLogs() should return error")
		}
	})
}
