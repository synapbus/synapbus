package channels

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ChannelStore defines the storage interface for channel operations.
type ChannelStore interface {
	CreateChannel(ctx context.Context, ch *Channel) error
	GetChannel(ctx context.Context, id int64) (*Channel, error)
	GetChannelByName(ctx context.Context, name string) (*Channel, error)
	ListChannels(ctx context.Context, agentName string) ([]*Channel, error)
	CountChannels(ctx context.Context, agentName string) (int, error)
	UpdateChannel(ctx context.Context, ch *Channel) error
	DeleteChannel(ctx context.Context, id int64) error
	AddMember(ctx context.Context, m *Membership) error
	RemoveMember(ctx context.Context, channelID int64, agentName string) error
	GetMember(ctx context.Context, channelID int64, agentName string) (*Membership, error)
	GetMembers(ctx context.Context, channelID int64) ([]*Membership, error)
	IsMember(ctx context.Context, channelID int64, agentName string) (bool, error)
	CountMembers(ctx context.Context, channelID int64) (int, error)
	CreateInvite(ctx context.Context, inv *ChannelInvite) error
	GetInvite(ctx context.Context, channelID int64, agentName string) (*ChannelInvite, error)
	HasPendingInvite(ctx context.Context, channelID int64, agentName string) (bool, error)
	AcceptInvite(ctx context.Context, channelID int64, agentName string) error
	GetChannelSummaries(ctx context.Context, agentName string) ([]ChannelSummary, error)
}

// SQLiteChannelStore implements ChannelStore using SQLite.
type SQLiteChannelStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewSQLiteChannelStore creates a new SQLite-backed channel store.
func NewSQLiteChannelStore(db *sql.DB) *SQLiteChannelStore {
	return &SQLiteChannelStore{
		db:     db,
		logger: slog.Default().With("component", "channel-store"),
	}
}

// CreateChannel inserts a new channel.
func (s *SQLiteChannelStore) CreateChannel(ctx context.Context, ch *Channel) error {
	isPrivate := 0
	if ch.IsPrivate {
		isPrivate = 1
	}
	isSystem := 0
	if ch.IsSystem {
		isSystem = 1
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (name, description, topic, type, is_private, is_system, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		ch.Name, ch.Description, ch.Topic, ch.Type, isPrivate, isSystem, ch.CreatedBy,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrChannelNameConflict
		}
		return fmt.Errorf("insert channel: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get channel id: %w", err)
	}
	ch.ID = id

	s.logger.Info("channel created", "id", id, "name", ch.Name)
	return nil
}

// GetChannel returns a channel by ID.
func (s *SQLiteChannelStore) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	var ch Channel
	var isPrivate, isSystem int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at
		 FROM channels WHERE id = ?`, id,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Topic, &ch.Type, &isPrivate, &isSystem, &ch.CreatedBy, &ch.CreatedAt, &ch.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrChannelNotFound
		}
		return nil, fmt.Errorf("get channel: %w", err)
	}
	ch.IsPrivate = isPrivate != 0
	ch.IsSystem = isSystem != 0
	return &ch, nil
}

// GetChannelByName returns a channel by name (case-insensitive).
func (s *SQLiteChannelStore) GetChannelByName(ctx context.Context, name string) (*Channel, error) {
	var ch Channel
	var isPrivate, isSystem int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, topic, type, is_private, is_system, created_by, created_at, updated_at
		 FROM channels WHERE LOWER(name) = LOWER(?)`, name,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Topic, &ch.Type, &isPrivate, &isSystem, &ch.CreatedBy, &ch.CreatedAt, &ch.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrChannelNotFound
		}
		return nil, fmt.Errorf("get channel by name: %w", err)
	}
	ch.IsPrivate = isPrivate != 0
	ch.IsSystem = isSystem != 0
	return &ch, nil
}

