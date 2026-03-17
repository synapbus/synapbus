package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/messaging"
)

func setupNotificationsRouter(t *testing.T) (chi.Router, *messaging.MessagingService, *agents.AgentService, *channels.Service) {
	t.Helper()
	db := newTestDBFull(t)

	msgStore := messaging.NewSQLiteMessageStore(db)
	msgService := messaging.NewMessagingService(msgStore, nil)

	agentStore := agents.NewSQLiteAgentStore(db)
	agentService := agents.NewAgentService(agentStore, nil)

	channelStore := channels.NewSQLiteChannelStore(db)
	channelService := channels.NewService(channelStore, msgService, nil)

	// Seed agents — human-agent must be type 'human' for GetHumanAgentForUser
	seedTestAgentWithType(t, db, "human-agent", "human", 1)
	seedTestAgent(t, db, "bot-alice", 2)
	seedTestAgent(t, db, "bot-bob", 2)

	handler := NewNotificationsHandler(msgService, agentService, channelService)
	messagesHandler := NewMessagesHandler(msgService, agentService)
	channelsHandler := NewChannelsHandler(channelService, agentService, msgService)

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(OwnerAuthMiddleware)
		r.Get("/api/notifications/unread", handler.UnreadCounts)
		r.Post("/api/notifications/mark-read", handler.MarkRead)
		r.Get("/api/channels/{name}/messages", channelsHandler.ChannelMessages)
		r.Get("/api/agents/{name}/messages", messagesHandler.DMMessages)
	})

	return router, msgService, agentService, channelService
}

