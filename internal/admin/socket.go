package admin

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/trace"
)

// Request is the JSON-RPC style request sent over the admin socket.
type Request struct {
	Command string          `json:"command"`
	Args    json.RawMessage `json:"args,omitempty"`
}

// Response is the JSON response returned over the admin socket.
type Response struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

// Start begins listening on the Unix socket. It accepts connections in a loop
// until Stop is called.
func (s *AdminServer) Start() error {
	// Remove stale socket file if it exists.
	os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.socketPath, err)
	}
	s.listener = ln

	// Make the socket accessible by the owner only.
	os.Chmod(s.socketPath, 0o600)

	s.logger.Info("admin socket listening", "path", s.socketPath)

	go s.acceptLoop()
	return nil
}

// Stop closes the listener and removes the socket file.
func (s *AdminServer) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
	close(s.done)
	s.logger.Info("admin socket stopped")
}

func (s *AdminServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if !isClosedError(err) {
				s.logger.Error("accept error", "error", err)
			}
			return
		}
		go s.handleConn(conn)
	}
}

func isClosedError(err error) bool {
	return strings.Contains(err.Error(), "use of closed network connection")
}

func (s *AdminServer) handleConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 10 MB lines for large exports.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(conn, Response{OK: false, Error: "invalid JSON: " + err.Error()})
			continue
		}

		resp := s.dispatch(req)
		s.writeResponse(conn, resp)
	}
}

func (s *AdminServer) writeResponse(w io.Writer, resp Response) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	w.Write(data)
}

// dispatch routes a request to the appropriate handler.
func (s *AdminServer) dispatch(req Request) Response {
	ctx := context.Background()

	switch req.Command {
	// --- user commands ---
	case "user.list":
		return s.handleUserList(ctx)
	case "user.create":
		return s.handleUserCreate(ctx, req.Args)
	case "user.delete":
		return s.handleUserDelete(ctx, req.Args)
	case "user.passwd":
		return s.handleUserPasswd(ctx, req.Args)

	// --- agent commands ---
	case "agent.list":
		return s.handleAgentList(ctx)
	case "agent.create":
		return s.handleAgentCreate(ctx, req.Args)
	case "agent.delete":
		return s.handleAgentDelete(ctx, req.Args)
	case "agent.revoke_key":
		return s.handleAgentRevokeKey(ctx, req.Args)
	case "agent.update_capabilities":
		return s.handleAgentUpdateCapabilities(ctx, req.Args)

	// --- audit commands ---
	case "audit.list":
		return s.handleAuditList(ctx, req.Args)
	case "audit.stats":
		return s.handleAuditStats(ctx)
	case "audit.export":
		return s.handleAuditExport(ctx, req.Args)

	// --- backup ---
	case "backup":
		return s.handleBackup(ctx)

	// --- messages ---
	case "messages.list":
		return s.handleMessagesList(ctx, req.Args)
	case "messages.search":
		return s.handleMessagesSearch(ctx, req.Args)
	case "messages.send":
		return s.handleMessagesSend(ctx, req.Args)

	// --- channels ---
	case "channels.list":
		return s.handleChannelsList(ctx)
	case "channels.show":
		return s.handleChannelsShow(ctx, req.Args)
	case "channels.create":
		return s.handleChannelsCreate(ctx, req.Args)
	case "channels.join":
		return s.handleChannelsJoin(ctx, req.Args)
	case "channels.update_settings":
		return s.handleChannelsUpdateSettings(ctx, req.Args)

	// --- conversations ---
	case "conversations.list":
		return s.handleConversationsList(ctx, req.Args)
	case "conversations.show":
		return s.handleConversationsShow(ctx, req.Args)

	// --- embeddings ---
	case "embeddings.status":
		return s.handleEmbeddingsStatus(ctx)
	case "embeddings.reindex":
		return s.handleEmbeddingsReindex(ctx)
	case "embeddings.clear":
		return s.handleEmbeddingsClear(ctx)

	// --- db maintenance ---
	case "db.vacuum":
		return s.handleDBVacuum(ctx)

	// --- messages purge ---
	case "messages.purge":
		return s.handleMessagesPurge(ctx, req.Args)

	// --- retention ---
	case "retention.status":
		return s.handleRetentionStatus(ctx)

	// --- webhooks ---
	case "webhook.register":
		return s.handleWebhookRegister(ctx, req.Args)
	case "webhook.list":
		return s.handleWebhookList(ctx, req.Args)
	case "webhook.delete":
		return s.handleWebhookDelete(ctx, req.Args)

	// --- k8s ---
	case "k8s.register":
		return s.handleK8sRegister(ctx, req.Args)
	case "k8s.list":
		return s.handleK8sList(ctx, req.Args)
	case "k8s.delete":
		return s.handleK8sDelete(ctx, req.Args)

	// --- attachments ---
	case "attachments.gc":
		return s.handleAttachmentsGC(ctx)

	// --- harness config (subprocess / webhook agent config) ---
	case "harness.config_get":
		return s.handleHarnessConfigGet(ctx, req.Args)
	case "harness.config_set":
		return s.handleHarnessConfigSet(ctx, req.Args)

	// --- memory core (feature 020 — proactive memory) ---
	case "memory.core.get":
		return s.handleMemoryCoreGet(ctx, req.Args)
	case "memory.core.set":
		return s.handleMemoryCoreSet(ctx, req.Args)
	case "memory.core.delete":
		return s.handleMemoryCoreDelete(ctx, req.Args)
	case "memory.dream_run":
		return s.handleMemoryDreamRun(ctx, req.Args)

	default:
		return Response{OK: false, Error: fmt.Sprintf("unknown command: %s", req.Command)}
	}
}

// ---------- user handlers ----------

