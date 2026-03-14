package dispatcher

import (
	"context"
	"log/slog"
	"regexp"
)

// MessageEvent represents a messaging event to be dispatched to delivery mechanisms.
type MessageEvent struct {
	EventType       string   // "message.received", "message.mentioned", "channel.message"
	MessageID       int64
	FromAgent       string
	ToAgent         string   // for DMs
	Channel         string   // for channel messages (empty for DMs)
	Body            string
	Priority        int
	Metadata        string   // JSON string
	Depth           int      // webhook chain depth (0 for original, incremented per hop)
	MentionedAgents []string // agents @mentioned in the body
}

// EventDispatcher is the interface for delivering message events.
type EventDispatcher interface {
	Dispatch(ctx context.Context, event MessageEvent) error
}

// MultiDispatcher fans out events to multiple EventDispatchers.
type MultiDispatcher struct {
	dispatchers []EventDispatcher
	logger      *slog.Logger
}

// NewMultiDispatcher creates a MultiDispatcher that delivers events to each
// provided dispatcher in sequence.
func NewMultiDispatcher(logger *slog.Logger, dispatchers ...EventDispatcher) *MultiDispatcher {
	return &MultiDispatcher{
		dispatchers: dispatchers,
		logger:      logger,
	}
}

// Dispatch sends the event to every registered dispatcher. Delivery is
// best-effort: errors are logged but never returned so that message sending
// is never blocked by a failing delivery mechanism.
func (m *MultiDispatcher) Dispatch(ctx context.Context, event MessageEvent) error {
	for _, d := range m.dispatchers {
		if err := d.Dispatch(ctx, event); err != nil {
			m.logger.ErrorContext(ctx, "dispatcher failed",
				"event_type", event.EventType,
				"message_id", event.MessageID,
				"error", err,
			)
		}
	}
	return nil
}

// mentionRe matches @agent-name patterns where the agent name consists of
// alphanumeric characters, hyphens, and underscores.
var mentionRe = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9_-]*)`)

// ExtractMentions returns a deduplicated list of agent names mentioned in body
// via @agent-name syntax. The returned names do not include the @ prefix.
func ExtractMentions(body string) []string {
	matches := mentionRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	var result []string
	for _, m := range matches {
		name := m[1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}
