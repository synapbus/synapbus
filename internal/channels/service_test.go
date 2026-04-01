package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := newTestDB(t)

	// Seed test agents
	seedAgent(t, db, "agent-a")
	seedAgent(t, db, "agent-b")
	seedAgent(t, db, "agent-c")

	channelStore := NewSQLiteChannelStore(db)
	msgStore := messaging.NewSQLiteMessageStore(db)
	tracer := trace.NewTracer(db)
	t.Cleanup(func() { tracer.Close() })

	msgService := messaging.NewMessagingService(msgStore, tracer)
	svc := NewService(channelStore, msgService, tracer)
	return svc, db
}

// --- CreateChannel tests ---

func TestService_CreateChannel(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateChannelRequest
		wantErr bool
		errIs   error
	}{
		{
			name: "create public channel",
			req: CreateChannelRequest{
				Name:      "alerts",
				Type:      TypeStandard,
				CreatedBy: "agent-a",
			},
		},
		{
			name: "create private channel",
			req: CreateChannelRequest{
				Name:      "secret",
				Type:      TypeStandard,
				IsPrivate: true,
				CreatedBy: "agent-a",
			},
		},
		{
			name: "default type is standard",
			req: CreateChannelRequest{
				Name:      "default-type",
				CreatedBy: "agent-a",
			},
		},
		{
			name: "with description and topic",
			req: CreateChannelRequest{
				Name:        "research",
				Description: "Research channel",
				Topic:       "Q1 findings",
				Type:        TypeStandard,
				CreatedBy:   "agent-a",
			},
		},
		{
			name: "invalid name (empty)",
			req: CreateChannelRequest{
				Name:      "",
				Type:      TypeStandard,
				CreatedBy: "agent-a",
			},
			wantErr: true,
			errIs:   ErrInvalidChannelName,
		},
		{
			name: "invalid name (special chars)",
			req: CreateChannelRequest{
				Name:      "ch@nnel!",
				Type:      TypeStandard,
				CreatedBy: "agent-a",
			},
			wantErr: true,
			errIs:   ErrInvalidChannelName,
		},
		{
			name: "invalid name (too long)",
			req: CreateChannelRequest{
				Name:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaX",
				Type:      TypeStandard,
				CreatedBy: "agent-a",
			},
			wantErr: true,
			errIs:   ErrInvalidChannelName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			ctx := context.Background()

			ch, err := svc.CreateChannel(ctx, tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errIs != nil && !errors.Is(err, tt.errIs) {
					t.Errorf("expected error %v, got %v", tt.errIs, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateChannel: %v", err)
			}

			if ch.ID == 0 {
				t.Error("channel ID should not be 0")
			}
			if ch.Name != NormalizeChannelName(tt.req.Name) {
				t.Errorf("name = %s, want %s", ch.Name, NormalizeChannelName(tt.req.Name))
			}
		})
	}
}