func (s *AdminServer) handleUserList(ctx context.Context) Response {
	users, err := s.services.Users.ListUsers(ctx)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	type userRow struct {
		ID          int64  `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		CreatedAt   string `json:"created_at"`
	}

	rows := make([]userRow, len(users))
	for i, u := range users {
		rows[i] = userRow{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			CreatedAt:   u.CreatedAt.Format(time.RFC3339),
		}
	}
	return Response{OK: true, Data: rows}
}

func (s *AdminServer) handleUserCreate(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Username == "" || p.Password == "" {
		return Response{OK: false, Error: "username and password are required"}
	}

	user, err := s.services.Users.CreateUser(ctx, p.Username, p.Password, p.DisplayName)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"role":         user.Role,
	}}
}

func (s *AdminServer) handleUserDelete(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Username == "" {
		return Response{OK: false, Error: "username is required"}
	}

	user, err := s.services.Users.GetUserByUsername(ctx, p.Username)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	// Delete all sessions first, then delete the user row.
	if err := s.services.Sessions.DeleteSessionsByUser(ctx, user.ID); err != nil {
		return Response{OK: false, Error: "delete sessions: " + err.Error()}
	}

	_, err = s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", user.ID)
	if err != nil {
		return Response{OK: false, Error: "delete user: " + err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"deleted": p.Username,
	}}
}

func (s *AdminServer) handleUserPasswd(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Username == "" || p.Password == "" {
		return Response{OK: false, Error: "username and password are required"}
	}

	user, err := s.services.Users.GetUserByUsername(ctx, p.Username)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	if err := s.services.Users.UpdatePassword(ctx, user.ID, p.Password); err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"updated": p.Username,
	}}
}

// ---------- agent handlers ----------

func (s *AdminServer) handleAgentList(ctx context.Context) Response {
	// Use the store directly via DiscoverAgents with empty query to get all active agents.
	agentList, err := s.services.Agents.DiscoverAgents(ctx, "")
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	type agentRow struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
		OwnerID     int64  `json:"owner_id"`
		Status      string `json:"status"`
		CreatedAt   string `json:"created_at"`
	}

	rows := make([]agentRow, len(agentList))
	for i, a := range agentList {
		rows[i] = agentRow{
			ID:          a.ID,
			Name:        a.Name,
			DisplayName: a.DisplayName,
			Type:        a.Type,
			OwnerID:     a.OwnerID,
			Status:      a.Status,
			CreatedAt:   a.CreatedAt.Format(time.RFC3339),
		}
	}
	return Response{OK: true, Data: rows}
}

func (s *AdminServer) handleAgentCreate(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name         string          `json:"name"`
		DisplayName  string          `json:"display_name"`
		Type         string          `json:"type"`
		Capabilities json.RawMessage `json:"capabilities"`
		OwnerID      int64           `json:"owner_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}

	agent, apiKey, err := s.services.Agents.Register(ctx, p.Name, p.DisplayName, p.Type, p.Capabilities, p.OwnerID)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"id":      agent.ID,
		"name":    agent.Name,
		"api_key": apiKey,
		"status":  agent.Status,
	}}
}

func (s *AdminServer) handleAgentDelete(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}

	// Admin bypass: deactivate directly via the store.
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET status = 'inactive', updated_at = CURRENT_TIMESTAMP WHERE name = ? AND status = 'active'`,
		p.Name,
	)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"deactivated": p.Name,
	}}
}

func (s *AdminServer) handleAgentRevokeKey(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}

	// Look up the agent to get its owner_id so RevokeKey works.
	agent, err := s.services.Agents.GetAgent(ctx, p.Name)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	_, apiKey, err := s.services.Agents.RevokeKey(ctx, p.Name, agent.OwnerID)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"name":        p.Name,
		"new_api_key": apiKey,
	}}
}

func (s *AdminServer) handleAgentUpdateCapabilities(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name         string          `json:"name"`
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}
	if len(p.Capabilities) == 0 {
		return Response{OK: false, Error: "capabilities is required"}
	}
	if !json.Valid(p.Capabilities) {
		return Response{OK: false, Error: "capabilities must be valid JSON"}
	}

	agent, err := s.services.Agents.UpdateAgent(ctx, p.Name, "", p.Capabilities)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"name":         agent.Name,
		"capabilities": json.RawMessage(agent.Capabilities),
	}}
}

// ---------- audit handlers ----------

func (s *AdminServer) handleAuditList(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		AgentName string `json:"agent_name"`
		Action    string `json:"action"`
		Since     string `json:"since"`
		Limit     int    `json:"limit"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}

	filter := trace.TraceFilter{
		AgentName: p.AgentName,
		Action:    p.Action,
		PageSize:  p.Limit,
		Page:      1,
	}
	if p.Since != "" {
		t, err := time.Parse(time.RFC3339, p.Since)
		if err == nil {
			filter.Since = &t
		}
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}

	traces, total, err := s.services.Traces.Query(ctx, filter)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"traces": traces,
		"total":  total,
	}}
}

func (s *AdminServer) handleAuditStats(ctx context.Context) Response {
	// Count by action for all owners (empty owner_id means all).
	counts, err := s.services.Traces.CountByAction(ctx, "")
	if err != nil {
		// Fallback: try with a simple count query.
		var total int
		s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM traces").Scan(&total)
		return Response{OK: true, Data: map[string]interface{}{
			"total_traces": total,
		}}
	}

	var totalTraces int64
	for _, c := range counts {
		totalTraces += c
	}

	return Response{OK: true, Data: map[string]interface{}{
		"total_traces":     totalTraces,
		"counts_by_action": counts,
	}}
}