// ListChannels returns all public channels plus private channels where the agent
// is a member or has a pending invite.
func (s *SQLiteChannelStore) ListChannels(ctx context.Context, agentName string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT c.id, c.name, c.description, c.topic, c.type, c.is_private, c.is_system, c.created_by, c.created_at, c.updated_at
		 FROM channels c
		 WHERE c.is_private = 0
		    OR EXISTS (SELECT 1 FROM channel_members cm WHERE cm.channel_id = c.id AND cm.agent_name = ?)
		    OR EXISTS (SELECT 1 FROM channel_invites ci WHERE ci.channel_id = c.id AND ci.agent_name = ? AND ci.status = 'pending')
		 ORDER BY c.name`,
		agentName, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	var channels []*Channel
	for rows.Next() {
		var ch Channel
		var isPrivate, isSystem int
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Topic, &ch.Type, &isPrivate, &isSystem, &ch.CreatedBy, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		ch.IsPrivate = isPrivate != 0
		ch.IsSystem = isSystem != 0
		channels = append(channels, &ch)
	}
	if channels == nil {
		channels = []*Channel{}
	}
	return channels, rows.Err()
}

// CountChannels returns the total number of channels visible to the agent.
func (s *SQLiteChannelStore) CountChannels(ctx context.Context, agentName string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT c.id)
		 FROM channels c
		 WHERE c.is_private = 0
		    OR EXISTS (SELECT 1 FROM channel_members cm WHERE cm.channel_id = c.id AND cm.agent_name = ?)
		    OR EXISTS (SELECT 1 FROM channel_invites ci WHERE ci.channel_id = c.id AND ci.agent_name = ? AND ci.status = 'pending')`,
		agentName, agentName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count channels: %w", err)
	}
	return count, nil
}

// UpdateChannel updates a channel's mutable fields.
func (s *SQLiteChannelStore) UpdateChannel(ctx context.Context, ch *Channel) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE channels SET description = ?, topic = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		ch.Description, ch.Topic, ch.ID,
	)
	if err != nil {
		return fmt.Errorf("update channel: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrChannelNotFound
	}
	s.logger.Info("channel updated", "id", ch.ID)
	return nil
}

// DeleteChannel deletes a channel by ID.
func (s *SQLiteChannelStore) DeleteChannel(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrChannelNotFound
	}
	s.logger.Info("channel deleted", "id", id)
	return nil
}

// AddMember adds a member to a channel.
func (s *SQLiteChannelStore) AddMember(ctx context.Context, m *Membership) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO channel_members (channel_id, agent_name, role, joined_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		m.ChannelID, m.AgentName, m.Role,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			// Already a member — idempotent
			return nil
		}
		return fmt.Errorf("add member: %w", err)
	}
	id, _ := result.LastInsertId()
	m.ID = id
	s.logger.Info("member added", "channel_id", m.ChannelID, "agent", m.AgentName, "role", m.Role)
	return nil
}

// RemoveMember removes a member from a channel.
func (s *SQLiteChannelStore) RemoveMember(ctx context.Context, channelID int64, agentName string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_members WHERE channel_id = ? AND agent_name = ?`,
		channelID, agentName,
	)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrNotChannelMember
	}
	s.logger.Info("member removed", "channel_id", channelID, "agent", agentName)
	return nil
}

// GetMember returns a channel membership record.
func (s *SQLiteChannelStore) GetMember(ctx context.Context, channelID int64, agentName string) (*Membership, error) {
	var m Membership
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, agent_name, role, joined_at
		 FROM channel_members WHERE channel_id = ? AND agent_name = ?`,
		channelID, agentName,
	).Scan(&m.ID, &m.ChannelID, &m.AgentName, &m.Role, &m.JoinedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotChannelMember
		}
		return nil, fmt.Errorf("get member: %w", err)
	}
	return &m, nil
}

// GetMembers returns all members of a channel.
func (s *SQLiteChannelStore) GetMembers(ctx context.Context, channelID int64) ([]*Membership, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, agent_name, role, joined_at
		 FROM channel_members WHERE channel_id = ? ORDER BY joined_at`,
		channelID,
	)
	if err != nil {
		return nil, fmt.Errorf("get members: %w", err)
	}
	defer rows.Close()

	var members []*Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AgentName, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, &m)
	}
	if members == nil {
		members = []*Membership{}
	}
	return members, rows.Err()
}

// IsMember checks if an agent is a member of a channel.
func (s *SQLiteChannelStore) IsMember(ctx context.Context, channelID int64, agentName string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channel_members WHERE channel_id = ? AND agent_name = ?`,
		channelID, agentName,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	return count > 0, nil
}