func TestService_CreateChannel_CreatorIsOwner(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, err := svc.CreateChannel(ctx, CreateChannelRequest{
		Name:      "owned-channel",
		Type:      TypeStandard,
		CreatedBy: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	members, err := svc.GetMembers(ctx, ch.ID)
	if err != nil {
		t.Fatalf("GetMembers: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("got %d members, want 1", len(members))
	}
	if members[0].AgentName != "agent-a" {
		t.Errorf("member = %s, want agent-a", members[0].AgentName)
	}
	if members[0].Role != RoleOwner {
		t.Errorf("role = %s, want owner", members[0].Role)
	}
}

func TestService_CreateChannel_DuplicateName(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.CreateChannel(ctx, CreateChannelRequest{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-a"})

	_, err := svc.CreateChannel(ctx, CreateChannelRequest{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-b"})
	if !errors.Is(err, ErrChannelNameConflict) {
		t.Errorf("expected ErrChannelNameConflict, got %v", err)
	}
}

func TestService_CreateChannel_NameNormalized(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, err := svc.CreateChannel(ctx, CreateChannelRequest{Name: "MyChannel", Type: TypeStandard, CreatedBy: "agent-a"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if ch.Name != "mychannel" {
		t.Errorf("name = %s, want mychannel", ch.Name)
	}
}

// --- JoinChannel tests ---

func TestService_JoinChannel(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-a"})

	t.Run("join public channel", func(t *testing.T) {
		err := svc.JoinChannel(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("JoinChannel: %v", err)
		}

		is, _ := svc.store.IsMember(ctx, ch.ID, "agent-b")
		if !is {
			t.Error("agent-b should be a member after joining")
		}
	})

	t.Run("join is idempotent", func(t *testing.T) {
		err := svc.JoinChannel(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Errorf("second JoinChannel should be idempotent, got: %v", err)
		}
	})

	t.Run("join non-existent channel fails", func(t *testing.T) {
		err := svc.JoinChannel(ctx, 99999, "agent-b")
		if !errors.Is(err, ErrChannelNotFound) {
			t.Errorf("expected ErrChannelNotFound, got %v", err)
		}
	})
}

func TestService_JoinChannel_PrivateRequiresInvite(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{
		Name:      "private-ch",
		Type:      TypeStandard,
		IsPrivate: true,
		CreatedBy: "agent-a",
	})

	t.Run("uninvited agent cannot join private channel", func(t *testing.T) {
		err := svc.JoinChannel(ctx, ch.ID, "agent-c")
		if !errors.Is(err, ErrNotInvited) {
			t.Errorf("expected ErrNotInvited, got %v", err)
		}
	})

	t.Run("invited agent can join private channel", func(t *testing.T) {
		svc.InviteToChannel(ctx, ch.ID, "agent-b", "agent-a")
		err := svc.JoinChannel(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("JoinChannel: %v", err)
		}

		is, _ := svc.store.IsMember(ctx, ch.ID, "agent-b")
		if !is {
			t.Error("agent-b should be a member after invite+join")
		}
	})

	t.Run("invite status changes to accepted after join", func(t *testing.T) {
		inv, err := svc.store.GetInvite(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("GetInvite: %v", err)
		}
		if inv.Status != InviteStatusAccepted {
			t.Errorf("invite status = %s, want accepted", inv.Status)
		}
	})
}

// --- LeaveChannel tests ---

func TestService_LeaveChannel(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-a"})
	svc.JoinChannel(ctx, ch.ID, "agent-b")

	t.Run("member can leave", func(t *testing.T) {
		err := svc.LeaveChannel(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("LeaveChannel: %v", err)
		}

		is, _ := svc.store.IsMember(ctx, ch.ID, "agent-b")
		if is {
			t.Error("agent-b should not be a member after leaving")
		}
	})

	t.Run("owner cannot leave", func(t *testing.T) {
		err := svc.LeaveChannel(ctx, ch.ID, "agent-a")
		if !errors.Is(err, ErrOwnerCannotLeave) {
			t.Errorf("expected ErrOwnerCannotLeave, got %v", err)
		}
	})

	t.Run("non-member cannot leave", func(t *testing.T) {
		err := svc.LeaveChannel(ctx, ch.ID, "agent-c")
		if !errors.Is(err, ErrNotChannelMember) {
			t.Errorf("expected ErrNotChannelMember, got %v", err)
		}
	})

	t.Run("can rejoin public channel after leaving", func(t *testing.T) {
		err := svc.JoinChannel(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("JoinChannel after leave: %v", err)
		}
	})
}

// --- InviteToChannel tests ---

func TestService_InviteToChannel(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{
		Name:      "private-ch",
		Type:      TypeStandard,
		IsPrivate: true,
		CreatedBy: "agent-a",
	})

	t.Run("owner can invite", func(t *testing.T) {
		err := svc.InviteToChannel(ctx, ch.ID, "agent-b", "agent-a")
		if err != nil {
			t.Fatalf("InviteToChannel: %v", err)
		}

		has, _ := svc.store.HasPendingInvite(ctx, ch.ID, "agent-b")
		if !has {
			t.Error("expected pending invite for agent-b")
		}
	})

	t.Run("non-owner cannot invite", func(t *testing.T) {
		// First join agent-b
		svc.JoinChannel(ctx, ch.ID, "agent-b")

		err := svc.InviteToChannel(ctx, ch.ID, "agent-c", "agent-b")
		if !errors.Is(err, ErrNotChannelOwner) {
			t.Errorf("expected ErrNotChannelOwner, got %v", err)
		}
	})

	t.Run("inviting already-member is idempotent", func(t *testing.T) {
		err := svc.InviteToChannel(ctx, ch.ID, "agent-b", "agent-a")
		if err != nil {
			t.Errorf("invite existing member should be idempotent, got: %v", err)
		}
	})

	t.Run("inviting to non-existent channel fails", func(t *testing.T) {
		err := svc.InviteToChannel(ctx, 99999, "agent-c", "agent-a")
		if !errors.Is(err, ErrChannelNotFound) {
			t.Errorf("expected ErrChannelNotFound, got %v", err)
		}
	})
}

// --- KickFromChannel tests ---

func TestService_KickFromChannel(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "team", Type: TypeStandard, CreatedBy: "agent-a"})
	svc.JoinChannel(ctx, ch.ID, "agent-b")

	t.Run("owner can kick a member", func(t *testing.T) {
		err := svc.KickFromChannel(ctx, ch.ID, "agent-b", "agent-a")
		if err != nil {
			t.Fatalf("KickFromChannel: %v", err)
		}

		is, _ := svc.store.IsMember(ctx, ch.ID, "agent-b")
		if is {
			t.Error("agent-b should not be a member after kick")
		}
	})

	t.Run("non-owner cannot kick", func(t *testing.T) {
		svc.JoinChannel(ctx, ch.ID, "agent-b")
		err := svc.KickFromChannel(ctx, ch.ID, "agent-b", "agent-b")
		if !errors.Is(err, ErrNotChannelOwner) {
			t.Errorf("expected ErrNotChannelOwner, got %v", err)
		}
	})

	t.Run("owner cannot kick themselves", func(t *testing.T) {
		err := svc.KickFromChannel(ctx, ch.ID, "agent-a", "agent-a")
		if err == nil {
			t.Error("expected error when owner kicks themselves")
		}
	})

	t.Run("kicking non-member fails", func(t *testing.T) {
		err := svc.KickFromChannel(ctx, ch.ID, "agent-c", "agent-a")
		if !errors.Is(err, ErrNotChannelMember) {
			t.Errorf("expected ErrNotChannelMember, got %v", err)
		}
	})
}

// --- ListChannels tests ---

func TestService_ListChannels(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	t.Run("empty list when no channels", func(t *testing.T) {
		result, err := svc.ListChannels(ctx, "agent-a")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("got %d channels, want 0", len(result))
		}
	})

	// Create channels
	svc.CreateChannel(ctx, CreateChannelRequest{Name: "public-1", Type: TypeStandard, CreatedBy: "agent-a"})
	svc.CreateChannel(ctx, CreateChannelRequest{Name: "public-2", Type: TypeStandard, CreatedBy: "agent-a"})
	privCh, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "private-1", Type: TypeStandard, IsPrivate: true, CreatedBy: "agent-a"})

	t.Run("lists public channels for outsider", func(t *testing.T) {
		result, err := svc.ListChannels(ctx, "agent-c")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("got %d channels, want 2 (public only)", len(result))
		}
	})

	t.Run("includes member count", func(t *testing.T) {
		result, err := svc.ListChannels(ctx, "agent-a")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		for _, ch := range result {
			if ch.MemberCount < 1 {
				t.Errorf("channel %s has member_count %d, want >= 1", ch.Name, ch.MemberCount)
			}
		}
	})

	t.Run("includes private channel for member", func(t *testing.T) {
		result, err := svc.ListChannels(ctx, "agent-a")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(result) != 3 {
			t.Errorf("got %d channels, want 3 (2 public + 1 private owned)", len(result))
		}
	})

	t.Run("includes private channel for invited agent", func(t *testing.T) {
		svc.InviteToChannel(ctx, privCh.ID, "agent-b", "agent-a")
		result, err := svc.ListChannels(ctx, "agent-b")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(result) != 3 {
			t.Errorf("got %d channels, want 3 (2 public + 1 invited private)", len(result))
		}
	})
}

// --- UpdateChannel tests ---

func TestService_UpdateChannel(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{
		Name:        "research",
		Description: "Research topics",
		Topic:       "Q1 findings",
		Type:        TypeStandard,
		CreatedBy:   "agent-a",
	})

	t.Run("owner can update topic", func(t *testing.T) {
		newTopic := "Q2 planning"
		updated, err := svc.UpdateChannel(ctx, ch.ID, UpdateChannelRequest{Topic: &newTopic}, "agent-a")
		if err != nil {
			t.Fatalf("UpdateChannel: %v", err)
		}
		if updated.Topic != "Q2 planning" {
			t.Errorf("topic = %s, want Q2 planning", updated.Topic)
		}
	})

	t.Run("owner can update description", func(t *testing.T) {
		newDesc := "Updated description"
		updated, err := svc.UpdateChannel(ctx, ch.ID, UpdateChannelRequest{Description: &newDesc}, "agent-a")
		if err != nil {
			t.Fatalf("UpdateChannel: %v", err)
		}
		if updated.Description != "Updated description" {
			t.Errorf("description = %s, want Updated description", updated.Description)
		}
	})

	t.Run("non-owner cannot update", func(t *testing.T) {
		svc.JoinChannel(ctx, ch.ID, "agent-b")
		newTopic := "Unauthorized"
		_, err := svc.UpdateChannel(ctx, ch.ID, UpdateChannelRequest{Topic: &newTopic}, "agent-b")
		if !errors.Is(err, ErrNotChannelOwner) {
			t.Errorf("expected ErrNotChannelOwner, got %v", err)
		}
	})

	t.Run("update non-existent channel fails", func(t *testing.T) {
		newTopic := "test"
		_, err := svc.UpdateChannel(ctx, 99999, UpdateChannelRequest{Topic: &newTopic}, "agent-a")
		if !errors.Is(err, ErrChannelNotFound) {
			t.Errorf("expected ErrChannelNotFound, got %v", err)
		}
	})
}

