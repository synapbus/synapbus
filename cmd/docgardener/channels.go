package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// dbChannelCreator implements goals.ChannelCreator without taking a
// dependency on the internal/channels service (which would drag in
// half the server). It talks to the channels and channel_members
// tables directly. This is only safe because the demo driver runs
// against the same process's DB and the channels schema is stable.
type dbChannelCreator struct {
	db *sql.DB
}

// CreateGoalChannel satisfies goals.ChannelCreator.
func (c *dbChannelCreator) CreateGoalChannel(ctx context.Context, slug, title, description, ownerUsername string) (int64, error) {
	name := "goal-" + slug
	id, err := c.ensureChannel(ctx, name, description, "blackboard", ownerUsername)
	if err != nil {
		return 0, err
	}
	if ownerUsername != "" {
		if err := c.addMember(ctx, id, ownerUsername); err != nil {
			return 0, fmt.Errorf("add owner %q to goal channel: %w", ownerUsername, err)
		}
	}
	return id, nil
}

// ensureChannel upserts a channel row by name. Returns the id.
func (c *dbChannelCreator) ensureChannel(ctx context.Context, name, description, channelType, createdBy string) (int64, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, `SELECT id FROM channels WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := c.db.ExecContext(ctx, `
		INSERT INTO channels (name, description, type, is_private, is_system, created_by)
		VALUES (?, ?, ?, 0, 0, ?)`, name, description, channelType, createdBy)
	if err != nil {
		return 0, fmt.Errorf("create channel %q: %w", name, err)
	}
	return res.LastInsertId()
}

// getByName resolves a channel id by name.
func (c *dbChannelCreator) getByName(ctx context.Context, name string) (int64, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, `SELECT id FROM channels WHERE name = ?`, name).Scan(&id)
	return id, err
}

// addMember is idempotent — does nothing if the member is already present.
func (c *dbChannelCreator) addMember(ctx context.Context, channelID int64, agentName string) error {
	var exists int
	_ = c.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM channel_members WHERE channel_id=? AND agent_name=?`,
		channelID, agentName).Scan(&exists)
	if exists > 0 {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO channel_members (channel_id, agent_name, role)
		VALUES (?, ?, 'member')`, channelID, agentName)
	return err
}