func (s *AdminServer) handleAuditExport(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Format string `json:"format"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}
	if p.Format == "" {
		p.Format = "json"
	}

	filter := trace.TraceFilter{
		PageSize: 10000,
		Page:     1,
	}

	traces, _, err := s.services.Traces.Query(ctx, filter)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	if p.Format == "csv" {
		var buf strings.Builder
		w := csv.NewWriter(&buf)
		w.Write([]string{"id", "agent_name", "action", "details", "error", "timestamp"})
		for _, t := range traces {
			w.Write([]string{
				fmt.Sprintf("%d", t.ID),
				t.AgentName,
				t.Action,
				string(t.Details),
				t.Error,
				t.Timestamp.Format(time.RFC3339),
			})
		}
		w.Flush()
		return Response{OK: true, Data: map[string]interface{}{
			"format": "csv",
			"csv":    buf.String(),
		}}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"format": "json",
		"traces": traces,
	}}
}

// ---------- backup handler ----------

func (s *AdminServer) handleBackup(ctx context.Context) Response {
	// Checkpoint WAL first.
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return Response{OK: false, Error: "wal checkpoint: " + err.Error()}
	}

	backupDir := filepath.Join(s.services.DataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return Response{OK: false, Error: "create backup dir: " + err.Error()}
	}

	ts := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("synapbus-%s.db", ts))
	srcPath := filepath.Join(s.services.DataDir, "synapbus.db")

	src, err := os.Open(srcPath)
	if err != nil {
		return Response{OK: false, Error: "open source db: " + err.Error()}
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return Response{OK: false, Error: "create backup file: " + err.Error()}
	}
	defer dst.Close()

	n, err := io.Copy(dst, src)
	if err != nil {
		return Response{OK: false, Error: "copy db: " + err.Error()}
	}

	s.logger.Info("backup created", "path", backupPath, "bytes", n)

	return Response{OK: true, Data: map[string]interface{}{
		"path":  backupPath,
		"bytes": n,
	}}
}

// ---------- messages handlers ----------

func (s *AdminServer) handleMessagesList(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Agent  string `json:"agent"`
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}

	// Query messages directly from DB for admin access (no agent scoping).
	var conditions []string
	var queryArgs []any

	if p.Agent != "" {
		conditions = append(conditions, "(from_agent = ? OR to_agent = ?)")
		queryArgs = append(queryArgs, p.Agent, p.Agent)
	}
	if p.Status != "" {
		conditions = append(conditions, "status = ?")
		queryArgs = append(queryArgs, p.Status)
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT id, conversation_id, from_agent, COALESCE(to_agent, ''), COALESCE(channel_id, 0),
		        body, priority, status, metadata, COALESCE(claimed_by, ''), claimed_at,
		        created_at, updated_at
		 FROM messages%s ORDER BY created_at DESC LIMIT ?`, where)
	queryArgs = append(queryArgs, p.Limit)

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	defer rows.Close()

	type msgRow struct {
		ID             int64  `json:"id"`
		ConversationID int64  `json:"conversation_id"`
		FromAgent      string `json:"from_agent"`
		ToAgent        string `json:"to_agent,omitempty"`
		ChannelID      int64  `json:"channel_id,omitempty"`
		Body           string `json:"body"`
		Priority       int    `json:"priority"`
		Status         string `json:"status"`
		ClaimedBy      string `json:"claimed_by,omitempty"`
		CreatedAt      string `json:"created_at"`
	}

	var result []msgRow
	for rows.Next() {
		var m msgRow
		var metadata, claimedBy string
		var channelID int64
		var claimedAt *time.Time
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.FromAgent, &m.ToAgent, &channelID,
			&m.Body, &m.Priority, &m.Status, &metadata, &claimedBy, &claimedAt,
			&m.CreatedAt, // will be scanned as string below
			new(string),  // updated_at (unused)
		); err != nil {
			return Response{OK: false, Error: "scan: " + err.Error()}
		}
		// Re-scan with proper types
		result = append(result, m)
	}

	// The above scan approach is fragile with time types. Use a simpler direct query.
	return s.handleMessagesListDirect(ctx, p.Agent, p.Status, p.Limit)
}

func (s *AdminServer) handleMessagesListDirect(ctx context.Context, agent, status string, limit int) Response {
	var conditions []string
	var queryArgs []any

	if agent != "" {
		conditions = append(conditions, "(from_agent = ? OR to_agent = ?)")
		queryArgs = append(queryArgs, agent, agent)
	}
	if status != "" {
		conditions = append(conditions, "status = ?")
		queryArgs = append(queryArgs, status)
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT id, conversation_id, from_agent, COALESCE(to_agent, ''), body, priority, status, created_at
		 FROM messages%s ORDER BY created_at DESC LIMIT ?`, where)
	queryArgs = append(queryArgs, limit)

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	defer rows.Close()

	type msgRow struct {
		ID             int64  `json:"id"`
		ConversationID int64  `json:"conversation_id"`
		FromAgent      string `json:"from_agent"`
		ToAgent        string `json:"to_agent,omitempty"`
		Body           string `json:"body"`
		Priority       int    `json:"priority"`
		Status         string `json:"status"`
		CreatedAt      string `json:"created_at"`
	}

	var result []msgRow
	for rows.Next() {
		var m msgRow
		var createdAt time.Time
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.FromAgent, &m.ToAgent,
			&m.Body, &m.Priority, &m.Status, &createdAt); err != nil {
			return Response{OK: false, Error: "scan: " + err.Error()}
		}
		m.CreatedAt = createdAt.Format(time.RFC3339)
		result = append(result, m)
	}
	if result == nil {
		result = []msgRow{}
	}

	return Response{OK: true, Data: result}
}

func (s *AdminServer) handleMessagesSearch(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Query == "" {
		return Response{OK: false, Error: "query is required"}
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}

	// Admin search: use FTS on all messages (no agent scoping).
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.conversation_id, m.from_agent, COALESCE(m.to_agent, ''),
		        m.body, m.priority, m.status, m.created_at
		 FROM messages m
		 JOIN messages_fts ON messages_fts.rowid = m.id
		 WHERE messages_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`, p.Query, p.Limit)
	if err != nil {
		// FTS may not be available; fall back to LIKE search.
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, conversation_id, from_agent, COALESCE(to_agent, ''),
			        body, priority, status, created_at
			 FROM messages
			 WHERE body LIKE ?
			 ORDER BY created_at DESC
			 LIMIT ?`, "%"+p.Query+"%", p.Limit)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
	}
	defer rows.Close()

	type msgRow struct {
		ID             int64  `json:"id"`
		ConversationID int64  `json:"conversation_id"`
		FromAgent      string `json:"from_agent"`
		ToAgent        string `json:"to_agent,omitempty"`
		Body           string `json:"body"`
		Priority       int    `json:"priority"`
		Status         string `json:"status"`
		CreatedAt      string `json:"created_at"`
	}

	var result []msgRow
	for rows.Next() {
		var m msgRow
		var createdAt time.Time
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.FromAgent, &m.ToAgent,
			&m.Body, &m.Priority, &m.Status, &createdAt); err != nil {
			return Response{OK: false, Error: "scan: " + err.Error()}
		}
		m.CreatedAt = createdAt.Format(time.RFC3339)
		result = append(result, m)
	}
	if result == nil {
		result = []msgRow{}
	}

	return Response{OK: true, Data: result}
}

// ---------- channels handlers ----------

func (s *AdminServer) handleChannelsList(ctx context.Context) Response {
	// Admin: list all channels (not scoped to an agent).
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.name, c.description, c.topic, c.type, c.is_private, c.created_by, c.created_at,
		        (SELECT COUNT(*) FROM channel_members cm WHERE cm.channel_id = c.id) as member_count
		 FROM channels c ORDER BY c.name`)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	defer rows.Close()

	type chRow struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Topic       string `json:"topic"`
		Type        string `json:"type"`
		IsPrivate   bool   `json:"is_private"`
		CreatedBy   string `json:"created_by"`
		MemberCount int    `json:"member_count"`
		CreatedAt   string `json:"created_at"`
	}

	var result []chRow
	for rows.Next() {
		var ch chRow
		var isPrivate int
		var createdAt time.Time
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Topic, &ch.Type,
			&isPrivate, &ch.CreatedBy, &createdAt, &ch.MemberCount); err != nil {
			return Response{OK: false, Error: "scan: " + err.Error()}
		}
		ch.IsPrivate = isPrivate != 0
		ch.CreatedAt = createdAt.Format(time.RFC3339)
		result = append(result, ch)
	}
	if result == nil {
		result = []chRow{}
	}

	return Response{OK: true, Data: result}
}

