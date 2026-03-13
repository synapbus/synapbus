package channels

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/smart-mcp-proxy/synapbus/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Seed test user
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)

	return db
}

func seedAgent(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	db.Exec(`INSERT OR IGNORE INTO users (id, username, password_hash, display_name) VALUES (1, 'testowner', 'hash', 'Test Owner')`)
	_, err := db.Exec(
		`INSERT OR IGNORE INTO agents (name, display_name, type, capabilities, owner_id, api_key_hash, status) VALUES (?, ?, 'ai', '{}', 1, 'testhash', 'active')`,
		name, name,
	)
	if err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
}

func TestSQLiteChannelStore_CreateChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		wantErr error
	}{
		{
			name: "create public channel",
			channel: Channel{
				Name:        "alerts",
				Description: "System alerts",
				Topic:       "Current alerts",
				Type:        TypeStandard,
				IsPrivate:   false,
				CreatedBy:   "agent-a",
			},
		},
		{
			name: "create private channel",
			channel: Channel{
				Name:        "core-team",
				Description: "Core team only",
				Type:        TypeStandard,
				IsPrivate:   true,
				CreatedBy:   "agent-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			store := NewSQLiteChannelStore(db)
			ctx := context.Background()

			ch := tt.channel
			err := store.CreateChannel(ctx, &ch)
			if err != nil {
				t.Fatalf("CreateChannel: %v", err)
			}

			if ch.ID == 0 {
				t.Error("channel ID should not be 0")
			}

			// Verify retrieval
			got, err := store.GetChannel(ctx, ch.ID)
			if err != nil {
				t.Fatalf("GetChannel: %v", err)
			}
			if got.Name != tt.channel.Name {
				t.Errorf("name = %s, want %s", got.Name, tt.channel.Name)
			}
			if got.IsPrivate != tt.channel.IsPrivate {
				t.Errorf("is_private = %v, want %v", got.IsPrivate, tt.channel.IsPrivate)
			}
		})
	}
}

func TestSQLiteChannelStore_CreateChannel_DuplicateName(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	ch1 := &Channel{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-a"}
	if err := store.CreateChannel(ctx, ch1); err != nil {
		t.Fatalf("CreateChannel 1: %v", err)
	}

	ch2 := &Channel{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-b"}
	err := store.CreateChannel(ctx, ch2)
	if err != ErrChannelNameConflict {
		t.Errorf("expected ErrChannelNameConflict, got %v", err)
	}
}

func TestSQLiteChannelStore_GetChannelByName(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	ch := &Channel{Name: "alerts", Type: TypeStandard, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, ch)

	tests := []struct {
		name    string
		lookup  string
		wantErr bool
	}{
		{"exact match", "alerts", false},
		{"case insensitive", "ALERTS", false},
		{"not found", "nonexistent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetChannelByName(ctx, tt.lookup)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("GetChannelByName: %v", err)
			}
			if got.Name != "alerts" {
				t.Errorf("name = %s, want alerts", got.Name)
			}
		})
	}
}

func TestSQLiteChannelStore_GetChannel_NotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	_, err := store.GetChannel(ctx, 99999)
	if err != ErrChannelNotFound {
		t.Errorf("expected ErrChannelNotFound, got %v", err)
	}
}

