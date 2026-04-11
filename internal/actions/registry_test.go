package actions

import (
	"testing"
)

func TestRegistryHasAllActions(t *testing.T) {
	r := NewRegistry()
	got := len(r.List())
	const want = 41 // 35 original + 6 marketplace (spec 016)
	if got != want {
		t.Errorf("expected %d actions, got %d", want, got)
	}
}

func TestRegistryCategories(t *testing.T) {
	r := NewRegistry()

	tests := []struct {
		category string
		want     int
	}{
		{"messaging", 7},
		{"channels", 9},
		{"swarm", 5},
		{"attachments", 2},
		{"reactions", 4},
		{"threads", 1},
		{"trust", 1},
		{"data", 1},
		{"wiki", 5},
		{"marketplace", 6},
	}

	for _, tt := range tests {
		t.Run(tt.category, func(t *testing.T) {
			got := len(r.ListByCategory(tt.category))
			if got != tt.want {
				t.Errorf("category %q: expected %d actions, got %d", tt.category, tt.want, got)
			}
		})
	}
}

func TestRegistryGetByName(t *testing.T) {
	r := NewRegistry()

	allNames := []string{
		// messaging
		"my_status", "send_message", "read_inbox", "claim_messages", "mark_done", "search_messages", "discover_agents",
		// channels
		"create_channel", "join_channel", "leave_channel", "list_channels",
		"invite_to_channel", "kick_from_channel", "get_channel_messages",
		"send_channel_message", "update_channel",
		// swarm
		"post_task", "bid_task", "accept_bid", "complete_task", "list_tasks",
		// attachments
		"upload_attachment", "download_attachment",
		// reactions
		"react", "unreact", "get_reactions", "list_by_state",
		// threads
		"get_replies",
		// trust
		"get_trust",
		// data
		"query",
		// wiki
		"create_article", "get_article", "update_article", "list_articles", "get_backlinks",
		// marketplace (spec 016)
		"post_auction", "bid", "award", "mark_task_done", "read_skill_card", "query_reputation",
	}

	for _, name := range allNames {
		t.Run(name, func(t *testing.T) {
			a, ok := r.Get(name)
			if !ok {
				t.Fatalf("action %q not found in registry", name)
			}
			if a.Name != name {
				t.Errorf("expected name %q, got %q", name, a.Name)
			}
		})
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent_action")
	if ok {
		t.Error("expected Get to return false for nonexistent action")
	}
}

func TestRegistryActionsHaveExamples(t *testing.T) {
	r := NewRegistry()
	for _, a := range r.List() {
		t.Run(a.Name, func(t *testing.T) {
			if len(a.Examples) == 0 {
				t.Errorf("action %q has no examples", a.Name)
			}
		})
	}
}

func TestRegistryActionsHaveDescriptions(t *testing.T) {
	r := NewRegistry()
	for _, a := range r.List() {
		t.Run(a.Name, func(t *testing.T) {
			if a.Description == "" {
				t.Errorf("action %q has empty description", a.Name)
			}
		})
	}
}

func TestRegistryActionsHaveReturns(t *testing.T) {
	r := NewRegistry()
	for _, a := range r.List() {
		t.Run(a.Name, func(t *testing.T) {
			if a.Returns == "" {
				t.Errorf("action %q has empty Returns field", a.Name)
			}
		})
	}
}

func TestRegistryListByUnknownCategory(t *testing.T) {
	r := NewRegistry()
	got := r.ListByCategory("nonexistent")
	if len(got) != 0 {
		t.Errorf("expected 0 actions for unknown category, got %d", len(got))
	}
}

func TestRegistryListReturnsCopy(t *testing.T) {
	r := NewRegistry()
	list1 := r.List()
	list2 := r.List()
	// Mutating the first list should not affect the second.
	if len(list1) > 0 {
		list1[0].Name = "mutated"
		if list2[0].Name == "mutated" {
			t.Error("List() should return a copy, not a reference to internal slice")
		}
	}
}
