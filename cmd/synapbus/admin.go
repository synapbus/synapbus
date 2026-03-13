package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var adminSocket string

// adminRequest sends a command over the Unix socket and returns the parsed response.
func adminRequest(command string, args interface{}) (map[string]interface{}, error) {
	socket := adminSocket
	if s := os.Getenv("SYNAPBUS_SOCKET"); s != "" && socket == "./data/synapbus.sock" {
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

	agentCmd.AddCommand(agentListCmd, agentCreateCmd, agentDeleteCmd, agentRevokeKeyCmd)

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

	messagesCmd.AddCommand(messagesListCmd, messagesSearchCmd)

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

	channelsCmd.AddCommand(channelsListCmd, channelsShowCmd)

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

	// ----- add persistent flag and commands to root -----
	rootCmd.PersistentFlags().StringVar(&adminSocket, "socket", "./data/synapbus.sock", "Path to admin Unix socket")

	rootCmd.AddCommand(userCmd, agentCmd, auditCmd, backupCmd, messagesCmd, channelsCmd, conversationsCmd)
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
