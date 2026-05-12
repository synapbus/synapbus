// Auto-link emitter for feature 020 — when a new memory-eligible
// message is inserted, derive `mention`, `reply_to`, and
// `channel_cooccurrence` links automatically and write them with
// `created_by = "auto:<rule>"`. The dream-agent's `memory_add_link`
// tool rejects these auto-types (see memory_links.go) so this is the
// only path that creates them.
//
// Wiring: register an AutoLinkListener with
// `MessagingService.AddMessageListener`. The listener fires after
// every successful message insert. Failures are logged but never
// propagated — auto-links are best-effort.
package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
)

// mentionPattern matches `@agent-name` in the body. Hyphens and digits
// allowed; case-insensitive; bounded by non-word characters or string
// edges. Matches the existing mentions.go convention.
var autoMentionPattern = regexp.MustCompile(`@([A-Za-z][A-Za-z0-9_\-]{1,63})`)

// AutoLinkListener implements MessageListener and writes the three
// auto-link types per OnMessageSent invocation.
type AutoLinkListener struct {
	db     *sql.DB
	links  *LinkStore
	logger *slog.Logger
}

// NewAutoLinkListener returns a listener over db + links.
func NewAutoLinkListener(db *sql.DB, links *LinkStore) *AutoLinkListener {
	return &AutoLinkListener{
		db:     db,
		links:  links,
		logger: slog.Default().With("component", "auto-links"),
	}
}

// OnMessageSent implements MessageListener. Runs the three rules in
// order. Each rule independently best-effort.
func (l *AutoLinkListener) OnMessageSent(ctx context.Context, msg *Message) {
	if l == nil || l.db == nil || l.links == nil || msg == nil || msg.ID == 0 {
		return
	}
	// Only run for memory-channel messages — auto-links on every DM
	// would pollute the link table.
	if msg.ChannelID == nil {
		return
	}
	channelName, err := channelNameByID(ctx, l.db, *msg.ChannelID)
	if err != nil || !matchesMemoryChannelName(channelName) {
		return
	}
	ownerID := resolveOwnerString(ctx, l.db, msg.FromAgent)
	if ownerID == "" {
		return
	}

	// 1. reply_to: simple metadata or msg.ReplyTo column.
	if msg.ReplyTo != nil && *msg.ReplyTo != 0 {
		if _, err := l.links.Add(ctx, msg.ID, *msg.ReplyTo, "reply_to", ownerID, "auto:reply_to", nil); err != nil {
			l.logger.Debug("auto reply_to failed", "msg", msg.ID, "error", err)
		}
	} else if reply := extractReplyToFromMetadata(msg.Metadata); reply != 0 {
		if _, err := l.links.Add(ctx, msg.ID, reply, "reply_to", ownerID, "auto:reply_to", nil); err != nil {
			l.logger.Debug("auto reply_to (meta) failed", "msg", msg.ID, "error", err)
		}
	}

	// 2. mention: @agent-name → latest message from that agent in the
	//    same channel.
	for _, m := range autoMentionPattern.FindAllStringSubmatch(msg.Body, -1) {
		if len(m) < 2 {
			continue
		}
		target := m[1]
		if target == msg.FromAgent {
			continue
		}
		dst := mostRecentMessageFromAgentInChannel(ctx, l.db, *msg.ChannelID, target, msg.ID)
		if dst == 0 {
			continue
		}
		if _, err := l.links.Add(ctx, msg.ID, dst, "mention", ownerID, "auto:mention", nil); err != nil {
			l.logger.Debug("auto mention failed", "msg", msg.ID, "to", target, "error", err)
		}
	}

	// 3. channel_cooccurrence: previous-most-recent message in the
	//    same channel.
	prev := mostRecentMessageInChannel(ctx, l.db, *msg.ChannelID, msg.ID)
	if prev != 0 {
		if _, err := l.links.Add(ctx, msg.ID, prev, "channel_cooccurrence", ownerID, "auto:channel_cooccurrence", nil); err != nil {
			l.logger.Debug("auto cooccurrence failed", "msg", msg.ID, "error", err)
		}
	}
}

// channelNameByID returns the name of the channel with the given id.
func channelNameByID(ctx context.Context, db *sql.DB, id int64) (string, error) {
	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM channels WHERE id = ?`, id).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

// resolveOwnerString returns the agents.owner_id of fromAgent as a
// string, or "" on any error.
func resolveOwnerString(ctx context.Context, db *sql.DB, fromAgent string) string {
	var ownerID int64
	err := db.QueryRowContext(ctx,
		`SELECT owner_id FROM agents WHERE name = ?`, fromAgent,
	).Scan(&ownerID)
	if err != nil || ownerID == 0 {
		return ""
	}
	// Use the same formatting as agents.OwnerFor so cross-package
	// comparisons stay byte-for-byte consistent.
	return itoaInt64(ownerID)
}

// itoaInt64 mirrors strconv.FormatInt(v,10) without importing strconv.
func itoaInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// extractReplyToFromMetadata returns the reply_to_message_id field if
// the metadata is a JSON object with that key.
func extractReplyToFromMetadata(meta json.RawMessage) int64 {
	if len(meta) == 0 {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return 0
	}
	if v, ok := m["reply_to_message_id"]; ok {
		switch t := v.(type) {
		case float64:
			return int64(t)
		case int64:
			return t
		case int:
			return int64(t)
		}
	}
	return 0
}

func mostRecentMessageFromAgentInChannel(ctx context.Context, db *sql.DB, channelID int64, agent string, excludeID int64) int64 {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM messages
		  WHERE channel_id = ? AND from_agent = ? AND id != ?
		  ORDER BY id DESC LIMIT 1`,
		channelID, agent, excludeID,
	).Scan(&id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			// best-effort: caller logs at debug
		}
		return 0
	}
	return id
}

func mostRecentMessageInChannel(ctx context.Context, db *sql.DB, channelID int64, excludeID int64) int64 {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM messages
		  WHERE channel_id = ? AND id < ?
		  ORDER BY id DESC LIMIT 1`,
		channelID, excludeID,
	).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}