// --- BroadcastMessage tests ---

func TestService_BroadcastMessage(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-a"})

	t.Run("broadcast creates channel message", func(t *testing.T) {
		msgs, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "hello", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("got %d messages, want 1 (channel message)", len(msgs))
		}
		if msgs[0].ToAgent != "" {
			t.Errorf("channel message to_agent = %q, want empty", msgs[0].ToAgent)
		}
		if msgs[0].ChannelID == nil || *msgs[0].ChannelID != ch.ID {
			t.Errorf("channel_id = %v, want %d", msgs[0].ChannelID, ch.ID)
		}
	})

	t.Run("broadcast visible via GetChannelMessages", func(t *testing.T) {
		channelResult, err := svc.msgService.GetChannelMessages(ctx, ch.ID, 100, 0)
		if err != nil {
			t.Fatalf("GetChannelMessages: %v", err)
		}
		found := false
		for _, m := range channelResult.Messages {
			if m.Body == "hello" && m.FromAgent == "agent-a" {
				found = true
				break
			}
		}
		if !found {
			t.Error("broadcast message not found via GetChannelMessages")
		}
	})

	t.Run("broadcast without mentions sends no DMs", func(t *testing.T) {
		svc.JoinChannel(ctx, ch.ID, "agent-b")
		svc.JoinChannel(ctx, ch.ID, "agent-c")
		_, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "no-dm-test", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}

		// agent-b should NOT have an inbox notification (no @mention)
		inboxResult, _ := svc.msgService.ReadInbox(ctx, "agent-b", messaging.ReadOptions{IncludeRead: true})
		for _, m := range inboxResult.Messages {
			if m.Body == "no-dm-test" {
				t.Error("agent-b should not receive inbox DM for non-mention broadcast")
			}
		}
	})

	t.Run("sender does not receive own message", func(t *testing.T) {
		svc.BroadcastMessage(ctx, ch.ID, "agent-a", "no self-message", 5, "", nil, nil)
		inboxResult, _ := svc.msgService.ReadInbox(ctx, "agent-a", messaging.ReadOptions{IncludeRead: true})
		for _, m := range inboxResult.Messages {
			if m.Body == "no self-message" {
				t.Error("sender should not receive their own broadcast in inbox")
			}
		}
	})

	t.Run("non-member auto-joins public channel on broadcast", func(t *testing.T) {
		seedAgent(t, svc.store.(*SQLiteChannelStore).db, "outsider")
		_, err := svc.BroadcastMessage(ctx, ch.ID, "outsider", "auto-joined", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("expected auto-join for public channel, got %v", err)
		}
		isMember, _ := svc.IsMember(ctx, ch.ID, "outsider")
		if !isMember {
			t.Error("outsider should be a member after auto-join")
		}
	})

	t.Run("broadcast with reply_to", func(t *testing.T) {
		msgs, _ := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "original", 5, "", nil, nil)
		original := msgs[0]

		replies, err := svc.BroadcastMessage(ctx, ch.ID, "agent-b", "reply to original", 5, "", &original.ID, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage with reply_to: %v", err)
		}
		if replies[0].ReplyTo == nil || *replies[0].ReplyTo != original.ID {
			t.Errorf("reply_to = %v, want %d", replies[0].ReplyTo, original.ID)
		}
	})

	t.Run("non-member cannot broadcast to private channel", func(t *testing.T) {
		privCh, err := svc.CreateChannel(ctx, CreateChannelRequest{
			Name: "private-test", Type: TypeStandard, IsPrivate: true, CreatedBy: "agent-a",
		})
		if err != nil {
			t.Fatalf("create private channel: %v", err)
		}
		seedAgent(t, svc.store.(*SQLiteChannelStore).db, "outsider2")
		_, err = svc.BroadcastMessage(ctx, privCh.ID, "outsider2", "unauthorized", 5, "", nil, nil)
		if !errors.Is(err, ErrNotChannelMember) {
			t.Errorf("expected ErrNotChannelMember for private channel, got %v", err)
		}
	})

}