func (s *AdminServer) handleChannelsShow(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}

	ch, err := s.services.Channels.GetChannelByName(ctx, p.Name)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	members, err := s.services.Channels.GetMembers(ctx, ch.ID)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	type memberRow struct {
		AgentName string `json:"agent_name"`
		Role      string `json:"role"`
		JoinedAt  string `json:"joined_at"`
	}

	memberRows := make([]memberRow, len(members))
	for i, m := range members {
		memberRows[i] = memberRow{
			AgentName: m.AgentName,
			Role:      m.Role,
			JoinedAt:  m.JoinedAt.Format(time.RFC3339),
		}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"channel": ch,
		"members": memberRows,
	}}
}

func (s *AdminServer) handleChannelsCreate(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}

	ch, err := s.services.Channels.CreateChannel(ctx, channels.CreateChannelRequest{
		Name:        p.Name,
		Description: p.Description,
		Type:        "standard",
		CreatedBy:   "system",
	})
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"id":          ch.ID,
		"name":        ch.Name,
		"description": ch.Description,
		"type":        ch.Type,
		"is_private":  ch.IsPrivate,
		"created_by":  ch.CreatedBy,
		"created_at":  ch.CreatedAt.Format(time.RFC3339),
	}}
}

func (s *AdminServer) handleChannelsJoin(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Channel string `json:"channel"`
		Agent   string `json:"agent"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Channel == "" {
		return Response{OK: false, Error: "channel is required"}
	}
	if p.Agent == "" {
		return Response{OK: false, Error: "agent is required"}
	}

	ch, err := s.services.Channels.GetChannelByName(ctx, p.Channel)
	if err != nil {
		return Response{OK: false, Error: fmt.Sprintf("channel not found: %s", p.Channel)}
	}

	// Check if already a member for status reporting
	isMember, _ := s.services.Channels.IsMember(ctx, ch.ID, p.Agent)

	if err := s.services.Channels.JoinChannel(ctx, ch.ID, p.Agent); err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	status := "joined"
	if isMember {
		status = "already_member"
	}

	return Response{OK: true, Data: map[string]interface{}{
		"channel": p.Channel,
		"agent":   p.Agent,
		"status":  status,
	}}
}

func (s *AdminServer) handleChannelsUpdateSettings(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Name                   string `json:"name"`
		AutoApprove            *bool  `json:"auto_approve,omitempty"`
		StalemateRemindAfter   string `json:"stalemate_remind_after,omitempty"`
		StalemateEscalateAfter string `json:"stalemate_escalate_after,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Name == "" {
		return Response{OK: false, Error: "name is required"}
	}

	// Build the SET clause dynamically based on provided fields
	var setClauses []string
	var setArgs []interface{}

	if p.AutoApprove != nil {
		autoApproveVal := 0
		if *p.AutoApprove {
			autoApproveVal = 1
		}
		setClauses = append(setClauses, "auto_approve = ?")
		setArgs = append(setArgs, autoApproveVal)
	}
	if p.StalemateRemindAfter != "" {
		setClauses = append(setClauses, "stalemate_remind_after = ?")
		setArgs = append(setArgs, p.StalemateRemindAfter)
	}
	if p.StalemateEscalateAfter != "" {
		setClauses = append(setClauses, "stalemate_escalate_after = ?")
		setArgs = append(setArgs, p.StalemateEscalateAfter)
	}

	if len(setClauses) == 0 {
		return Response{OK: false, Error: "at least one setting must be provided (auto_approve, stalemate_remind_after, stalemate_escalate_after)"}
	}

	query := fmt.Sprintf("UPDATE channels SET %s WHERE LOWER(name) = LOWER(?)", strings.Join(setClauses, ", "))
	setArgs = append(setArgs, p.Name)

	result, err := s.db.ExecContext(ctx, query, setArgs...)
	if err != nil {
		return Response{OK: false, Error: "update channel settings: " + err.Error()}
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return Response{OK: false, Error: fmt.Sprintf("channel not found: %s", p.Name)}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"channel": p.Name,
		"updated": true,
	}}
}

// ---------- conversations handlers ----------

func (s *AdminServer) handleConversationsList(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Limit int `json:"limit"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.subject, c.created_by, c.created_at,
		        (SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.id) as msg_count
		 FROM conversations c
		 ORDER BY c.updated_at DESC
		 LIMIT ?`, p.Limit)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	defer rows.Close()

	type convRow struct {
		ID        int64  `json:"id"`
		Subject   string `json:"subject"`
		CreatedBy string `json:"created_by"`
		CreatedAt string `json:"created_at"`
		MsgCount  int    `json:"message_count"`
	}

	var result []convRow
	for rows.Next() {
		var c convRow
		var createdAt time.Time
		if err := rows.Scan(&c.ID, &c.Subject, &c.CreatedBy, &createdAt, &c.MsgCount); err != nil {
			return Response{OK: false, Error: "scan: " + err.Error()}
		}
		c.CreatedAt = createdAt.Format(time.RFC3339)
		result = append(result, c)
	}
	if result == nil {
		result = []convRow{}
	}

	return Response{OK: true, Data: result}
}

