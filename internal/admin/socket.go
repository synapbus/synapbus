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

	// --- channels ---
	case "channels.list":
		return s.handleChannelsList(ctx)
	case "channels.show":
		return s.handleChannelsShow(ctx, req.Args)

	// --- conversations ---
	case "conversations.list":
		return s.handleConversationsList(ctx, req.Args)
	case "conversations.show":
		return s.handleConversationsShow(ctx, req.Args)

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
		"total_traces":    totalTraces,
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

// Ensure the messaging import is used.
var _ = messaging.StatusPending