// --- @mentions in BroadcastMessage tests ---

func TestService_BroadcastMessage_Mentions(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "mentions-test", Type: TypeStandard, CreatedBy: "agent-a"})
	svc.JoinChannel(ctx, ch.ID, "agent-b")
	svc.JoinChannel(ctx, ch.ID, "agent-c")

	t.Run("mentioned member gets mention flag in inbox", func(t *testing.T) {
		_, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "hey @agent-b check this", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}

		// agent-b was mentioned — inbox notification should have mention:true
		inboxResult, _ := svc.msgService.ReadInbox(ctx, "agent-b", messaging.ReadOptions{IncludeRead: true})
		found := false
		for _, m := range inboxResult.Messages {
			if m.Body == "hey @agent-b check this" {
				found = true
				var meta map[string]any
				json.Unmarshal(m.Metadata, &meta)
				if meta["mention"] != true {
					t.Error("agent-b inbox notification should have mention=true")
				}
				break
			}
		}
		if !found {
			t.Error("agent-b did not receive inbox notification")
		}

		// agent-c was NOT mentioned — should NOT receive an inbox DM at all
		inboxResult, _ = svc.msgService.ReadInbox(ctx, "agent-c", messaging.ReadOptions{IncludeRead: true})
		for _, m := range inboxResult.Messages {
			if m.Body == "hey @agent-b check this" {
				t.Error("agent-c should not receive inbox DM when not @mentioned")
			}
		}
	})

	t.Run("channel message metadata includes mentioned_agents", func(t *testing.T) {
		_, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "cc @agent-b and @agent-c", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}

		channelResult2, _ := svc.msgService.GetChannelMessages(ctx, ch.ID, 10, 0)
		found := false
		for _, m := range channelResult2.Messages {
			if m.Body == "cc @agent-b and @agent-c" {
				found = true
				var meta map[string]any
				json.Unmarshal(m.Metadata, &meta)
				mentioned, ok := meta["mentioned_agents"]
				if !ok {
					t.Error("channel message should have mentioned_agents")
				} else {
					list := mentioned.([]any)
					if len(list) != 2 {
						t.Errorf("mentioned_agents = %v, want 2 entries", list)
					}
				}
				break
			}
		}
		if !found {
			t.Error("channel message not found")
		}
	})

	t.Run("self-mention is excluded", func(t *testing.T) {
		_, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "I am @agent-a and cc @agent-b", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}

		channelResult3, _ := svc.msgService.GetChannelMessages(ctx, ch.ID, 10, 0)
		for _, m := range channelResult3.Messages {
			if m.Body == "I am @agent-a and cc @agent-b" {
				var meta map[string]any
				json.Unmarshal(m.Metadata, &meta)
				mentioned := meta["mentioned_agents"].([]any)
				for _, name := range mentioned {
					if name == "agent-a" {
						t.Error("sender should not be in mentioned_agents")
					}
				}
				if len(mentioned) != 1 || mentioned[0] != "agent-b" {
					t.Errorf("mentioned_agents = %v, want [agent-b]", mentioned)
				}
				break
			}
		}
	})

	t.Run("no mentions produces no mention metadata", func(t *testing.T) {
		_, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "just a normal message", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}

		channelResult4, _ := svc.msgService.GetChannelMessages(ctx, ch.ID, 10, 0)
		for _, m := range channelResult4.Messages {
			if m.Body == "just a normal message" {
				var meta map[string]any
				json.Unmarshal(m.Metadata, &meta)
				if _, ok := meta["mentioned_agents"]; ok {
					t.Error("should not have mentioned_agents when no mentions")
				}
				break
			}
		}
	})

	t.Run("non-member mention is ignored", func(t *testing.T) {
		seedAgent(t, svc.store.(*SQLiteChannelStore).db, "outsider")
		_, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "hey @outsider and @agent-b", 5, "", nil, nil)
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}

		channelResult5, _ := svc.msgService.GetChannelMessages(ctx, ch.ID, 10, 0)
		for _, m := range channelResult5.Messages {
			if m.Body == "hey @outsider and @agent-b" {
				var meta map[string]any
				json.Unmarshal(m.Metadata, &meta)
				mentioned := meta["mentioned_agents"].([]any)
				for _, name := range mentioned {
					if name == "outsider" {
						t.Error("non-member should not be in mentioned_agents")
					}
				}
				if len(mentioned) != 1 || mentioned[0] != "agent-b" {
					t.Errorf("mentioned_agents = %v, want [agent-b]", mentioned)
				}
				break
			}
		}
	})
}