func (s *AdminServer) handleConversationsShow(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.ID <= 0 {
		return Response{OK: false, Error: "id is required"}
	}

	conv, messages, err := s.services.Messages.GetConversation(ctx, p.ID)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	// Strip metadata from messages to keep output clean; convert to simple form.
	type simplMsg struct {
		ID        int64  `json:"id"`
		From      string `json:"from"`
		To        string `json:"to,omitempty"`
		Body      string `json:"body"`
		Status    string `json:"status"`
		Priority  int    `json:"priority"`
		CreatedAt string `json:"created_at"`
	}

	msgs := make([]simplMsg, len(messages))
	for i, m := range messages {
		msgs[i] = simplMsg{
			ID:        m.ID,
			From:      m.FromAgent,
			To:        m.ToAgent,
			Body:      m.Body,
			Status:    m.Status,
			Priority:  m.Priority,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"conversation": conv,
		"messages":     msgs,
	}}
}

// ---------- embeddings handlers ----------

func (s *AdminServer) handleEmbeddingsStatus(ctx context.Context) Response {
	result := map[string]interface{}{
		"provider":       "",
		"total_embedded": int64(0),
		"pending_count":  int64(0),
		"failed_count":   int64(0),
		"index_size":     0,
		"dimensions":     0,
	}

	if s.services.EmbeddingStore == nil {
		return Response{OK: true, Data: result}
	}

	stats, err := s.services.EmbeddingStore.Stats(ctx)
	if err != nil {
		return Response{OK: false, Error: "get stats: " + err.Error()}
	}

	result["provider"] = stats.Provider
	result["total_embedded"] = stats.TotalEmbedded
	result["pending_count"] = stats.PendingCount
	result["failed_count"] = stats.FailedCount
	result["dimensions"] = stats.Dimensions

	if s.services.VectorIndex != nil {
		result["index_size"] = s.services.VectorIndex.Len()
	}

	return Response{OK: true, Data: result}
}

func (s *AdminServer) handleEmbeddingsReindex(ctx context.Context) Response {
	if s.services.EmbeddingStore == nil {
		return Response{OK: false, Error: "embedding subsystem not configured"}
	}

	if err := s.services.EmbeddingStore.DeleteAllEmbeddings(ctx); err != nil {
		return Response{OK: false, Error: "delete embeddings: " + err.Error()}
	}

	if err := s.services.EmbeddingStore.ClearQueue(ctx); err != nil {
		return Response{OK: false, Error: "clear queue: " + err.Error()}
	}

	clearedIndex := false
	if s.services.VectorIndex != nil {
		if err := s.services.VectorIndex.Rebuild(nil); err != nil {
			return Response{OK: false, Error: "clear index: " + err.Error()}
		}
		clearedIndex = true
	}

	enqueued, err := s.services.EmbeddingStore.EnqueueAllMessages(ctx)
	if err != nil {
		return Response{OK: false, Error: "enqueue messages: " + err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"deleted_embeddings": true,
		"cleared_index":      clearedIndex,
		"enqueued_messages":  enqueued,
	}}
}

func (s *AdminServer) handleEmbeddingsClear(ctx context.Context) Response {
	if s.services.EmbeddingStore == nil {
		return Response{OK: false, Error: "embedding subsystem not configured"}
	}

	if err := s.services.EmbeddingStore.DeleteAllEmbeddings(ctx); err != nil {
		return Response{OK: false, Error: "delete embeddings: " + err.Error()}
	}

	if err := s.services.EmbeddingStore.ClearQueue(ctx); err != nil {
		return Response{OK: false, Error: "clear queue: " + err.Error()}
	}

	clearedIndex := false
	if s.services.VectorIndex != nil {
		if err := s.services.VectorIndex.Rebuild(nil); err != nil {
			return Response{OK: false, Error: "clear index: " + err.Error()}
		}
		clearedIndex = true
	}

	return Response{OK: true, Data: map[string]interface{}{
		"deleted_embeddings": true,
		"cleared_index":      clearedIndex,
		"cleared_queue":      true,
	}}
}

// ---------- db maintenance handlers ----------

func (s *AdminServer) handleDBVacuum(ctx context.Context) Response {
	dbPath := filepath.Join(s.services.DataDir, "synapbus.db")

	beforeInfo, err := os.Stat(dbPath)
	if err != nil {
		return Response{OK: false, Error: "stat db: " + err.Error()}
	}
	beforeSize := beforeInfo.Size()

	start := time.Now()

	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return Response{OK: false, Error: "wal checkpoint: " + err.Error()}
	}

	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return Response{OK: false, Error: "vacuum: " + err.Error()}
	}

	durationMs := time.Since(start).Milliseconds()

	afterInfo, err := os.Stat(dbPath)
	if err != nil {
		return Response{OK: false, Error: "stat db after vacuum: " + err.Error()}
	}
	afterSize := afterInfo.Size()

	return Response{OK: true, Data: map[string]interface{}{
		"before_size_bytes": beforeSize,
		"after_size_bytes":  afterSize,
		"reclaimed_bytes":   beforeSize - afterSize,
		"duration_ms":       durationMs,
	}}
}

// ---------- messages purge handler ----------

