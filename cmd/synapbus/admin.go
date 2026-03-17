package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var adminSocket string

// adminRequest sends a command over the Unix socket and returns the parsed response.
func adminRequest(command string, args interface{}) (map[string]interface{}, error) {
	socket := adminSocket
	if s := os.Getenv("SYNAPBUS_SOCKET"); s != "" && socket == "/tmp/synapbus.sock" {
		socket = s
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w (is synapbus serve running?)", socket, err)
	}
	defer conn.Close()

	var argsRaw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("marshal args: %w", err)
		}
		argsRaw = b
	}

	req := struct {
		Command string          `json:"command"`
		Args    json.RawMessage `json:"args,omitempty"`
	}{
		Command: command,
		Args:    argsRaw,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("empty response from server")
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if ok, _ := resp["ok"].(bool); !ok {
		errMsg, _ := resp["error"].(string)
		return nil, fmt.Errorf("error: %s", errMsg)
	}

	return resp, nil
}

// printTable prints a slice of maps as a table.
func printTable(headers []string, rows []map[string]string) {
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Repeat("-\t", len(headers)))
	for _, row := range rows {
		vals := make([]string, len(headers))
		for i, h := range headers {
			vals[i] = row[h]
		}
		fmt.Fprintln(w, strings.Join(vals, "\t"))
	}
	w.Flush()
}

// printJSON pretty-prints a value as JSON.
func printJSON(v interface{}) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

// toMapSlice converts response data ([]interface{}) to []map[string]string.
func toMapSlice(data interface{}) []map[string]string {
	arr, ok := data.([]interface{})
	if !ok {
		return nil
	}
	var result []map[string]string
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		row := make(map[string]string)
		for k, v := range m {
			row[k] = fmt.Sprintf("%v", v)
		}
		result = append(result, row)
	}
	return result
}

