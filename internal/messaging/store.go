package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MessageStore defines the storage interface for messaging operations.
type MessageStore interface {
	InsertMessage(ctx context.Context, msg *Message) error
	InsertConversation(ctx context.Context, conv *Conversation) error
	FindConversation(ctx context.Context, subject, fromAgent, toAgent string) (*Conversation, error)
	GetInboxMessages(ctx context.Context, agentName string, opts ReadOptions) ([]*Message, error)
	CountInboxMessages(ctx context.Context, agentName string, opts ReadOptions) (int, error)
	GetInboxState(ctx context.Context, agentName string, conversationID int64) (*InboxState, error)
	UpdateInboxState(ctx context.Context, agentName string, conversationID int64, lastReadMsgID int64) error
	ClaimMessages(ctx context.Context, agentName string, limit int) ([]*Message, error)
	UpdateMessageStatus(ctx context.Context, id int64, status, claimedBy string, metadata json.RawMessage) error
	GetMessageByID(ctx context.Context, id int64) (*Message, error)
	SearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) ([]*Message, error)
	CountSearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) (int, error)
	GetConversation(ctx context.Context, id int64) (*Conversation, error)
	GetConversationMessages(ctx context.Context, conversationID int64) ([]*Message, error)
	GetReplies(ctx context.Context, messageID int64) ([]*Message, error)
	GetChannelMessages(ctx context.Context, channelID int64, limit, offset int) ([]*Message, error)
	CountChannelMessages(ctx context.Context, channelID int64) (int, error)
	GetDMMessages(ctx context.Context, agents []string, peerAgent string, limit int) ([]*Message, error)
	AgentExists(ctx context.Context, agentName string) (bool, error)
	CountPendingDMs(ctx context.Context, agentName string) (int64, error)
	GetPendingDMs(ctx context.Context, agentName string, limit int) ([]*Message, error)
	GetRecentMentions(ctx context.Context, agentName string, limit int) ([]*Message, error)
	GetSystemNotifications(ctx context.Context, agentName string, limit int) ([]*Message, error)
	GetDMUnreadCounts(ctx context.Context, agentName string) ([]DMUnreadCount, error)
	GetLastReadForChannel(ctx context.Context, agentName string, channelID int64) (int64, error)
	GetLastReadForDM(ctx context.Context, agentNames []string, peerAgent string) (int64, error)
	GetConversationIDsForChannel(ctx context.Context, channelID int64, lastMessageID int64) ([]int64, error)
	GetConversationIDsForDM(ctx context.Context, agentNames []string, peerAgent string, lastMessageID int64) ([]int64, error)
}

// SQLiteMessageStore implements MessageStore using SQLite.
type SQLiteMessageStore struct {
	db *sql.DB
}

// NewSQLiteMessageStore creates a new SQLite-backed message store.
func NewSQLiteMessageStore(db *sql.DB) *SQLiteMessageStore {
	return &SQLiteMessageStore{db: db}
}

func (s *SQLiteMessageStore) InsertConversation(ctx context.Context, conv *Conversation) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (subject, created_by, channel_id, created_at, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		conv.Subject, conv.CreatedBy, conv.ChannelID,
	)
	if err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get conversation id: %w", err)
	}
	conv.ID = id
	return nil
}