func (s *AdminServer) handleMessagesPurge(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		OlderThan string `json:"older_than"`
		Agent     string `json:"agent"`
		Channel   string `json:"channel"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}

	if p.OlderThan == "" && p.Agent == "" && p.Channel == "" {
		return Response{OK: false, Error: "at least one filter is required (older_than, agent, or channel)"}
	}

	var olderThan time.Duration
	if p.OlderThan != "" {
		cfg := messaging.ParseRetentionPeriod(p.OlderThan)
		if cfg.RetentionPeriod <= 0 {
			return Response{OK: false, Error: fmt.Sprintf("invalid duration: %q", p.OlderThan)}
		}
		olderThan = cfg.RetentionPeriod
	}

	counts, err := messaging.PurgeMessages(ctx, s.db, s.services.DataDir, olderThan, p.Agent, p.Channel)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: counts}
}

// ---------- retention handler ----------

func (s *AdminServer) handleRetentionStatus(ctx context.Context) Response {
	if s.services.RetentionWorker != nil {
		return Response{OK: true, Data: s.services.RetentionWorker.Status()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"enabled": false,
		"message": "retention worker not configured",
	}}
}

// ---------- webhook handlers ----------

func (s *AdminServer) handleWebhookRegister(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		URL       string `json:"url"`
		Events    string `json:"events"`
		Secret    string `json:"secret"`
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.URL == "" || p.Events == "" || p.Secret == "" || p.AgentName == "" {
		return Response{OK: false, Error: "url, events, secret, and agent_name are required"}
	}

	if s.services.WebhookService == nil {
		return Response{OK: false, Error: "webhook service not configured"}
	}

	events := strings.Split(p.Events, ",")
	for i := range events {
		events[i] = strings.TrimSpace(events[i])
	}

	wh, err := s.services.WebhookService.RegisterWebhook(ctx, p.AgentName, p.URL, events, p.Secret)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"id":     wh.ID,
		"url":    wh.URL,
		"events": wh.Events,
		"status": wh.Status,
	}}
}

func (s *AdminServer) handleWebhookList(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		AgentName string `json:"agent_name"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}

	if s.services.WebhookService == nil {
		return Response{OK: false, Error: "webhook service not configured"}
	}

	if p.AgentName == "" {
		// Admin: list all webhooks by querying DB directly.
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, agent_name, url, events, status, consecutive_failures, created_at
			 FROM webhooks ORDER BY created_at DESC`)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		defer rows.Close()

		type whRow struct {
			ID                  int64    `json:"id"`
			AgentName           string   `json:"agent_name"`
			URL                 string   `json:"url"`
			Events              []string `json:"events"`
			Status              string   `json:"status"`
			ConsecutiveFailures int      `json:"consecutive_failures"`
			CreatedAt           string   `json:"created_at"`
		}

		var result []whRow
		for rows.Next() {
			var r whRow
			var eventsJSON string
			var createdAt time.Time
			if err := rows.Scan(&r.ID, &r.AgentName, &r.URL, &eventsJSON, &r.Status, &r.ConsecutiveFailures, &createdAt); err != nil {
				return Response{OK: false, Error: "scan: " + err.Error()}
			}
			json.Unmarshal([]byte(eventsJSON), &r.Events)
			if r.Events == nil {
				r.Events = []string{}
			}
			r.CreatedAt = createdAt.Format(time.RFC3339)
			result = append(result, r)
		}
		if result == nil {
			result = []whRow{}
		}
		return Response{OK: true, Data: result}
	}

	webhookList, err := s.services.WebhookService.ListWebhooks(ctx, p.AgentName)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	type whRow struct {
		ID                  int64    `json:"id"`
		AgentName           string   `json:"agent_name"`
		URL                 string   `json:"url"`
		Events              []string `json:"events"`
		Status              string   `json:"status"`
		ConsecutiveFailures int      `json:"consecutive_failures"`
		CreatedAt           string   `json:"created_at"`
	}

	result := make([]whRow, len(webhookList))
	for i, wh := range webhookList {
		result[i] = whRow{
			ID:                  wh.ID,
			AgentName:           wh.AgentName,
			URL:                 wh.URL,
			Events:              wh.Events,
			Status:              wh.Status,
			ConsecutiveFailures: wh.ConsecutiveFailures,
			CreatedAt:           wh.CreatedAt.Format(time.RFC3339),
		}
	}
	return Response{OK: true, Data: result}
}

func (s *AdminServer) handleWebhookDelete(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.ID <= 0 {
		return Response{OK: false, Error: "id is required"}
	}

	if s.services.WebhookService == nil {
		return Response{OK: false, Error: "webhook service not configured"}
	}

	// Admin bypass: look up the webhook's agent_name first, then delete.
	var agentName string
	err := s.db.QueryRowContext(ctx, "SELECT agent_name FROM webhooks WHERE id = ?", p.ID).Scan(&agentName)
	if err != nil {
		return Response{OK: false, Error: "webhook not found"}
	}

	if err := s.services.WebhookService.DeleteWebhook(ctx, agentName, p.ID); err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"deleted": p.ID,
	}}
}

// ---------- k8s handlers ----------

func (s *AdminServer) handleK8sRegister(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Image           string `json:"image"`
		Events          string `json:"events"`
		AgentName       string `json:"agent_name"`
		Namespace       string `json:"namespace"`
		ResourcesMemory string `json:"resources_memory"`
		ResourcesCPU    string `json:"resources_cpu"`
		Env             string `json:"env"`
		TimeoutSeconds  int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Image == "" || p.Events == "" || p.AgentName == "" {
		return Response{OK: false, Error: "image, events, and agent_name are required"}
	}

	if s.services.K8sService == nil {
		return Response{OK: false, Error: "k8s service not configured"}
	}

	events := strings.Split(p.Events, ",")
	for i := range events {
		events[i] = strings.TrimSpace(events[i])
	}

	// Parse env from comma-separated KEY=VALUE pairs.
	envMap := map[string]string{}
	if p.Env != "" {
		for _, pair := range strings.Split(p.Env, ",") {
			pair = strings.TrimSpace(pair)
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
	}

	timeout := p.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}

	req := k8s.RegisterHandlerRequest{
		Image:           p.Image,
		Events:          events,
		Namespace:       p.Namespace,
		ResourcesMemory: p.ResourcesMemory,
		ResourcesCPU:    p.ResourcesCPU,
		Env:             envMap,
		TimeoutSeconds:  timeout,
	}

	handler, err := s.services.K8sService.RegisterHandler(ctx, p.AgentName, req)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"id":     handler.ID,
		"image":  handler.Image,
		"events": handler.Events,
		"status": handler.Status,
	}}
}

func (s *AdminServer) handleK8sList(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		AgentName string `json:"agent_name"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}

	if s.services.K8sService == nil {
		return Response{OK: false, Error: "k8s service not configured"}
	}

	if p.AgentName == "" {
		// Admin: list all K8s handlers by querying DB directly.
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, agent_name, image, events, namespace, resources_memory, resources_cpu, timeout_seconds, status, created_at
			 FROM k8s_handlers ORDER BY created_at DESC`)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		defer rows.Close()

		type handlerRow struct {
			ID              int64    `json:"id"`
			AgentName       string   `json:"agent_name"`
			Image           string   `json:"image"`
			Events          []string `json:"events"`
			Namespace       string   `json:"namespace"`
			ResourcesMemory string   `json:"resources_memory"`
			ResourcesCPU    string   `json:"resources_cpu"`
			TimeoutSeconds  int      `json:"timeout_seconds"`
			Status          string   `json:"status"`
			CreatedAt       string   `json:"created_at"`
		}

		var result []handlerRow
		for rows.Next() {
			var r handlerRow
			var eventsJSON string
			var createdAt time.Time
			if err := rows.Scan(&r.ID, &r.AgentName, &r.Image, &eventsJSON, &r.Namespace,
				&r.ResourcesMemory, &r.ResourcesCPU, &r.TimeoutSeconds, &r.Status, &createdAt); err != nil {
				return Response{OK: false, Error: "scan: " + err.Error()}
			}
			json.Unmarshal([]byte(eventsJSON), &r.Events)
			if r.Events == nil {
				r.Events = []string{}
			}
			r.CreatedAt = createdAt.Format(time.RFC3339)
			result = append(result, r)
		}
		if result == nil {
			result = []handlerRow{}
		}
		return Response{OK: true, Data: result}
	}

	handlers, err := s.services.K8sService.ListHandlers(ctx, p.AgentName)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	type handlerRow struct {
		ID              int64    `json:"id"`
		AgentName       string   `json:"agent_name"`
		Image           string   `json:"image"`
		Events          []string `json:"events"`
		Namespace       string   `json:"namespace"`
		ResourcesMemory string   `json:"resources_memory"`
		ResourcesCPU    string   `json:"resources_cpu"`
		TimeoutSeconds  int      `json:"timeout_seconds"`
		Status          string   `json:"status"`
		CreatedAt       string   `json:"created_at"`
	}

	result := make([]handlerRow, len(handlers))
	for i, h := range handlers {
		result[i] = handlerRow{
			ID:              h.ID,
			AgentName:       h.AgentName,
			Image:           h.Image,
			Events:          h.Events,
			Namespace:       h.Namespace,
			ResourcesMemory: h.ResourcesMemory,
			ResourcesCPU:    h.ResourcesCPU,
			TimeoutSeconds:  h.TimeoutSeconds,
			Status:          h.Status,
			CreatedAt:       h.CreatedAt.Format(time.RFC3339),
		}
	}
	return Response{OK: true, Data: result}
}

func (s *AdminServer) handleK8sDelete(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.ID <= 0 {
		return Response{OK: false, Error: "id is required"}
	}

	if s.services.K8sService == nil {
		return Response{OK: false, Error: "k8s service not configured"}
	}

	// Admin bypass: look up the handler's agent_name first, then delete.
	var agentName string
	err := s.db.QueryRowContext(ctx, "SELECT agent_name FROM k8s_handlers WHERE id = ?", p.ID).Scan(&agentName)
	if err != nil {
		return Response{OK: false, Error: "handler not found"}
	}

	if err := s.services.K8sService.DeleteHandler(ctx, agentName, p.ID); err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"deleted": p.ID,
	}}
}

// ---------- attachments handlers ----------

func (s *AdminServer) handleAttachmentsGC(ctx context.Context) Response {
	if s.services.AttachmentService == nil {
		return Response{OK: false, Error: "attachment service not configured"}
	}

	result, err := s.services.AttachmentService.GarbageCollect(ctx)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	return Response{OK: true, Data: map[string]interface{}{
		"files_removed":   result.FilesRemoved,
		"bytes_reclaimed": result.BytesReclaimed,
	}}
}

// ---------- messages.send handler ----------
//
// This command lets the admin socket send a message as any agent. It
// bypasses the regular auth/ownership checks because the socket is
// already admin-privileged (local Unix socket, owned by the synapbus
// process). Used by the harness shell wrappers (see
// examples/cold-topic-explainer/wrapper.sh) so Gemini agents can DM
// each other without implementing the full MCP handshake.

func (s *AdminServer) handleMessagesSend(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Body      string `json:"body"`
		Subject   string `json:"subject,omitempty"`
		Priority  int    `json:"priority,omitempty"`
		ChannelID int64  `json:"channel_id,omitempty"`
		ReplyTo   int64  `json:"reply_to,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.From == "" {
		return Response{OK: false, Error: "from is required"}
	}
	if p.Body == "" {
		return Response{OK: false, Error: "body is required"}
	}
	if p.To == "" && p.ChannelID == 0 {
		return Response{OK: false, Error: "one of to or channel_id is required"}
	}
	if s.services.Messages == nil {
		return Response{OK: false, Error: "messaging service not configured"}
	}
	opts := messaging.SendOptions{
		Subject:  p.Subject,
		Priority: p.Priority,
	}
	if p.ChannelID > 0 {
		id := p.ChannelID
		opts.ChannelID = &id
	}
	if p.ReplyTo > 0 {
		id := p.ReplyTo
		opts.ReplyTo = &id
	}
	msg, err := s.services.Messages.SendMessage(ctx, p.From, p.To, p.Body, opts)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: map[string]any{
		"message_id":      msg.ID,
		"conversation_id": msg.ConversationID,
		"status":          msg.Status,
		"from":            msg.FromAgent,
		"to":              msg.ToAgent,
	}}
}