func TestUnreadCounts_Unauthenticated(t *testing.T) {
	router, _, _, _ := setupNotificationsRouter(t)

	req := httptest.NewRequest("GET", "/api/notifications/unread", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestUnreadCounts_NoAgents(t *testing.T) {
	router, _, _, _ := setupNotificationsRouter(t)

	// Owner 99 has no agents
	req := httptest.NewRequest("GET", "/api/notifications/unread", nil)
	req.Header.Set("X-Owner-ID", "99")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Channels    []channelUnread `json:"channels"`
		DMs         []dmUnread      `json:"dms"`
		TotalUnread int             `json:"total_unread"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.Channels) != 0 {
		t.Errorf("channels = %d, want 0", len(resp.Channels))
	}
	if len(resp.DMs) != 0 {
		t.Errorf("dms = %d, want 0", len(resp.DMs))
	}
	if resp.TotalUnread != 0 {
		t.Errorf("total_unread = %d, want 0", resp.TotalUnread)
	}
}

func TestUnreadCounts_WithDMs(t *testing.T) {
	router, msgService, _, _ := setupNotificationsRouter(t)

	ctx := t.Context()

	// bot-alice sends DMs to human-agent (owner 1)
	_, err := msgService.SendMessage(ctx, "bot-alice", "human-agent", "Hello from Alice", messaging.SendOptions{Subject: "dm"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	_, err = msgService.SendMessage(ctx, "bot-alice", "human-agent", "Second message from Alice", messaging.SendOptions{Subject: "dm"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	// bot-bob sends a DM to human-agent
	_, err = msgService.SendMessage(ctx, "bot-bob", "human-agent", "Hello from Bob", messaging.SendOptions{Subject: "dm"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/notifications/unread", nil)
	req.Header.Set("X-Owner-ID", "1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Channels    []channelUnread `json:"channels"`
		DMs         []dmUnread      `json:"dms"`
		TotalUnread int             `json:"total_unread"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.DMs) != 2 {
		t.Fatalf("dms = %d, want 2; body: %s", len(resp.DMs), rr.Body.String())
	}

	// Find alice and bob counts
	dmsByAgent := make(map[string]dmUnread)
	for _, dm := range resp.DMs {
		dmsByAgent[dm.Agent] = dm
	}

	if alice, ok := dmsByAgent["bot-alice"]; !ok {
		t.Error("expected DM from bot-alice")
	} else if alice.UnreadCount != 2 {
		t.Errorf("bot-alice unread = %d, want 2", alice.UnreadCount)
	}

	if bob, ok := dmsByAgent["bot-bob"]; !ok {
		t.Error("expected DM from bot-bob")
	} else if bob.UnreadCount != 1 {
		t.Errorf("bot-bob unread = %d, want 1", bob.UnreadCount)
	}

	if resp.TotalUnread != 3 {
		t.Errorf("total_unread = %d, want 3", resp.TotalUnread)
	}
}

func TestMarkRead_DM(t *testing.T) {
	router, msgService, _, _ := setupNotificationsRouter(t)

	ctx := t.Context()

	// bot-alice sends 3 DMs to human-agent
	msg1, _ := msgService.SendMessage(ctx, "bot-alice", "human-agent", "Message 1", messaging.SendOptions{Subject: "dm"})
	_, _ = msgService.SendMessage(ctx, "bot-alice", "human-agent", "Message 2", messaging.SendOptions{Subject: "dm"})
	msg3, _ := msgService.SendMessage(ctx, "bot-alice", "human-agent", "Message 3", messaging.SendOptions{Subject: "dm"})

	// Mark read up to msg1
	body, _ := json.Marshal(map[string]any{
		"type":            "dm",
		"target":          "bot-alice",
		"last_message_id": msg1.ID,
	})
	req := httptest.NewRequest("POST", "/api/notifications/mark-read", bytes.NewReader(body))
	req.Header.Set("X-Owner-ID", "1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("mark-read status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Check unread — should have 2 unread still
	req = httptest.NewRequest("GET", "/api/notifications/unread", nil)
	req.Header.Set("X-Owner-ID", "1")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	var resp struct {
		DMs         []dmUnread `json:"dms"`
		TotalUnread int        `json:"total_unread"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.DMs) != 1 {
		t.Fatalf("dms = %d, want 1; body: %s", len(resp.DMs), rr.Body.String())
	}
	if resp.DMs[0].UnreadCount != 2 {
		t.Errorf("unread after mark-read = %d, want 2", resp.DMs[0].UnreadCount)
	}

	// Mark all read up to msg3
	body, _ = json.Marshal(map[string]any{
		"type":            "dm",
		"target":          "bot-alice",
		"last_message_id": msg3.ID,
	})
	req = httptest.NewRequest("POST", "/api/notifications/mark-read", bytes.NewReader(body))
	req.Header.Set("X-Owner-ID", "1")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("mark-read status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Check unread — should be 0
	req = httptest.NewRequest("GET", "/api/notifications/unread", nil)
	req.Header.Set("X-Owner-ID", "1")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.TotalUnread != 0 {
		t.Errorf("total_unread after marking all read = %d, want 0; body: %s", resp.TotalUnread, rr.Body.String())
	}
}

func TestMarkRead_Validation(t *testing.T) {
	router, _, _, _ := setupNotificationsRouter(t)

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "missing type",
			body: map[string]any{"target": "foo", "last_message_id": 1},
			want: http.StatusBadRequest,
		},
		{
			name: "missing target",
			body: map[string]any{"type": "dm", "last_message_id": 1},
			want: http.StatusBadRequest,
		},
		{
			name: "missing last_message_id",
			body: map[string]any{"type": "dm", "target": "foo"},
			want: http.StatusBadRequest,
		},
		{
			name: "invalid type",
			body: map[string]any{"type": "invalid", "target": "foo", "last_message_id": 1},
			want: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/notifications/mark-read", bytes.NewReader(body))
			req.Header.Set("X-Owner-ID", "1")
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != tt.want {
				t.Errorf("status = %d, want %d, body: %s", rr.Code, tt.want, rr.Body.String())
			}
		})
	}
}

func TestDMMessages_IncludesLastRead(t *testing.T) {
	router, msgService, _, _ := setupNotificationsRouter(t)

	ctx := t.Context()

	// Send DMs from bot-alice to human-agent
	msg1, _ := msgService.SendMessage(ctx, "bot-alice", "human-agent", "Hello", messaging.SendOptions{Subject: "dm"})
	_, _ = msgService.SendMessage(ctx, "bot-alice", "human-agent", "World", messaging.SendOptions{Subject: "dm"})

	// Mark read up to msg1
	body, _ := json.Marshal(map[string]any{
		"type":            "dm",
		"target":          "bot-alice",
		"last_message_id": msg1.ID,
	})
	req := httptest.NewRequest("POST", "/api/notifications/mark-read", bytes.NewReader(body))
	req.Header.Set("X-Owner-ID", "1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("mark-read status = %d", rr.Code)
	}

	// GET DM messages should include last_read_message_id
	req = httptest.NewRequest("GET", "/api/agents/bot-alice/messages", nil)
	req.Header.Set("X-Owner-ID", "1")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("dm messages status = %d, body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)

	lastRead, ok := resp["last_read_message_id"]
	if !ok {
		t.Fatal("response missing last_read_message_id")
	}
	if int64(lastRead.(float64)) != msg1.ID {
		t.Errorf("last_read_message_id = %v, want %d", lastRead, msg1.ID)
	}
}

func TestChannelMessages_IncludesLastRead(t *testing.T) {
	router, _, _, channelService := setupNotificationsRouter(t)

	ctx := t.Context()

	// Create a channel and have human-agent join
	ch, err := channelService.CreateChannel(ctx, channels.CreateChannelRequest{
		Name:      "test-channel",
		CreatedBy: "human-agent",
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// Broadcast messages
	msgs, err := channelService.BroadcastMessage(ctx, ch.ID, "human-agent", "Hello channel", 5, "", nil, nil)
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// Mark read up to the first channel message
	body, _ := json.Marshal(map[string]any{
		"type":            "channel",
		"target":          "test-channel",
		"last_message_id": msgs[0].ID,
	})
	req := httptest.NewRequest("POST", "/api/notifications/mark-read", bytes.NewReader(body))
	req.Header.Set("X-Owner-ID", "1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("mark-read status = %d, body: %s", rr.Code, rr.Body.String())
	}

	// GET channel messages should include last_read_message_id
	req = httptest.NewRequest("GET", "/api/channels/test-channel/messages", nil)
	req.Header.Set("X-Owner-ID", "1")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("channel messages status = %d, body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)

	_, ok := resp["last_read_message_id"]
	if !ok {
		t.Fatal("response missing last_read_message_id")
	}
}