func (s *SQLiteMessageStore) FindConversation(ctx context.Context, subject, fromAgent, toAgent string) (*Conversation, error) {
	var conv Conversation
	var channelID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.subject, c.created_by, c.channel_id, c.created_at, c.updated_at
		 FROM conversations c
		 WHERE c.subject = ? AND c.channel_id IS NULL
		 AND EXISTS (
			SELECT 1 FROM messages m WHERE m.conversation_id = c.id
			AND ((m.from_agent = ? AND m.to_agent = ?) OR (m.from_agent = ? AND m.to_agent = ?))
		 )
		 ORDER BY c.id DESC LIMIT 1`,
		subject, fromAgent, toAgent, toAgent, fromAgent,
	).Scan(&conv.ID, &conv.Subject, &conv.CreatedBy, &channelID, &conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if channelID.Valid {
		conv.ChannelID = &channelID.Int64
	}
	return &conv, nil
}

func (s *SQLiteMessageStore) InsertMessage(ctx context.Context, msg *Message) error {
	metadata := msg.Metadata
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}

	var toAgent sql.NullString
	if msg.ToAgent != "" {
		toAgent = sql.NullString{String: msg.ToAgent, Valid: true}
	}

	var replyTo sql.NullInt64
	if msg.ReplyTo != nil {
		replyTo = sql.NullInt64{Int64: *msg.ReplyTo, Valid: true}
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, reply_to, body, priority, status, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		msg.ConversationID, msg.FromAgent, toAgent, msg.ChannelID, replyTo, msg.Body, msg.Priority, msg.Status, string(metadata),
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get message id: %w", err)
	}
	msg.ID = id
	return nil
}

func (s *SQLiteMessageStore) GetInboxMessages(ctx context.Context, agentName string, opts ReadOptions) ([]*Message, error) {
	conditions, args := s.buildInboxConditions(agentName, opts)

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(
		`SELECT m.id, m.conversation_id, m.from_agent, m.to_agent, m.channel_id,
		        m.body, m.priority, m.status, m.metadata, m.claimed_by, m.claimed_at,
		        m.created_at, m.updated_at, m.reply_to
		 FROM messages m
		 WHERE %s
		 ORDER BY m.priority DESC, m.created_at ASC
		 LIMIT ? OFFSET ?`,
		strings.Join(conditions, " AND "),
	)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// CountInboxMessages returns the total count of inbox messages matching the given options.
func (s *SQLiteMessageStore) CountInboxMessages(ctx context.Context, agentName string, opts ReadOptions) (int, error) {
	conditions, args := s.buildInboxConditions(agentName, opts)

	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM messages m WHERE %s`,
		strings.Join(conditions, " AND "),
	)

	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count inbox messages: %w", err)
	}
	return count, nil
}

// buildInboxConditions builds the WHERE conditions and args for inbox queries.
func (s *SQLiteMessageStore) buildInboxConditions(agentName string, opts ReadOptions) ([]string, []any) {
	var conditions []string
	var args []any

	// Direct messages to this agent
	conditions = append(conditions, "m.to_agent = ?")
	args = append(args, agentName)

	// Filter by read/unread using inbox_state
	if !opts.IncludeRead {
		conditions = append(conditions,
			`m.id > COALESCE(
				(SELECT last_read_message_id FROM inbox_state
				 WHERE agent_name = ? AND conversation_id = m.conversation_id), 0)`)
		args = append(args, agentName)
	}

	if opts.Status != "" {
		conditions = append(conditions, "m.status = ?")
		args = append(args, opts.Status)
	}

	if opts.FromAgent != "" {
		conditions = append(conditions, "m.from_agent = ?")
		args = append(args, opts.FromAgent)
	}

	if opts.ConversationID != nil {
		conditions = append(conditions, "m.conversation_id = ?")
		args = append(args, *opts.ConversationID)
	}

	if opts.MinPriority > 0 {
		conditions = append(conditions, "m.priority >= ?")
		args = append(args, opts.MinPriority)
	}

	if opts.After != "" {
		if t, err := time.Parse(time.RFC3339, opts.After); err == nil {
			conditions = append(conditions, "m.created_at >= ?")
			args = append(args, t.UTC().Format("2006-01-02 15:04:05"))
		}
	}

	if opts.Before != "" {
		if t, err := time.Parse(time.RFC3339, opts.Before); err == nil {
			conditions = append(conditions, "m.created_at <= ?")
			args = append(args, t.UTC().Format("2006-01-02 15:04:05"))
		}
	}

	return conditions, args
}

