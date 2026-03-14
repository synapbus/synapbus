package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
)

// mockDispatcher records events it receives and optionally returns an error.
type mockDispatcher struct {
	mu     sync.Mutex
	events []MessageEvent
	err    error
}

func (m *mockDispatcher) Dispatch(ctx context.Context, event MessageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return m.err
}

func (m *mockDispatcher) getEvents() []MessageEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]MessageEvent, len(m.events))
	copy(cp, m.events)
	return cp
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestMultiDispatcher_DispatchesToAll(t *testing.T) {
	d1 := &mockDispatcher{}
	d2 := &mockDispatcher{}
	d3 := &mockDispatcher{}

	md := NewMultiDispatcher(testLogger(), d1, d2, d3)
	ctx := context.Background()

	event := MessageEvent{
		EventType: "message.received",
		MessageID: 42,
		FromAgent: "sender",
		ToAgent:   "receiver",
		Body:      "hello",
	}

	err := md.Dispatch(ctx, event)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	for i, d := range []*mockDispatcher{d1, d2, d3} {
		events := d.getEvents()
		if len(events) != 1 {
			t.Errorf("dispatcher %d: got %d events, want 1", i, len(events))
			continue
		}
		if events[0].MessageID != 42 {
			t.Errorf("dispatcher %d: message_id = %d, want 42", i, events[0].MessageID)
		}
		if events[0].FromAgent != "sender" {
			t.Errorf("dispatcher %d: from_agent = %q, want %q", i, events[0].FromAgent, "sender")
		}
	}
}

func TestMultiDispatcher_ContinuesOnError(t *testing.T) {
	d1 := &mockDispatcher{err: errors.New("d1 failed")}
	d2 := &mockDispatcher{} // should still receive the event
	d3 := &mockDispatcher{err: errors.New("d3 failed")}

	md := NewMultiDispatcher(testLogger(), d1, d2, d3)
	ctx := context.Background()

	event := MessageEvent{
		EventType: "message.received",
		MessageID: 99,
		FromAgent: "agent-x",
		ToAgent:   "agent-y",
		Body:      "test",
	}

	// MultiDispatcher always returns nil (best-effort)
	err := md.Dispatch(ctx, event)
	if err != nil {
		t.Fatalf("Dispatch() should return nil even when dispatchers fail, got %v", err)
	}

	// All three should have received the event
	for i, d := range []*mockDispatcher{d1, d2, d3} {
		events := d.getEvents()
		if len(events) != 1 {
			t.Errorf("dispatcher %d: got %d events, want 1 (should receive event even if it returns error)", i, len(events))
		}
	}
}

func TestMultiDispatcher_EmptyDispatchers(t *testing.T) {
	md := NewMultiDispatcher(testLogger())
	ctx := context.Background()

	event := MessageEvent{
		EventType: "message.received",
		MessageID: 1,
	}

	err := md.Dispatch(ctx, event)
	if err != nil {
		t.Fatalf("Dispatch() with no dispatchers should not error, got %v", err)
	}
}

func TestExtractMentions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "single mention",
			body: "Hello @agent-1, how are you?",
			want: []string{"agent-1"},
		},
		{
			name: "multiple mentions",
			body: "@alice please talk to @bob about this",
			want: []string{"alice", "bob"},
		},
		{
			name: "duplicate mentions deduplicated",
			body: "@alice said hello, and @alice said goodbye",
			want: []string{"alice"},
		},
		{
			name: "mentions with underscores",
			body: "cc @my_agent_123",
			want: []string{"my_agent_123"},
		},
		{
			name: "mention with hyphen",
			body: "ask @code-reviewer",
			want: []string{"code-reviewer"},
		},
		{
			name: "no mentions",
			body: "this is a plain message with no mentions",
			want: nil,
		},
		{
			name: "empty string",
			body: "",
			want: nil,
		},
		{
			name: "mention at start",
			body: "@first is mentioned",
			want: []string{"first"},
		},
		{
			name: "mention at end",
			body: "mentioned at end @last",
			want: []string{"last"},
		},
		{
			name: "mixed duplicates and unique",
			body: "@alice @bob @alice @charlie @bob",
			want: []string{"alice", "bob", "charlie"},
		},
		{
			name: "email-like pattern",
			body: "contact user@example.com",
			want: []string{"example"},
		},
		{
			name: "alphanumeric agent name",
			body: "Hey @Agent42 check this",
			want: []string{"Agent42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMentions(tt.body)

			if tt.want == nil {
				if got != nil {
					t.Errorf("ExtractMentions(%q) = %v, want nil", tt.body, got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Fatalf("ExtractMentions(%q) = %v (len %d), want %v (len %d)", tt.body, got, len(got), tt.want, len(tt.want))
			}

			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("ExtractMentions(%q)[%d] = %q, want %q", tt.body, i, g, tt.want[i])
				}
			}
		})
	}
}