func TestSQLiteChannelStore_ListChannels(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	// Create public channels
	pub1 := &Channel{Name: "alerts", Type: TypeStandard, IsPrivate: false, CreatedBy: "agent-a"}
	pub2 := &Channel{Name: "general", Type: TypeStandard, IsPrivate: false, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, pub1)
	store.CreateChannel(ctx, pub2)

	// Create private channel
	priv := &Channel{Name: "secret", Type: TypeStandard, IsPrivate: true, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, priv)

	t.Run("lists public channels for uninvited agent", func(t *testing.T) {
		channels, err := store.ListChannels(ctx, "agent-b")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(channels) != 2 {
			t.Errorf("got %d channels, want 2 (public only)", len(channels))
		}
	})

	t.Run("includes private channel when agent is a member", func(t *testing.T) {
		store.AddMember(ctx, &Membership{ChannelID: priv.ID, AgentName: "agent-b", Role: RoleMember})
		channels, err := store.ListChannels(ctx, "agent-b")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(channels) != 3 {
			t.Errorf("got %d channels, want 3 (public + member of private)", len(channels))
		}
	})

	t.Run("includes private channel when agent has pending invite", func(t *testing.T) {
		priv2 := &Channel{Name: "invited-only", Type: TypeStandard, IsPrivate: true, CreatedBy: "agent-a"}
		store.CreateChannel(ctx, priv2)

		inv := &ChannelInvite{ChannelID: priv2.ID, AgentName: "agent-c", InvitedBy: "agent-a"}
		store.CreateInvite(ctx, inv)

		channels, err := store.ListChannels(ctx, "agent-c")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(channels) != 3 {
			t.Errorf("got %d channels, want 3 (2 public + 1 invited private)", len(channels))
		}
	})

	t.Run("empty list when no channels", func(t *testing.T) {
		db2 := newTestDB(t)
		store2 := NewSQLiteChannelStore(db2)
		channels, err := store2.ListChannels(ctx, "agent-x")
		if err != nil {
			t.Fatalf("ListChannels: %v", err)
		}
		if len(channels) != 0 {
			t.Errorf("got %d channels, want 0", len(channels))
		}
	})
}

func TestSQLiteChannelStore_UpdateChannel(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	ch := &Channel{Name: "research", Topic: "Q1 findings", Type: TypeStandard, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, ch)

	ch.Topic = "Q2 planning"
	ch.Description = "Updated description"
	if err := store.UpdateChannel(ctx, ch); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}

	got, err := store.GetChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Topic != "Q2 planning" {
		t.Errorf("topic = %s, want Q2 planning", got.Topic)
	}
	if got.Description != "Updated description" {
		t.Errorf("description = %s, want Updated description", got.Description)
	}
}

func TestSQLiteChannelStore_DeleteChannel(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	ch := &Channel{Name: "temp", Type: TypeStandard, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, ch)

	if err := store.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	_, err := store.GetChannel(ctx, ch.ID)
	if err != ErrChannelNotFound {
		t.Errorf("expected ErrChannelNotFound after delete, got %v", err)
	}
}

func TestSQLiteChannelStore_Membership(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	ch := &Channel{Name: "test-ch", Type: TypeStandard, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, ch)

	t.Run("add member", func(t *testing.T) {
		m := &Membership{ChannelID: ch.ID, AgentName: "agent-a", Role: RoleOwner}
		if err := store.AddMember(ctx, m); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
		if m.ID == 0 {
			t.Error("membership ID should not be 0")
		}
	})

	t.Run("add duplicate member is idempotent", func(t *testing.T) {
		m := &Membership{ChannelID: ch.ID, AgentName: "agent-a", Role: RoleMember}
		err := store.AddMember(ctx, m)
		if err != nil {
			t.Fatalf("AddMember duplicate: %v", err)
		}
	})

	t.Run("is member", func(t *testing.T) {
		yes, err := store.IsMember(ctx, ch.ID, "agent-a")
		if err != nil {
			t.Fatalf("IsMember: %v", err)
		}
		if !yes {
			t.Error("expected agent-a to be a member")
		}

		no, err := store.IsMember(ctx, ch.ID, "agent-x")
		if err != nil {
			t.Fatalf("IsMember: %v", err)
		}
		if no {
			t.Error("expected agent-x to NOT be a member")
		}
	})

	t.Run("get member", func(t *testing.T) {
		m, err := store.GetMember(ctx, ch.ID, "agent-a")
		if err != nil {
			t.Fatalf("GetMember: %v", err)
		}
		if m.Role != RoleOwner {
			t.Errorf("role = %s, want owner", m.Role)
		}
	})

	t.Run("get member not found", func(t *testing.T) {
		_, err := store.GetMember(ctx, ch.ID, "nonexistent")
		if err != ErrNotChannelMember {
			t.Errorf("expected ErrNotChannelMember, got %v", err)
		}
	})

	t.Run("count members", func(t *testing.T) {
		store.AddMember(ctx, &Membership{ChannelID: ch.ID, AgentName: "agent-b", Role: RoleMember})
		count, err := store.CountMembers(ctx, ch.ID)
		if err != nil {
			t.Fatalf("CountMembers: %v", err)
		}
		if count != 2 {
			t.Errorf("count = %d, want 2", count)
		}
	})

	t.Run("get members", func(t *testing.T) {
		members, err := store.GetMembers(ctx, ch.ID)
		if err != nil {
			t.Fatalf("GetMembers: %v", err)
		}
		if len(members) != 2 {
			t.Errorf("got %d members, want 2", len(members))
		}
	})

	t.Run("remove member", func(t *testing.T) {
		if err := store.RemoveMember(ctx, ch.ID, "agent-b"); err != nil {
			t.Fatalf("RemoveMember: %v", err)
		}

		is, _ := store.IsMember(ctx, ch.ID, "agent-b")
		if is {
			t.Error("agent-b should no longer be a member")
		}
	})

	t.Run("remove non-member fails", func(t *testing.T) {
		err := store.RemoveMember(ctx, ch.ID, "nonexistent")
		if err != ErrNotChannelMember {
			t.Errorf("expected ErrNotChannelMember, got %v", err)
		}
	})
}