func (s *SQLiteMessageStore) GetInboxState(ctx context.Context, agentName string, conversationID int64) (*InboxState, error) {
	var state InboxState
	err := s.db.QueryRowContext(ctx,
		`SELECT agent_name, conversation_id, last_read_message_id, updated_at
		 FROM inbox_state WHERE agent_name = ? AND conversation_id = ?`,
		agentName, conversationID,
	).Scan(&state.AgentName, &state.ConversationID, &state.LastReadMessageID, &state.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteMessageStore) UpdateInboxState(ctx context.Context, agentName string, conversationID int64, lastReadMsgID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO inbox_state (agent_name, conversation_id, last_read_message_id, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(agent_name, conversation_id) DO UPDATE SET
			last_read_message_id = MAX(last_read_message_id, excluded.last_read_message_id),
			updated_at = CURRENT_TIMESTAMP`,
		agentName, conversationID, lastReadMsgID,
	)
	return err
}

func (s *SQLiteMessageStore) ClaimMessages(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 10
	}

	// First, find the IDs of pending messages to claim
	idRows, err := s.db.QueryContext(ctx,
		`SELECT id FROM messages
		 WHERE to_agent = ? AND status = 'pending'
		 ORDER BY priority DESC, created_at ASC
		 LIMIT ?`,
		agentName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("find pending messages: %w", err)
	}

	var ids []int64
	for idRows.Next() {
		var id int64
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return nil, fmt.Errorf("scan message id: %w", err)
		}
		ids = append(ids, id)
	}
	idRows.Close()

	if len(ids) == 0 {
		return []*Message{}, nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, agentName)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	// Atomically claim these specific messages
	_, err = s.db.ExecContext(ctx,
		fmt.Sprintf(
			`UPDATE messages SET
				status = 'processing',
				claimed_by = ?,
				claimed_at = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP
			 WHERE id IN (%s) AND status = 'pending'`,
			strings.Join(placeholders, ","),
		),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("claim messages: %w", err)
	}

	// Return the claimed messages by their specific IDs
	fetchArgs := make([]any, len(ids))
	for i, id := range ids {
		fetchArgs[i] = id
	}

	fetchPlaceholders := make([]string, len(ids))
	for i := range ids {
		fetchPlaceholders[i] = "?"
	}

	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(
			`SELECT id, conversation_id, from_agent, to_agent, channel_id,
			        body, priority, status, metadata, claimed_by, claimed_at,
			        created_at, updated_at, reply_to
			 FROM messages
			 WHERE id IN (%s)
			 ORDER BY priority DESC, created_at ASC`,
			strings.Join(fetchPlaceholders, ","),
		),
		fetchArgs...,
	)
	if err != nil {
		return nil, fmt.Errorf("query claimed messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *SQLiteMessageStore) UpdateMessageStatus(ctx context.Context, id int64, status, claimedBy string, metadata json.RawMessage) error {
	var result sql.Result
	var err error

	if metadata != nil {
		result, err = s.db.ExecContext(ctx,
			`UPDATE messages SET status = ?, metadata = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND claimed_by = ?`,
			status, string(metadata), id, claimedBy,
		)
	} else {
		result, err = s.db.ExecContext(ctx,
			`UPDATE messages SET status = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND claimed_by = ?`,
			status, id, claimedBy,
		)
	}
	if err != nil {
		return fmt.Errorf("update message status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("message not found or not claimed by agent")
	}
	return nil
}

func (s *SQLiteMessageStore) GetMessageByID(ctx context.Context, id int64) (*Message, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages WHERE id = ?`, id,
	)
	return scanMessage(row)
}

func (s *SQLiteMessageStore) SearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) ([]*Message, error) {
	conditions, args, joinClause, orderClause := s.buildSearchConditions(agentName, query, opts)

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	querySQL := fmt.Sprintf(
		`SELECT m.id, m.conversation_id, m.from_agent, m.to_agent, m.channel_id,
		        m.body, m.priority, m.status, m.metadata, m.claimed_by, m.claimed_at,
		        m.created_at, m.updated_at, m.reply_to
		 FROM messages m
		 %s
		 WHERE %s
		 %s
		 LIMIT ? OFFSET ?`,
		joinClause,
		strings.Join(conditions, " AND "),
		orderClause,
	)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// CountSearchMessages returns the total count of messages matching the search criteria.
func (s *SQLiteMessageStore) CountSearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) (int, error) {
	conditions, args, joinClause, _ := s.buildSearchConditions(agentName, query, opts)

	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) FROM messages m %s WHERE %s`,
		joinClause,
		strings.Join(conditions, " AND "),
	)

	var count int
	err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count search messages: %w", err)
	}
	return count, nil
}

// buildSearchConditions builds the WHERE conditions, args, JOIN clause, and ORDER clause for search queries.
func (s *SQLiteMessageStore) buildSearchConditions(agentName, query string, opts SearchOptions) ([]string, []any, string, string) {
	var conditions []string
	var args []any

	// Scope to messages accessible by this agent:
	// - DMs where agent is sender or recipient
	// - Channel messages where agent is a member
	conditions = append(conditions, "(m.to_agent = ? OR m.from_agent = ? OR (m.channel_id IS NOT NULL AND m.to_agent = '' AND EXISTS (SELECT 1 FROM channel_members cm WHERE cm.channel_id = m.channel_id AND cm.agent_name = ?)))")
	args = append(args, agentName, agentName, agentName)

	var joinClause string
	var orderClause string

	if query != "" {
		joinClause = "JOIN messages_fts ON messages_fts.rowid = m.id"
		conditions = append(conditions, "messages_fts MATCH ?")
		args = append(args, query)
		orderClause = "ORDER BY rank"
	} else {
		orderClause = "ORDER BY m.created_at DESC"
	}

	if opts.FromAgent != "" {
		conditions = append(conditions, "m.from_agent = ?")
		args = append(args, opts.FromAgent)
	}

	if opts.ToAgent != "" {
		conditions = append(conditions, "m.to_agent = ?")
		args = append(args, opts.ToAgent)
	}

	if opts.MinPriority > 0 {
		conditions = append(conditions, "m.priority >= ?")
		args = append(args, opts.MinPriority)
	}

	if opts.Status != "" {
		conditions = append(conditions, "m.status = ?")
		args = append(args, opts.Status)
	}

	if opts.ChannelID != nil {
		conditions = append(conditions, "m.channel_id = ?")
		args = append(args, *opts.ChannelID)
	}

	if opts.Channel != "" {
		conditions = append(conditions, "m.channel_id IN (SELECT id FROM channels WHERE LOWER(name) = LOWER(?))")
		args = append(args, opts.Channel)
	}

	if opts.After != "" {
		if t, err := time.Parse(time.RFC3339, opts.After); err == nil {
			conditions = append(conditions, "m.created_at >= ?")
			args = append(args, t.UTC().Format("2006-01-02 15:04:05"))
		}
	}

	if opts.Before != "" {
		if t, err := time.Parse(time.RFC3339, opts.Before); err == nil {
			conditions = append(conditions, "m.created_at <= ?")
			args = append(args, t.UTC().Format("2006-01-02 15:04:05"))
		}
	}

	return conditions, args, joinClause, orderClause
}

func (s *SQLiteMessageStore) GetConversation(ctx context.Context, id int64) (*Conversation, error) {
	var conv Conversation
	var channelID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, subject, created_by, channel_id, created_at, updated_at
		 FROM conversations WHERE id = ?`, id,
	).Scan(&conv.ID, &conv.Subject, &conv.CreatedBy, &channelID, &conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if channelID.Valid {
		conv.ChannelID = &channelID.Int64
	}
	return &conv, nil
}