// --- Trace recording tests ---

func TestService_TracesRecorded(t *testing.T) {
	svc, db := newTestService(t)
	ctx := context.Background()

	// Create + join should generate traces
	ch, _ := svc.CreateChannel(ctx, CreateChannelRequest{Name: "traced", Type: TypeStandard, CreatedBy: "agent-a"})
	svc.JoinChannel(ctx, ch.ID, "agent-b")
	svc.LeaveChannel(ctx, ch.ID, "agent-b")

	// Wait for async trace writes
	var count int
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		db.QueryRow("SELECT COUNT(*) FROM traces WHERE action LIKE 'channel.%'").Scan(&count)
		if count >= 3 {
			break
		}
	}
	if count < 3 {
		t.Errorf("expected at least 3 channel traces, got %d", count)
	}
}

// --- EnsureMyAgentsChannel tests ---

func TestService_EnsureMyAgentsChannel_CreatesOnFirstCall(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	err := svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")
	if err != nil {
		t.Fatalf("EnsureMyAgentsChannel: %v", err)
	}

	ch, err := svc.GetChannelByName(ctx, "my-agents-testowner")
	if err != nil {
		t.Fatalf("GetChannelByName: %v", err)
	}

	if ch.Name != "my-agents-testowner" {
		t.Errorf("name = %s, want my-agents-testowner", ch.Name)
	}
	if !ch.IsPrivate {
		t.Error("channel should be private")
	}
	if !ch.IsSystem {
		t.Error("channel should be system")
	}
	if ch.CreatedBy != "agent-a" {
		t.Errorf("created_by = %s, want agent-a", ch.CreatedBy)
	}

	// Verify agent-a is the owner
	member, err := svc.store.GetMember(ctx, ch.ID, "agent-a")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if member.Role != RoleOwner {
		t.Errorf("role = %s, want owner", member.Role)
	}
}