// ---------- harness config handlers ----------

func (s *AdminServer) handleHarnessConfigGet(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.AgentName == "" {
		return Response{OK: false, Error: "agent_name is required"}
	}
	agent, err := s.services.Agents.GetAgent(ctx, p.AgentName)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	// Parse harness_config_json if present so the caller gets
	// structured output instead of an opaque string. Tolerate empty.
	var parsed any
	if agent.HarnessConfigJSON != "" {
		if err := json.Unmarshal([]byte(agent.HarnessConfigJSON), &parsed); err != nil {
			// Return raw + parse error, not a hard failure — callers
			// might legitimately want to see broken config to fix it.
			return Response{OK: true, Data: map[string]any{
				"agent_name":          agent.Name,
				"harness_name":        agent.HarnessName,
				"local_command":       agent.LocalCommand,
				"harness_config_json": agent.HarnessConfigJSON,
				"parse_error":         err.Error(),
			}}
		}
	}
	return Response{OK: true, Data: map[string]any{
		"agent_name":          agent.Name,
		"harness_name":        agent.HarnessName,
		"local_command":       agent.LocalCommand,
		"harness_config_json": agent.HarnessConfigJSON,
		"harness_config":      parsed,
	}}
}

func (s *AdminServer) handleHarnessConfigSet(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		AgentName         string          `json:"agent_name"`
		HarnessName       string          `json:"harness_name"`
		LocalCommand      string          `json:"local_command"`
		HarnessConfigJSON json.RawMessage `json:"harness_config_json"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.AgentName == "" {
		return Response{OK: false, Error: "agent_name is required"}
	}

	// Validate harness_config_json shape when provided. An empty object
	// ({}) and null are both valid (clear with "-"); otherwise it must
	// parse as JSON.
	configJSON := ""
	if len(p.HarnessConfigJSON) > 0 {
		raw := strings.TrimSpace(string(p.HarnessConfigJSON))
		switch raw {
		case "", "null":
			configJSON = "-"
		case `"-"`:
			configJSON = "-"
		default:
			if !json.Valid(p.HarnessConfigJSON) {
				return Response{OK: false, Error: "harness_config_json is not valid JSON"}
			}
			configJSON = string(p.HarnessConfigJSON)
		}
	}

	// Delegate to the store. Empty strings mean "leave unchanged",
	// "-" means "clear".
	if err := s.services.Agents.Store().UpdateHarnessConfig(ctx, p.AgentName, p.HarnessName, p.LocalCommand, configJSON); err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	agent, err := s.services.Agents.GetAgent(ctx, p.AgentName)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: map[string]any{
		"agent_name":          agent.Name,
		"harness_name":        agent.HarnessName,
		"local_command":       agent.LocalCommand,
		"harness_config_json": agent.HarnessConfigJSON,
	}}
}

// ---------- memory core handlers (feature 020) ----------
//
// owner_id wire format: callers pass the owner as a username; we resolve
// to `users.id` and pass the string form to CoreMemoryStore so it matches
// the proactive-memory tables' TEXT owner_id convention.

func (s *AdminServer) resolveOwnerString(ctx context.Context, ownerInput string) (string, error) {
	if ownerInput == "" {
		return "", fmt.Errorf("owner is required")
	}
	// First try numeric — admins may already know the user ID.
	var id int64
	if _, err := fmt.Sscanf(ownerInput, "%d", &id); err == nil && id > 0 {
		return fmt.Sprintf("%d", id), nil
	}
	// Otherwise treat as username.
	user, err := s.services.Users.GetUserByUsername(ctx, ownerInput)
	if err != nil {
		return "", fmt.Errorf("resolve owner %q: %w", ownerInput, err)
	}
	return fmt.Sprintf("%d", user.ID), nil
}

func (s *AdminServer) handleMemoryCoreGet(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Owner string `json:"owner"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Agent == "" {
		return Response{OK: false, Error: "agent is required"}
	}
	if s.services.CoreMemoryStore == nil {
		return Response{OK: false, Error: "core memory store not configured"}
	}
	ownerStr, err := s.resolveOwnerString(ctx, p.Owner)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	blob, updatedAt, ok, err := s.services.CoreMemoryStore.Get(ctx, ownerStr, p.Agent)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	if !ok {
		return Response{OK: true, Data: map[string]any{
			"owner_id":   ownerStr,
			"agent_name": p.Agent,
			"exists":     false,
		}}
	}
	return Response{OK: true, Data: map[string]any{
		"owner_id":   ownerStr,
		"agent_name": p.Agent,
		"exists":     true,
		"blob":       blob,
		"updated_at": updatedAt.Format(time.RFC3339),
	}}
}