func (s *SQLiteMessageStore) GetConversationMessages(ctx context.Context, conversationID int64) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages WHERE conversation_id = ?
		 ORDER BY created_at ASC`, conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("get conversation messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *SQLiteMessageStore) GetReplies(ctx context.Context, messageID int64) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages WHERE reply_to = ?
		 ORDER BY created_at ASC`, messageID,
	)
	if err != nil {
		return nil, fmt.Errorf("get replies: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *SQLiteMessageStore) GetChannelMessages(ctx context.Context, channelID int64, limit, offset int) ([]*Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages WHERE channel_id = ?
		 ORDER BY created_at ASC
		 LIMIT ? OFFSET ?`, channelID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("get channel messages: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

// CountChannelMessages returns the total number of messages in a channel.
func (s *SQLiteMessageStore) CountChannelMessages(ctx context.Context, channelID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE channel_id = ?`, channelID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count channel messages: %w", err)
	}
	return count, nil
}

func (s *SQLiteMessageStore) GetDMMessages(ctx context.Context, agents []string, peerAgent string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if len(agents) == 0 {
		return []*Message{}, nil
	}

	// Build placeholders for owned agents
	placeholders := make([]string, len(agents))
	args := make([]any, 0, len(agents)*2+2)
	for i, a := range agents {
		placeholders[i] = "?"
		args = append(args, a)
	}
	inClause := strings.Join(placeholders, ",")

	// Messages where (from_agent IN owned AND to_agent = peer) OR (from_agent = peer AND to_agent IN owned)
	// and channel_id IS NULL (DMs only)
	query := fmt.Sprintf(
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages
		 WHERE channel_id IS NULL
		   AND ((from_agent IN (%s) AND to_agent = ?) OR (from_agent = ? AND to_agent IN (%s)))
		 ORDER BY created_at ASC
		 LIMIT ?`,
		inClause, inClause,
	)
	args = append(args, peerAgent, peerAgent)
	for _, a := range agents {
		args = append(args, a)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get dm messages: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteMessageStore) CountPendingDMs(ctx context.Context, agentName string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE to_agent = ? AND status = 'pending' AND channel_id IS NULL`,
		agentName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending DMs: %w", err)
	}
	return count, nil
}

