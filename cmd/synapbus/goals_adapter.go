package main

import (
	"context"
	"fmt"

	"github.com/synapbus/synapbus/internal/channels"
)

// svcGoalChannelCreator adapts channels.Service to the
// goals.ChannelCreator interface — wrapping it here avoids the
// internal/goals package importing internal/channels (which would
// create a cycle via messaging).
type svcGoalChannelCreator struct {
	channels *channels.Service
}

func (c *svcGoalChannelCreator) CreateGoalChannel(
	ctx context.Context,
	slug, title, description, ownerUsername string,
) (int64, error) {
	name := "goal-" + slug
	ch, err := c.channels.CreateChannel(ctx, channels.CreateChannelRequest{
		Name:        name,
		Description: fmt.Sprintf("Goal: %s", title),
		Topic:       title,
		Type:        channels.TypeBlackboard,
		IsPrivate:   true,
		IsSystem:    true,
		CreatedBy:   ownerUsername,
	})
	if err != nil {
		return 0, err
	}
	return ch.ID, nil
}