func (s *AdminServer) handleMemoryCoreSet(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Owner     string `json:"owner"`
		Agent     string `json:"agent"`
		Blob      string `json:"blob"`
		UpdatedBy string `json:"updated_by"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Agent == "" {
		return Response{OK: false, Error: "agent is required"}
	}
	if s.services.CoreMemoryStore == nil {
		return Response{OK: false, Error: "core memory store not configured"}
	}
	ownerStr, err := s.resolveOwnerString(ctx, p.Owner)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	updatedBy := p.UpdatedBy
	if updatedBy == "" {
		updatedBy = "human"
	}
	if err := s.services.CoreMemoryStore.Set(ctx, ownerStr, p.Agent, p.Blob, updatedBy); err != nil {
		if err == messaging.ErrCoreMemoryTooLarge {
			return Response{OK: false, Error: fmt.Sprintf("core_memory_too_large: blob exceeds %d bytes", s.services.CoreMemoryStore.MaxBytes())}
		}
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: map[string]any{
		"owner_id":   ownerStr,
		"agent_name": p.Agent,
		"blob_chars": len(p.Blob),
		"updated_by": updatedBy,
	}}
}

func (s *AdminServer) handleMemoryCoreDelete(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Owner string `json:"owner"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.Agent == "" {
		return Response{OK: false, Error: "agent is required"}
	}
	if s.services.CoreMemoryStore == nil {
		return Response{OK: false, Error: "core memory store not configured"}
	}
	ownerStr, err := s.resolveOwnerString(ctx, p.Owner)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	if err := s.services.CoreMemoryStore.Delete(ctx, ownerStr, p.Agent); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: map[string]any{
		"owner_id":   ownerStr,
		"agent_name": p.Agent,
		"deleted":    true,
	}}
}

// handleMemoryDreamRun forces a single dream-job dispatch. Bypasses
// trigger checks — useful for kubic verification (quickstart §"verify
// dream agent"). Returns the created job_id.
func (s *AdminServer) handleMemoryDreamRun(ctx context.Context, args json.RawMessage) Response {
	var p struct {
		Owner   string `json:"owner"`
		JobType string `json:"job_type"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return Response{OK: false, Error: "invalid args: " + err.Error()}
	}
	if p.JobType == "" {
		return Response{OK: false, Error: "job_type is required"}
	}
	if s.services.DreamRun == nil {
		return Response{OK: false, Error: "dream worker not configured (SYNAPBUS_DREAM_ENABLED=0?)"}
	}
	ownerStr, err := s.resolveOwnerString(ctx, p.Owner)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	jobID, err := s.services.DreamRun(ctx, ownerStr, p.JobType)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: map[string]any{
		"job_id":   jobID,
		"owner_id": ownerStr,
		"job_type": p.JobType,
	}}
}

// Ensure the messaging import is used.
var _ = messaging.StatusPending