func (s *SQLiteMessageStore) GetPendingDMs(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages
		 WHERE to_agent = ? AND status IN ('pending','processing') AND channel_id IS NULL AND from_agent != 'system'
		 ORDER BY created_at DESC
		 LIMIT ?`,
		agentName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending DMs: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteMessageStore) GetRecentMentions(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.conversation_id, m.from_agent, m.to_agent, m.channel_id,
		        m.body, m.priority, m.status, m.metadata, m.claimed_by, m.claimed_at,
		        m.created_at, m.updated_at, m.reply_to
		 FROM messages m
		 JOIN channel_members cm ON cm.channel_id = m.channel_id AND cm.agent_name = ?
		 WHERE m.channel_id IS NOT NULL
		   AND m.body LIKE '%@' || ? || '%'
		   AND m.from_agent != ?
		 ORDER BY m.created_at DESC
		 LIMIT ?`,
		agentName, agentName, agentName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get recent mentions: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteMessageStore) GetSystemNotifications(ctx context.Context, agentName string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, from_agent, to_agent, channel_id,
		        body, priority, status, metadata, claimed_by, claimed_at,
		        created_at, updated_at, reply_to
		 FROM messages
		 WHERE to_agent = ? AND from_agent = 'system' AND status = 'pending'
		 ORDER BY created_at DESC
		 LIMIT ?`,
		agentName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get system notifications: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteMessageStore) AgentExists(ctx context.Context, agentName string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents WHERE name = ? AND status = 'active'`,
		agentName,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// scanMessages scans multiple message rows.
func scanMessages(rows *sql.Rows) ([]*Message, error) {
	var messages []*Message
	for rows.Next() {
		msg, err := scanMessageFromRows(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if messages == nil {
		messages = []*Message{}
	}
	return messages, rows.Err()
}

// scanMessageFromRows scans a single message from sql.Rows.
func scanMessageFromRows(rows *sql.Rows) (*Message, error) {
	var msg Message
	var toAgent, claimedBy sql.NullString
	var channelID, replyTo sql.NullInt64
	var claimedAt sql.NullTime
	var metadata string

	err := rows.Scan(
		&msg.ID, &msg.ConversationID, &msg.FromAgent, &toAgent, &channelID,
		&msg.Body, &msg.Priority, &msg.Status, &metadata, &claimedBy, &claimedAt,
		&msg.CreatedAt, &msg.UpdatedAt, &replyTo,
	)
	if err != nil {
		return nil, fmt.Errorf("scan message: %w", err)
	}

	if toAgent.Valid {
		msg.ToAgent = toAgent.String
	}
	if channelID.Valid {
		msg.ChannelID = &channelID.Int64
	}
	if replyTo.Valid {
		msg.ReplyTo = &replyTo.Int64
	}
	if claimedBy.Valid {
		msg.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		msg.ClaimedAt = &claimedAt.Time
	}
	msg.Metadata = json.RawMessage(metadata)

	return &msg, nil
}

// GetDMUnreadCounts returns unread DM counts grouped by peer agent.
// For each unique from_agent that has sent DMs to agentName, it computes
// how many messages have id > last_read_message_id (from inbox_state).
func (s *SQLiteMessageStore) GetDMUnreadCounts(ctx context.Context, agentName string) ([]DMUnreadCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT
			m.from_agent,
			COUNT(CASE WHEN m.id > COALESCE(
				(SELECT ist.last_read_message_id FROM inbox_state ist
				 WHERE ist.agent_name = ? AND ist.conversation_id = m.conversation_id), 0)
			THEN 1 END) AS unread_count,
			MAX(m.id) AS last_message_id
		 FROM messages m
		 WHERE m.to_agent = ?
		   AND m.channel_id IS NULL
		   AND m.from_agent != 'system'
		 GROUP BY m.from_agent
		 ORDER BY m.from_agent`,
		agentName, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get dm unread counts: %w", err)
	}
	defer rows.Close()

	var results []DMUnreadCount
	for rows.Next() {
		var d DMUnreadCount
		if err := rows.Scan(&d.Agent, &d.UnreadCount, &d.LastMessageID); err != nil {
			return nil, fmt.Errorf("scan dm unread count: %w", err)
		}
		results = append(results, d)
	}
	if results == nil {
		results = []DMUnreadCount{}
	}
	return results, rows.Err()
}

// GetLastReadForChannel returns the effective last_read_message_id for an agent
// across all conversations in a given channel.
func (s *SQLiteMessageStore) GetLastReadForChannel(ctx context.Context, agentName string, channelID int64) (int64, error) {
	var lastRead sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(COALESCE(ist.last_read_message_id, 0))
		 FROM (SELECT DISTINCT conversation_id FROM messages WHERE channel_id = ?) conv
		 LEFT JOIN inbox_state ist ON ist.conversation_id = conv.conversation_id AND ist.agent_name = ?`,
		channelID, agentName,
	).Scan(&lastRead)
	if err != nil {
		return 0, fmt.Errorf("get last read for channel: %w", err)
	}
	if lastRead.Valid {
		return lastRead.Int64, nil
	}
	return 0, nil
}

// GetLastReadForDM returns the effective last_read_message_id for an owned agent
// in DM conversations with a specific peer agent.
func (s *SQLiteMessageStore) GetLastReadForDM(ctx context.Context, agentNames []string, peerAgent string) (int64, error) {
	if len(agentNames) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(agentNames))
	args := make([]any, 0, len(agentNames)*2+1)
	for i, a := range agentNames {
		placeholders[i] = "?"
		args = append(args, a)
	}
	inClause := strings.Join(placeholders, ",")

	// Find conversations between owned agents and peer agent (DMs only)
	// and get the max last_read_message_id
	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(ist.last_read_message_id), 0)
		 FROM inbox_state ist
		 WHERE ist.agent_name IN (%s)
		   AND ist.conversation_id IN (
			 SELECT DISTINCT m.conversation_id FROM messages m
			 WHERE m.channel_id IS NULL
			   AND ((m.from_agent IN (%s) AND m.to_agent = ?)
			     OR (m.from_agent = ? AND m.to_agent IN (%s)))
		   )`,
		inClause, inClause, inClause,
	)
	for _, a := range agentNames {
		args = append(args, a)
	}
	args = append(args, peerAgent, peerAgent)
	for _, a := range agentNames {
		args = append(args, a)
	}

	var lastRead int64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&lastRead)
	if err != nil {
		return 0, fmt.Errorf("get last read for dm: %w", err)
	}
	return lastRead, nil
}

// GetConversationIDsForChannel returns conversation IDs in a channel with messages up to lastMessageID.
func (s *SQLiteMessageStore) GetConversationIDsForChannel(ctx context.Context, channelID int64, lastMessageID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT conversation_id FROM messages
		 WHERE channel_id = ? AND id <= ?`,
		channelID, lastMessageID,
	)
	if err != nil {
		return nil, fmt.Errorf("get conversation ids for channel: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan conversation id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetConversationIDsForDM returns conversation IDs for DMs between owned agents and a peer agent,
// with messages up to lastMessageID.
func (s *SQLiteMessageStore) GetConversationIDsForDM(ctx context.Context, agentNames []string, peerAgent string, lastMessageID int64) ([]int64, error) {
	if len(agentNames) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(agentNames))
	args := make([]any, 0, len(agentNames)*2+2)
	for i, a := range agentNames {
		placeholders[i] = "?"
		args = append(args, a)
	}
	inClause := strings.Join(placeholders, ",")

	query := fmt.Sprintf(
		`SELECT DISTINCT conversation_id FROM messages
		 WHERE channel_id IS NULL
		   AND id <= ?
		   AND ((from_agent IN (%s) AND to_agent = ?)
		     OR (from_agent = ? AND to_agent IN (%s)))`,
		inClause, inClause,
	)

	// Reorder args: agentNames for first IN, lastMessageID, agentNames for second IN...
	finalArgs := make([]any, 0, len(agentNames)*2+3)
	finalArgs = append(finalArgs, lastMessageID)
	for _, a := range agentNames {
		finalArgs = append(finalArgs, a)
	}
	finalArgs = append(finalArgs, peerAgent, peerAgent)
	for _, a := range agentNames {
		finalArgs = append(finalArgs, a)
	}

	rows, err := s.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, fmt.Errorf("get conversation ids for dm: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan conversation id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// scanMessage scans a single message from sql.Row.
func scanMessage(row *sql.Row) (*Message, error) {
	var msg Message
	var toAgent, claimedBy sql.NullString
	var channelID, replyTo sql.NullInt64
	var claimedAt sql.NullTime
	var metadata string

	err := row.Scan(
		&msg.ID, &msg.ConversationID, &msg.FromAgent, &toAgent, &channelID,
		&msg.Body, &msg.Priority, &msg.Status, &metadata, &claimedBy, &claimedAt,
		&msg.CreatedAt, &msg.UpdatedAt, &replyTo,
	)
	if err != nil {
		return nil, err
	}

	if toAgent.Valid {
		msg.ToAgent = toAgent.String
	}
	if channelID.Valid {
		msg.ChannelID = &channelID.Int64
	}
	if replyTo.Valid {
		msg.ReplyTo = &replyTo.Int64
	}
	if claimedBy.Valid {
		msg.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		t := claimedAt.Time
		msg.ClaimedAt = &t
	}
	msg.Metadata = json.RawMessage(metadata)

	return &msg, nil
}
