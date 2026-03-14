package channels

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/storage"
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

	t.Run("broadcast to zero other members", func(t *testing.T) {
		msgs, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "hello", 5, "")
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}
		if len(msgs) != 0 {
			t.Errorf("got %d messages, want 0 (no other members)", len(msgs))
		}
	})

	t.Run("broadcast to one member", func(t *testing.T) {
		svc.JoinChannel(ctx, ch.ID, "agent-b")
		msgs, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "hello everyone", 5, "")
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("got %d messages, want 1", len(msgs))
		}
		if msgs[0].ToAgent != "agent-b" {
			t.Errorf("to_agent = %s, want agent-b", msgs[0].ToAgent)
		}
	})

	t.Run("broadcast to multiple members", func(t *testing.T) {
		svc.JoinChannel(ctx, ch.ID, "agent-c")
		msgs, err := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "broadcast test", 5, "")
		if err != nil {
			t.Fatalf("BroadcastMessage: %v", err)
		}
		if len(msgs) != 2 {
			t.Errorf("got %d messages, want 2", len(msgs))
		}
	})

	t.Run("sender does not receive own message", func(t *testing.T) {
		msgs, _ := svc.BroadcastMessage(ctx, ch.ID, "agent-a", "no self-message", 5, "")
		for _, m := range msgs {
			if m.ToAgent == "agent-a" {
				t.Error("sender should not receive their own broadcast message")
			}
		}
	})

	t.Run("non-member cannot broadcast", func(t *testing.T) {
		// agent-c is a member but let's test someone who isn't
		seedAgent(t, svc.store.(*SQLiteChannelStore).db, "outsider")
		_, err := svc.BroadcastMessage(ctx, ch.ID, "outsider", "unauthorized", 5, "")
		if !errors.Is(err, ErrNotChannelMember) {
			t.Errorf("expected ErrNotChannelMember, got %v", err)
		}
	})

	t.Run("broadcast delivers to inbox", func(t *testing.T) {
		svc.BroadcastMessage(ctx, ch.ID, "agent-a", "inbox test", 5, "")

		// Read agent-b's inbox
		msgs, err := svc.msgService.ReadInbox(ctx, "agent-b", messaging.ReadOptions{IncludeRead: true})
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		found := false
		for _, m := range msgs {
			if m.Body == "inbox test" && m.FromAgent == "agent-a" {
				found = true
				// Channel info is in metadata
				if string(m.Metadata) == "{}" {
					t.Error("message metadata should contain channel info")
				}
				break
			}
		}
		if !found {
			t.Error("agent-b should have received the broadcast message in inbox")
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

// suppress unused import warning
var _ = storage.RunMigrations