func addAdminCommands(rootCmd *cobra.Command) {
	// ----- user commands -----
	userCmd := &cobra.Command{
		Use:   "user",
		Short: "Manage users",
	}

	userListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all users",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("user.list", nil)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No users found.")
				return nil
			}
			printTable([]string{"ID", "USERNAME", "DISPLAY_NAME", "ROLE", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "USERNAME": "username", "DISPLAY_NAME": "display_name", "ROLE": "role", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}

	var (
		userCreateUsername    string
		userCreatePassword    string
		userCreateDisplayName string
	)
	userCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("user.create", map[string]string{
				"username":     userCreateUsername,
				"password":     userCreatePassword,
				"display_name": userCreateDisplayName,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	userCreateCmd.Flags().StringVar(&userCreateUsername, "username", "", "Username")
	userCreateCmd.Flags().StringVar(&userCreatePassword, "password", "", "Password")
	userCreateCmd.Flags().StringVar(&userCreateDisplayName, "display-name", "", "Display name")
	userCreateCmd.MarkFlagRequired("username")
	userCreateCmd.MarkFlagRequired("password")

	var userDeleteUsername string
	userDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("user.delete", map[string]string{
				"username": userDeleteUsername,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	userDeleteCmd.Flags().StringVar(&userDeleteUsername, "username", "", "Username to delete")
	userDeleteCmd.MarkFlagRequired("username")

	var (
		userPasswdUsername string
		userPasswdPassword string
	)
	userPasswdCmd := &cobra.Command{
		Use:   "passwd",
		Short: "Change a user's password",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("user.passwd", map[string]string{
				"username": userPasswdUsername,
				"password": userPasswdPassword,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	userPasswdCmd.Flags().StringVar(&userPasswdUsername, "username", "", "Username")
	userPasswdCmd.Flags().StringVar(&userPasswdPassword, "password", "", "New password")
	userPasswdCmd.MarkFlagRequired("username")
	userPasswdCmd.MarkFlagRequired("password")

	userCmd.AddCommand(userListCmd, userCreateCmd, userDeleteCmd, userPasswdCmd)

	// ----- agent commands -----
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}

	agentListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("agent.list", nil)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No agents found.")
				return nil
			}
			printTable([]string{"ID", "NAME", "DISPLAY_NAME", "TYPE", "OWNER_ID", "STATUS", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "NAME": "name", "DISPLAY_NAME": "display_name", "TYPE": "type",
				"OWNER_ID": "owner_id", "STATUS": "status", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}

	var (
		agentCreateName        string
		agentCreateDisplayName string
		agentCreateType        string
		agentCreateOwner       int64
	)
	agentCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Register a new agent and return its API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("agent.create", map[string]interface{}{
				"name":         agentCreateName,
				"display_name": agentCreateDisplayName,
				"type":         agentCreateType,
				"owner_id":     agentCreateOwner,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	agentCreateCmd.Flags().StringVar(&agentCreateName, "name", "", "Agent name")
	agentCreateCmd.Flags().StringVar(&agentCreateDisplayName, "display-name", "", "Display name")
	agentCreateCmd.Flags().StringVar(&agentCreateType, "type", "ai", "Agent type (ai|human)")
	agentCreateCmd.Flags().Int64Var(&agentCreateOwner, "owner", 0, "Owner user ID")
	agentCreateCmd.MarkFlagRequired("name")

	var agentDeleteName string
	agentDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Deactivate an agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("agent.delete", map[string]string{
				"name": agentDeleteName,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	agentDeleteCmd.Flags().StringVar(&agentDeleteName, "name", "", "Agent name")
	agentDeleteCmd.MarkFlagRequired("name")

	var agentRevokeKeyName string
	agentRevokeKeyCmd := &cobra.Command{
		Use:   "revoke-key",
		Short: "Regenerate an agent's API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("agent.revoke_key", map[string]string{
				"name": agentRevokeKeyName,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	agentRevokeKeyCmd.Flags().StringVar(&agentRevokeKeyName, "name", "", "Agent name")
	agentRevokeKeyCmd.MarkFlagRequired("name")

	var (
		agentUpdateCapsName string
		agentUpdateCapsJSON string
	)
	agentUpdateCapsCmd := &cobra.Command{
		Use:   "update-capabilities",
		Short: "Update an agent's capabilities JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("agent.update_capabilities", map[string]interface{}{
				"name":         agentUpdateCapsName,
				"capabilities": json.RawMessage(agentUpdateCapsJSON),
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	agentUpdateCapsCmd.Flags().StringVar(&agentUpdateCapsName, "name", "", "Agent name")
	agentUpdateCapsCmd.Flags().StringVar(&agentUpdateCapsJSON, "capabilities", "", "Capabilities JSON (e.g. '{\"role\":\"researcher\"}')")
	agentUpdateCapsCmd.MarkFlagRequired("name")
	agentUpdateCapsCmd.MarkFlagRequired("capabilities")

	agentCmd.AddCommand(agentListCmd, agentCreateCmd, agentDeleteCmd, agentRevokeKeyCmd, agentUpdateCapsCmd)

	// ----- audit commands -----
	auditCmd := &cobra.Command{
		Use:   "audit",
		Short: "Query audit/trace logs",
	}

	var (
		auditListAgent  string
		auditListAction string
		auditListSince  string
		auditListLimit  int
	)
	auditListCmd := &cobra.Command{
		Use:   "list",
		Short: "List audit traces",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{}
			if auditListAgent != "" {
				reqArgs["agent_name"] = auditListAgent
			}
			if auditListAction != "" {
				reqArgs["action"] = auditListAction
			}
			if auditListSince != "" {
				reqArgs["since"] = auditListSince
			}
			if auditListLimit > 0 {
				reqArgs["limit"] = auditListLimit
			}
			resp, err := adminRequest("audit.list", reqArgs)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	auditListCmd.Flags().StringVar(&auditListAgent, "agent", "", "Filter by agent name")
	auditListCmd.Flags().StringVar(&auditListAction, "action", "", "Filter by action")
	auditListCmd.Flags().StringVar(&auditListSince, "since", "", "Filter since time (RFC3339)")
	auditListCmd.Flags().IntVar(&auditListLimit, "limit", 50, "Max results")

	auditStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show audit statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("audit.stats", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	var auditExportFormat string
	auditExportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export audit traces",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("audit.export", map[string]string{
				"format": auditExportFormat,
			})
			if err != nil {
				return err
			}
			data, _ := resp["data"].(map[string]interface{})
			if data != nil && data["format"] == "csv" {
				// Print raw CSV.
				fmt.Print(data["csv"])
			} else {
				printJSON(resp["data"])
			}
			return nil
		},
	}
	auditExportCmd.Flags().StringVar(&auditExportFormat, "format", "json", "Export format (json|csv)")

	auditCmd.AddCommand(auditListCmd, auditStatsCmd, auditExportCmd)

	// ----- backup command -----
	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a database backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("backup", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	// ----- messages commands -----
	messagesCmd := &cobra.Command{
		Use:   "messages",
		Short: "Query messages",
	}

	var (
		messagesListAgent  string
		messagesListStatus string
		messagesListLimit  int
	)
	messagesListCmd := &cobra.Command{
		Use:   "list",
		Short: "List messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{}
			if messagesListAgent != "" {
				reqArgs["agent"] = messagesListAgent
			}
			if messagesListStatus != "" {
				reqArgs["status"] = messagesListStatus
			}
			if messagesListLimit > 0 {
				reqArgs["limit"] = messagesListLimit
			}
			resp, err := adminRequest("messages.list", reqArgs)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No messages found.")
				return nil
			}
			printTable([]string{"ID", "FROM", "TO", "STATUS", "PRIORITY", "BODY", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "FROM": "from_agent", "TO": "to_agent",
				"STATUS": "status", "PRIORITY": "priority", "BODY": "body", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}
	messagesListCmd.Flags().StringVar(&messagesListAgent, "agent", "", "Filter by agent name")
	messagesListCmd.Flags().StringVar(&messagesListStatus, "status", "", "Filter by status")
	messagesListCmd.Flags().IntVar(&messagesListLimit, "limit", 50, "Max results")

	var (
		messagesSearchQuery string
		messagesSearchLimit int
	)
	messagesSearchCmd := &cobra.Command{
		Use:   "search",
		Short: "Search messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("messages.search", map[string]interface{}{
				"query": messagesSearchQuery,
				"limit": messagesSearchLimit,
			})
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No messages found.")
				return nil
			}
			printTable([]string{"ID", "FROM", "TO", "STATUS", "BODY", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "FROM": "from_agent", "TO": "to_agent",
				"STATUS": "status", "BODY": "body", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}
	messagesSearchCmd.Flags().StringVar(&messagesSearchQuery, "query", "", "Search query")
	messagesSearchCmd.Flags().IntVar(&messagesSearchLimit, "limit", 20, "Max results")
	messagesSearchCmd.MarkFlagRequired("query")

	var (
		messagesPurgeOlderThan string
		messagesPurgeAgent     string
		messagesPurgeChannel   string
	)
	messagesPurgeCmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete messages matching filters (at least one filter required)",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{}
			if messagesPurgeOlderThan != "" {
				reqArgs["older_than"] = messagesPurgeOlderThan
			}
			if messagesPurgeAgent != "" {
				reqArgs["agent"] = messagesPurgeAgent
			}
			if messagesPurgeChannel != "" {
				reqArgs["channel"] = messagesPurgeChannel
			}
			if len(reqArgs) == 0 {
				return fmt.Errorf("at least one filter is required (--older-than, --agent, or --channel)")
			}
			resp, err := adminRequest("messages.purge", reqArgs)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	messagesPurgeCmd.Flags().StringVar(&messagesPurgeOlderThan, "older-than", "", "Delete messages older than this (e.g. 6m, 90d, 2160h)")
	messagesPurgeCmd.Flags().StringVar(&messagesPurgeAgent, "agent", "", "Delete messages from/to this agent")
	messagesPurgeCmd.Flags().StringVar(&messagesPurgeChannel, "channel", "", "Delete messages in this channel")

	messagesCmd.AddCommand(messagesListCmd, messagesSearchCmd, messagesPurgeCmd)

	// ----- channels commands -----
	channelsCmd := &cobra.Command{
		Use:   "channels",
		Short: "Manage channels",
	}

	channelsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("channels.list", nil)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No channels found.")
				return nil
			}
			printTable([]string{"ID", "NAME", "TYPE", "PRIVATE", "MEMBERS", "CREATED_BY", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "NAME": "name", "TYPE": "type", "PRIVATE": "is_private",
				"MEMBERS": "member_count", "CREATED_BY": "created_by", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}

	var channelsShowName string
	channelsShowCmd := &cobra.Command{
		Use:   "show",
		Short: "Show channel details and members",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("channels.show", map[string]string{
				"name": channelsShowName,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	channelsShowCmd.Flags().StringVar(&channelsShowName, "name", "", "Channel name")
	channelsShowCmd.MarkFlagRequired("name")

	var (
		channelsCreateName string
		channelsCreateDesc string
	)
	channelsCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new channel",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("channels.create", map[string]string{
				"name":        channelsCreateName,
				"description": channelsCreateDesc,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	channelsCreateCmd.Flags().StringVar(&channelsCreateName, "name", "", "Channel name")
	channelsCreateCmd.Flags().StringVar(&channelsCreateDesc, "description", "", "Channel description")
	channelsCreateCmd.MarkFlagRequired("name")

	var (
		channelsJoinChannel string
		channelsJoinAgent   string
	)
	channelsJoinCmd := &cobra.Command{
		Use:   "join",
		Short: "Add an agent to a channel",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("channels.join", map[string]string{
				"channel": channelsJoinChannel,
				"agent":   channelsJoinAgent,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	channelsJoinCmd.Flags().StringVar(&channelsJoinChannel, "channel", "", "Channel name")
	channelsJoinCmd.Flags().StringVar(&channelsJoinAgent, "agent", "", "Agent name")
	channelsJoinCmd.MarkFlagRequired("channel")
	channelsJoinCmd.MarkFlagRequired("agent")

	channelsCmd.AddCommand(channelsListCmd, channelsShowCmd, channelsCreateCmd, channelsJoinCmd)

	// ----- conversations commands -----
	conversationsCmd := &cobra.Command{
		Use:   "conversations",
		Short: "Query conversations",
	}

	var conversationsListLimit int
	conversationsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List conversations",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{}
			if conversationsListLimit > 0 {
				reqArgs["limit"] = conversationsListLimit
			}
			resp, err := adminRequest("conversations.list", reqArgs)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No conversations found.")
				return nil
			}
			printTable([]string{"ID", "SUBJECT", "CREATED_BY", "MESSAGES", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "SUBJECT": "subject", "CREATED_BY": "created_by",
				"MESSAGES": "message_count", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}
	conversationsListCmd.Flags().IntVar(&conversationsListLimit, "limit", 50, "Max results")

	var conversationsShowID int64
	conversationsShowCmd := &cobra.Command{
		Use:   "show",
		Short: "Show conversation messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("conversations.show", map[string]interface{}{
				"id": conversationsShowID,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	conversationsShowCmd.Flags().Int64Var(&conversationsShowID, "id", 0, "Conversation ID")
	conversationsShowCmd.MarkFlagRequired("id")

	conversationsCmd.AddCommand(conversationsListCmd, conversationsShowCmd)

	// ----- embeddings commands -----
	embeddingsCmd := &cobra.Command{
		Use:   "embeddings",
		Short: "Manage embedding vectors",
	}

	embeddingsStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show embedding status (provider, counts, index size)",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("embeddings.status", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	embeddingsReindexCmd := &cobra.Command{
		Use:   "reindex",
		Short: "Clear all embeddings and re-queue all messages for embedding",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("embeddings.reindex", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	embeddingsClearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete all embeddings and clear the vector index",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("embeddings.clear", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	embeddingsCmd.AddCommand(embeddingsStatusCmd, embeddingsReindexCmd, embeddingsClearCmd)

	// ----- db commands -----
	dbCmd := &cobra.Command{
		Use:   "db",
		Short: "Database maintenance",
	}

	dbVacuumCmd := &cobra.Command{
		Use:   "vacuum",
		Short: "Compact the database to reclaim disk space",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("db.vacuum", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	dbCmd.AddCommand(dbVacuumCmd)

	// ----- retention commands -----
	retentionCmd := &cobra.Command{
		Use:   "retention",
		Short: "Message retention management",
	}

	retentionStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show retention configuration and status",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("retention.status", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	retentionCmd.AddCommand(retentionStatusCmd)

	// ----- webhook commands -----
	webhookCmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage webhooks",
	}

	var (
		webhookRegisterURL    string
		webhookRegisterEvents string
		webhookRegisterSecret string
		webhookRegisterAgent  string
	)
	webhookRegisterCmd := &cobra.Command{
		Use:   "register",
		Short: "Register a webhook",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("webhook.register", map[string]string{
				"url":        webhookRegisterURL,
				"events":     webhookRegisterEvents,
				"secret":     webhookRegisterSecret,
				"agent_name": webhookRegisterAgent,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	webhookRegisterCmd.Flags().StringVar(&webhookRegisterURL, "url", "", "Webhook endpoint URL")
	webhookRegisterCmd.Flags().StringVar(&webhookRegisterEvents, "events", "", "Comma-separated event types (e.g. message.received,channel.message)")
	webhookRegisterCmd.Flags().StringVar(&webhookRegisterSecret, "secret", "", "HMAC signing secret")
	webhookRegisterCmd.Flags().StringVar(&webhookRegisterAgent, "agent", "", "Agent name to hook events for")
	webhookRegisterCmd.MarkFlagRequired("url")
	webhookRegisterCmd.MarkFlagRequired("events")
	webhookRegisterCmd.MarkFlagRequired("secret")
	webhookRegisterCmd.MarkFlagRequired("agent")

	var webhookListAgent string
	webhookListCmd := &cobra.Command{
		Use:   "list",
		Short: "List webhooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{}
			if webhookListAgent != "" {
				reqArgs["agent_name"] = webhookListAgent
			}
			resp, err := adminRequest("webhook.list", reqArgs)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No webhooks found.")
				return nil
			}
			printTable([]string{"ID", "AGENT", "URL", "EVENTS", "STATUS", "FAILURES", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "AGENT": "agent_name", "URL": "url", "EVENTS": "events",
				"STATUS": "status", "FAILURES": "consecutive_failures", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}
	webhookListCmd.Flags().StringVar(&webhookListAgent, "agent", "", "Filter by agent name")

	var webhookDeleteID int64
	webhookDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a webhook",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("webhook.delete", map[string]interface{}{
				"id": webhookDeleteID,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	webhookDeleteCmd.Flags().Int64Var(&webhookDeleteID, "id", 0, "Webhook ID to delete")
	webhookDeleteCmd.MarkFlagRequired("id")

	webhookCmd.AddCommand(webhookRegisterCmd, webhookListCmd, webhookDeleteCmd)

	// ----- k8s commands -----
	k8sCmd := &cobra.Command{
		Use:   "k8s",
		Short: "Manage Kubernetes job handlers",
	}

	var (
		k8sRegisterImage     string
		k8sRegisterEvents    string
		k8sRegisterAgent     string
		k8sRegisterNamespace string
		k8sRegisterMemory    string
		k8sRegisterCPU       string
		k8sRegisterEnv       string
		k8sRegisterTimeout   int
	)
	k8sRegisterCmd := &cobra.Command{
		Use:   "register",
		Short: "Register a K8s job handler",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{
				"image":      k8sRegisterImage,
				"events":     k8sRegisterEvents,
				"agent_name": k8sRegisterAgent,
			}
			if k8sRegisterNamespace != "" {
				reqArgs["namespace"] = k8sRegisterNamespace
			}
			if k8sRegisterMemory != "" {
				reqArgs["resources_memory"] = k8sRegisterMemory
			}
			if k8sRegisterCPU != "" {
				reqArgs["resources_cpu"] = k8sRegisterCPU
			}
			if k8sRegisterEnv != "" {
				reqArgs["env"] = k8sRegisterEnv
			}
			if k8sRegisterTimeout > 0 {
				reqArgs["timeout_seconds"] = k8sRegisterTimeout
			}
			resp, err := adminRequest("k8s.register", reqArgs)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterImage, "image", "", "Container image")
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterEvents, "events", "", "Comma-separated event types")
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterAgent, "agent", "", "Agent name")
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterNamespace, "namespace", "", "Kubernetes namespace (optional)")
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterMemory, "memory", "", "Memory resource limit (e.g. 256Mi)")
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterCPU, "cpu", "", "CPU resource limit (e.g. 500m)")
	k8sRegisterCmd.Flags().StringVar(&k8sRegisterEnv, "env", "", "Comma-separated KEY=VALUE environment variables")
	k8sRegisterCmd.Flags().IntVar(&k8sRegisterTimeout, "timeout", 300, "Job timeout in seconds")
	k8sRegisterCmd.MarkFlagRequired("image")
	k8sRegisterCmd.MarkFlagRequired("events")
	k8sRegisterCmd.MarkFlagRequired("agent")

	var k8sListAgent string
	k8sListCmd := &cobra.Command{
		Use:   "list",
		Short: "List K8s job handlers",
		RunE: func(cmd *cobra.Command, args []string) error {
			reqArgs := map[string]interface{}{}
			if k8sListAgent != "" {
				reqArgs["agent_name"] = k8sListAgent
			}
			resp, err := adminRequest("k8s.list", reqArgs)
			if err != nil {
				return err
			}
			rows := toMapSlice(resp["data"])
			if len(rows) == 0 {
				fmt.Println("No K8s handlers found.")
				return nil
			}
			printTable([]string{"ID", "AGENT", "IMAGE", "EVENTS", "NAMESPACE", "STATUS", "CREATED_AT"}, toTableRows(rows, map[string]string{
				"ID": "id", "AGENT": "agent_name", "IMAGE": "image", "EVENTS": "events",
				"NAMESPACE": "namespace", "STATUS": "status", "CREATED_AT": "created_at",
			}))
			return nil
		},
	}
	k8sListCmd.Flags().StringVar(&k8sListAgent, "agent", "", "Filter by agent name")

	var k8sDeleteID int64
	k8sDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a K8s job handler",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("k8s.delete", map[string]interface{}{
				"id": k8sDeleteID,
			})
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}
	k8sDeleteCmd.Flags().Int64Var(&k8sDeleteID, "id", 0, "Handler ID to delete")
	k8sDeleteCmd.MarkFlagRequired("id")

	k8sCmd.AddCommand(k8sRegisterCmd, k8sListCmd, k8sDeleteCmd)

	// ----- attachments commands -----
	attachmentsCmd := &cobra.Command{
		Use:   "attachments",
		Short: "Manage attachments",
	}

	attachmentsGCCmd := &cobra.Command{
		Use:   "gc",
		Short: "Run attachment garbage collection to remove orphaned files",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adminRequest("attachments.gc", nil)
			if err != nil {
				return err
			}
			printJSON(resp["data"])
			return nil
		},
	}

	var attachmentsBackupOutput string
	var attachmentsBackupDataDir string
	attachmentsBackupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a tar.gz backup of all attachments (no server required)",
		RunE: func(cmd *cobra.Command, args []string) error {
			attachDir := filepath.Join(attachmentsBackupDataDir, "attachments")
			if _, err := os.Stat(attachDir); os.IsNotExist(err) {
				return fmt.Errorf("attachments directory does not exist: %s", attachDir)
			}
			fileCount, totalSize, err := backupAttachments(attachDir, attachmentsBackupOutput)
			if err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}
			fmt.Printf("Backup complete: %d files, %s total, written to %s\n", fileCount, formatBytes(totalSize), attachmentsBackupOutput)
			return nil
		},
	}
	attachmentsBackupCmd.Flags().StringVar(&attachmentsBackupOutput, "output", "", "Output path for the tar.gz archive")
	attachmentsBackupCmd.Flags().StringVar(&attachmentsBackupDataDir, "data", "./data", "Data directory")
	attachmentsBackupCmd.MarkFlagRequired("output")

	var attachmentsRestoreInput string
	var attachmentsRestoreDataDir string
	attachmentsRestoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore attachments from a tar.gz backup (no server required)",
		RunE: func(cmd *cobra.Command, args []string) error {
			attachDir := filepath.Join(attachmentsRestoreDataDir, "attachments")
			restored, skipped, err := restoreAttachments(attachDir, attachmentsRestoreInput)
			if err != nil {
				return fmt.Errorf("restore failed: %w", err)
			}
			fmt.Printf("Restore complete: %d files restored, %d files skipped (already exist)\n", restored, skipped)
			return nil
		},
	}
	attachmentsRestoreCmd.Flags().StringVar(&attachmentsRestoreInput, "input", "", "Input path for the tar.gz archive")
	attachmentsRestoreCmd.Flags().StringVar(&attachmentsRestoreDataDir, "data", "./data", "Data directory")
	attachmentsRestoreCmd.MarkFlagRequired("input")

	attachmentsCmd.AddCommand(attachmentsGCCmd, attachmentsBackupCmd, attachmentsRestoreCmd)

	// ----- add persistent flag and commands to root -----
	rootCmd.PersistentFlags().StringVar(&adminSocket, "socket", "/tmp/synapbus.sock", "Path to admin Unix socket")

	rootCmd.AddCommand(userCmd, agentCmd, auditCmd, backupCmd, messagesCmd, channelsCmd, conversationsCmd, embeddingsCmd, dbCmd, retentionCmd, webhookCmd, k8sCmd, attachmentsCmd)
}

// toTableRows remaps []map[string]string using a header->key mapping.
func toTableRows(data []map[string]string, headerMap map[string]string) []map[string]string {
	var rows []map[string]string
	for _, d := range data {
		row := make(map[string]string)
		for header, key := range headerMap {
			val := d[key]
			// Truncate body field for table display.
			if key == "body" && len(val) > 60 {
				val = val[:57] + "..."
			}
			row[header] = val
		}
		rows = append(rows, row)
	}
	return rows
}

// backupAttachments creates a tar.gz archive of the attachments directory.
// Returns the number of files archived and total bytes of file content.
func backupAttachments(attachmentsDir, outputPath string) (int, int64, error) {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return 0, 0, fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	gzw := gzip.NewWriter(outFile)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	var fileCount int
	var totalSize int64

	err = filepath.Walk(attachmentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip directories — tar entries for files include the path.
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(attachmentsDir, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("file info header: %w", err)
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write header: %w", err)
		}

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open file: %w", err)
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("copy file: %w", err)
		}

		fileCount++
		totalSize += info.Size()
		return nil
	})

	return fileCount, totalSize, err
}

// restoreAttachments extracts a tar.gz archive into the attachments directory.
// Files that already exist on disk are skipped. Returns (restored, skipped) counts.
func restoreAttachments(attachmentsDir, inputPath string) (int, int, error) {
	inFile, err := os.Open(inputPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open input file: %w", err)
	}
	defer inFile.Close()

	gzr, err := gzip.NewReader(inFile)
	if err != nil {
		return 0, 0, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var restored, skipped int

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return restored, skipped, fmt.Errorf("read tar entry: %w", err)
		}

		// Only handle regular files.
		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Sanitize: reject absolute paths and path traversal.
		cleanName := filepath.Clean(header.Name)
		if filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, "..") {
			return restored, skipped, fmt.Errorf("invalid path in archive: %s", header.Name)
		}

		destPath := filepath.Join(attachmentsDir, cleanName)

		// Skip if already exists (content-addressable, so same hash = same content).
		if _, err := os.Stat(destPath); err == nil {
			skipped++
			continue
		}

		// Ensure parent directory exists.
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return restored, skipped, fmt.Errorf("create directory: %w", err)
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			return restored, skipped, fmt.Errorf("create file: %w", err)
		}

		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return restored, skipped, fmt.Errorf("write file: %w", err)
		}
		outFile.Close()

		restored++
	}

	return restored, skipped, nil
}

// formatBytes returns a human-readable byte count string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