func TestService_EnsureMyAgentsChannel_Idempotent(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// First call creates the channel
	err := svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")
	if err != nil {
		t.Fatalf("first EnsureMyAgentsChannel: %v", err)
	}

	ch1, _ := svc.GetChannelByName(ctx, "my-agents-testowner")

	// Second call should be a no-op
	err = svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")
	if err != nil {
		t.Fatalf("second EnsureMyAgentsChannel: %v", err)
	}

	ch2, _ := svc.GetChannelByName(ctx, "my-agents-testowner")

	if ch1.ID != ch2.ID {
		t.Errorf("channel IDs should match: %d != %d", ch1.ID, ch2.ID)
	}

	// Should still only have one member (the owner)
	members, err := svc.GetMembers(ctx, ch1.ID)
	if err != nil {
		t.Fatalf("GetMembers: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("got %d members, want 1", len(members))
	}
}

func TestService_EnsureMyAgentsChannel_PrivateAndSystem(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")

	ch, _ := svc.GetChannelByName(ctx, "my-agents-testowner")
	if !ch.IsPrivate {
		t.Error("my-agents channel should be private")
	}
	if !ch.IsSystem {
		t.Error("my-agents channel should be system")
	}
}

func TestService_LeaveChannel_SystemChannelBlocked(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")
	ch, _ := svc.GetChannelByName(ctx, "my-agents-testowner")

	// Add agent-b as a member directly
	svc.store.AddMember(ctx, &Membership{
		ChannelID: ch.ID,
		AgentName: "agent-b",
		Role:      RoleMember,
	})

	// Any member should not be able to leave a system channel
	err := svc.LeaveChannel(ctx, ch.ID, "agent-b")
	if !errors.Is(err, ErrSystemChannel) {
		t.Errorf("expected ErrSystemChannel, got %v", err)
	}
}

func TestService_DeleteChannel_SystemChannelBlocked(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")
	ch, _ := svc.GetChannelByName(ctx, "my-agents-testowner")

	err := svc.DeleteChannel(ctx, ch.ID)
	if !errors.Is(err, ErrSystemChannel) {
		t.Errorf("expected ErrSystemChannel, got %v", err)
	}

	// Regular channel should be deletable
	regularCh, _ := svc.CreateChannel(ctx, CreateChannelRequest{
		Name:      "deletable",
		Type:      TypeStandard,
		CreatedBy: "agent-a",
	})
	err = svc.DeleteChannel(ctx, regularCh.ID)
	if err != nil {
		t.Fatalf("DeleteChannel regular: %v", err)
	}
}

func TestService_JoinMyAgentsChannel(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Create my-agents channel
	svc.EnsureMyAgentsChannel(ctx, "testowner", "agent-a")

	// Join agent-b to the my-agents channel
	err := svc.JoinMyAgentsChannel(ctx, "testowner", "agent-b")
	if err != nil {
		t.Fatalf("JoinMyAgentsChannel: %v", err)
	}

	ch, _ := svc.GetChannelByName(ctx, "my-agents-testowner")
	isMember, _ := svc.store.IsMember(ctx, ch.ID, "agent-b")
	if !isMember {
		t.Error("agent-b should be a member of my-agents channel")
	}
}

func TestService_JoinMyAgentsChannel_NoChannelIsNoop(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// If the channel doesn't exist yet, this should be a no-op
	err := svc.JoinMyAgentsChannel(ctx, "nonexistent", "agent-a")
	if err != nil {
		t.Fatalf("JoinMyAgentsChannel should be no-op when channel missing: %v", err)
	}
}