// CountMembers returns the number of members in a channel.
func (s *SQLiteChannelStore) CountMembers(ctx context.Context, channelID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channel_members WHERE channel_id = ?`,
		channelID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count members: %w", err)
	}
	return count, nil
}

// CreateInvite creates an invitation for an agent to join a private channel.
func (s *SQLiteChannelStore) CreateInvite(ctx context.Context, inv *ChannelInvite) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO channel_invites (channel_id, agent_name, invited_by, created_at, status)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP, 'pending')
		 ON CONFLICT(channel_id, agent_name) DO UPDATE SET
			status = 'pending',
			invited_by = excluded.invited_by`,
		inv.ChannelID, inv.AgentName, inv.InvitedBy,
	)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	id, _ := result.LastInsertId()
	inv.ID = id
	s.logger.Info("invite created", "channel_id", inv.ChannelID, "agent", inv.AgentName, "invited_by", inv.InvitedBy)
	return nil
}

// GetInvite returns an invite by channel and agent.
func (s *SQLiteChannelStore) GetInvite(ctx context.Context, channelID int64, agentName string) (*ChannelInvite, error) {
	var inv ChannelInvite
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, agent_name, invited_by, created_at, status
		 FROM channel_invites WHERE channel_id = ? AND agent_name = ?`,
		channelID, agentName,
	).Scan(&inv.ID, &inv.ChannelID, &inv.AgentName, &inv.InvitedBy, &inv.CreatedAt, &inv.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotInvited
		}
		return nil, fmt.Errorf("get invite: %w", err)
	}
	return &inv, nil
}

// HasPendingInvite checks if an agent has a pending invite to a channel.
func (s *SQLiteChannelStore) HasPendingInvite(ctx context.Context, channelID int64, agentName string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channel_invites WHERE channel_id = ? AND agent_name = ? AND status = 'pending'`,
		channelID, agentName,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check invite: %w", err)
	}
	return count > 0, nil
}

// AcceptInvite marks a pending invite as accepted.
func (s *SQLiteChannelStore) AcceptInvite(ctx context.Context, channelID int64, agentName string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE channel_invites SET status = 'accepted' WHERE channel_id = ? AND agent_name = ? AND status = 'pending'`,
		channelID, agentName,
	)
	if err != nil {
		return fmt.Errorf("accept invite: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrNotInvited
	}
	s.logger.Info("invite accepted", "channel_id", channelID, "agent", agentName)
	return nil
}

// GetChannelSummaries returns channels the agent has joined with unread message counts.
func (s *SQLiteChannelStore) GetChannelSummaries(ctx context.Context, agentName string) ([]ChannelSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.name,
		        (SELECT COUNT(*) FROM messages m
		         WHERE m.channel_id = c.id
		           AND m.id > COALESCE(
		               (SELECT MAX(ist.last_read_message_id) FROM inbox_state ist
		                WHERE ist.agent_name = ? AND ist.conversation_id = m.conversation_id), 0)
		           AND m.from_agent != ?
		        ) AS unread_count,
		        COALESCE((SELECT MAX(m3.id) FROM messages m3 WHERE m3.channel_id = c.id), 0) AS last_message_id,
		        (SELECT MAX(m2.created_at) FROM messages m2 WHERE m2.channel_id = c.id) AS last_message_at
		 FROM channels c
		 JOIN channel_members cm ON cm.channel_id = c.id AND cm.agent_name = ?
		 ORDER BY c.name`,
		agentName, agentName, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get channel summaries: %w", err)
	}
	defer rows.Close()

	var summaries []ChannelSummary
	for rows.Next() {
		var cs ChannelSummary
		var lastMsg sql.NullString
		if err := rows.Scan(&cs.ID, &cs.Name, &cs.UnreadCount, &cs.LastMessageID, &lastMsg); err != nil {
			return nil, fmt.Errorf("scan channel summary: %w", err)
		}
		if lastMsg.Valid {
			if t, err := time.Parse("2006-01-02T15:04:05Z", lastMsg.String); err == nil {
				cs.LastMessageAt = &t
			} else if t, err := time.Parse("2006-01-02 15:04:05", lastMsg.String); err == nil {
				cs.LastMessageAt = &t
			}
		}
		summaries = append(summaries, cs)
	}
	if summaries == nil {
		summaries = []ChannelSummary{}
	}
	return summaries, rows.Err()
}

// isUniqueConstraintError checks if an error is a SQLite unique constraint violation.
func isUniqueConstraintError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
