package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// MessageStore defines the storage interface for messaging operations.
type MessageStore interface {
	InsertMessage(ctx context.Context, msg *Message) error
	InsertConversation(ctx context.Context, conv *Conversation) error
	FindConversation(ctx context.Context, subject, fromAgent, toAgent string) (*Conversation, error)
	GetInboxMessages(ctx context.Context, agentName string, opts ReadOptions) ([]*Message, error)
	GetInboxState(ctx context.Context, agentName string, conversationID int64) (*InboxState, error)
	UpdateInboxState(ctx context.Context, agentName string, conversationID int64, lastReadMsgID int64) error
	ClaimMessages(ctx context.Context, agentName string, limit int) ([]*Message, error)
	UpdateMessageStatus(ctx context.Context, id int64, status, claimedBy string, metadata json.RawMessage) error
	GetMessageByID(ctx context.Context, id int64) (*Message, error)
	SearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) ([]*Message, error)
	GetConversation(ctx context.Context, id int64) (*Conversation, error)
	GetConversationMessages(ctx context.Context, conversationID int64) ([]*Message, error)
	AgentExists(ctx context.Context, agentName string) (bool, error)
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

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, from_agent, to_agent, channel_id, body, priority, status, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		msg.ConversationID, msg.FromAgent, toAgent, msg.ChannelID, msg.Body, msg.Priority, msg.Status, string(metadata),
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

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	query := fmt.Sprintf(
		`SELECT m.id, m.conversation_id, m.from_agent, m.to_agent, m.channel_id,
		        m.body, m.priority, m.status, m.metadata, m.claimed_by, m.claimed_at,
		        m.created_at, m.updated_at
		 FROM messages m
		 WHERE %s
		 ORDER BY m.priority DESC, m.created_at ASC
		 LIMIT ?`,
		strings.Join(conditions, " AND "),
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
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
			        created_at, updated_at
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
		        created_at, updated_at
		 FROM messages WHERE id = ?`, id,
	)
	return scanMessage(row)
}

func (s *SQLiteMessageStore) SearchMessages(ctx context.Context, agentName, query string, opts SearchOptions) ([]*Message, error) {
	var conditions []string
	var args []any

	// Scope to messages accessible by this agent
	conditions = append(conditions, "(m.to_agent = ? OR m.from_agent = ?)")
	args = append(args, agentName, agentName)

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

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	querySQL := fmt.Sprintf(
		`SELECT m.id, m.conversation_id, m.from_agent, m.to_agent, m.channel_id,
		        m.body, m.priority, m.status, m.metadata, m.claimed_by, m.claimed_at,
		        m.created_at, m.updated_at
		 FROM messages m
		 %s
		 WHERE %s
		 %s
		 LIMIT ?`,
		joinClause,
		strings.Join(conditions, " AND "),
		orderClause,
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
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
		        created_at, updated_at
		 FROM messages WHERE conversation_id = ?
		 ORDER BY created_at ASC`, conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("get conversation messages: %w", err)
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
	var channelID sql.NullInt64
	var claimedAt sql.NullTime
	var metadata string

	err := rows.Scan(
		&msg.ID, &msg.ConversationID, &msg.FromAgent, &toAgent, &channelID,
		&msg.Body, &msg.Priority, &msg.Status, &metadata, &claimedBy, &claimedAt,
		&msg.CreatedAt, &msg.UpdatedAt,
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
	if claimedBy.Valid {
		msg.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		msg.ClaimedAt = &claimedAt.Time
	}
	msg.Metadata = json.RawMessage(metadata)

	return &msg, nil
}

// scanMessage scans a single message from sql.Row.
func scanMessage(row *sql.Row) (*Message, error) {
	var msg Message
	var toAgent, claimedBy sql.NullString
	var channelID sql.NullInt64
	var claimedAt sql.NullTime
	var metadata string

	err := row.Scan(
		&msg.ID, &msg.ConversationID, &msg.FromAgent, &toAgent, &channelID,
		&msg.Body, &msg.Priority, &msg.Status, &metadata, &claimedBy, &claimedAt,
		&msg.CreatedAt, &msg.UpdatedAt,
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