func TestSQLiteChannelStore_Invites(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteChannelStore(db)
	ctx := context.Background()

	ch := &Channel{Name: "private-ch", Type: TypeStandard, IsPrivate: true, CreatedBy: "agent-a"}
	store.CreateChannel(ctx, ch)

	t.Run("create invite", func(t *testing.T) {
		inv := &ChannelInvite{ChannelID: ch.ID, AgentName: "agent-b", InvitedBy: "agent-a"}
		if err := store.CreateInvite(ctx, inv); err != nil {
			t.Fatalf("CreateInvite: %v", err)
		}
	})

	t.Run("has pending invite", func(t *testing.T) {
		has, err := store.HasPendingInvite(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("HasPendingInvite: %v", err)
		}
		if !has {
			t.Error("expected pending invite for agent-b")
		}

		has, err = store.HasPendingInvite(ctx, ch.ID, "agent-c")
		if err != nil {
			t.Fatalf("HasPendingInvite: %v", err)
		}
		if has {
			t.Error("expected no pending invite for agent-c")
		}
	})

	t.Run("get invite", func(t *testing.T) {
		inv, err := store.GetInvite(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("GetInvite: %v", err)
		}
		if inv.Status != InviteStatusPending {
			t.Errorf("status = %s, want pending", inv.Status)
		}
		if inv.InvitedBy != "agent-a" {
			t.Errorf("invited_by = %s, want agent-a", inv.InvitedBy)
		}
	})

	t.Run("accept invite", func(t *testing.T) {
		if err := store.AcceptInvite(ctx, ch.ID, "agent-b"); err != nil {
			t.Fatalf("AcceptInvite: %v", err)
		}

		inv, err := store.GetInvite(ctx, ch.ID, "agent-b")
		if err != nil {
			t.Fatalf("GetInvite after accept: %v", err)
		}
		if inv.Status != InviteStatusAccepted {
			t.Errorf("status = %s, want accepted", inv.Status)
		}

		// Should no longer be pending
		has, _ := store.HasPendingInvite(ctx, ch.ID, "agent-b")
		if has {
			t.Error("should not have pending invite after acceptance")
		}
	})

	t.Run("duplicate invite is idempotent (resets to pending)", func(t *testing.T) {
		inv := &ChannelInvite{ChannelID: ch.ID, AgentName: "agent-b", InvitedBy: "agent-a"}
		if err := store.CreateInvite(ctx, inv); err != nil {
			t.Fatalf("CreateInvite duplicate: %v", err)
		}
		has, _ := store.HasPendingInvite(ctx, ch.ID, "agent-b")
		if !has {
			t.Error("re-invite should create a pending invite again")
		}
	})

	t.Run("accept non-existent invite fails", func(t *testing.T) {
		err := store.AcceptInvite(ctx, ch.ID, "nonexistent")
		if err != ErrNotInvited {
			t.Errorf("expected ErrNotInvited, got %v", err)
		}
	})
}

// suppress unused import warning
var _ = storage.RunMigrations
