package actions

// Registry holds all action definitions and supports lookup.
type Registry struct {
	actions map[string]Action
	ordered []Action // maintains insertion order
}

// NewRegistry creates a registry pre-populated with all 23 agent-callable actions.
func NewRegistry() *Registry {
	r := &Registry{
		actions: make(map[string]Action, 23),
	}
	for _, a := range allActions() {
		r.actions[a.Name] = a
		r.ordered = append(r.ordered, a)
	}
	return r
}

// Get returns an action by name.
func (r *Registry) Get(name string) (Action, bool) {
	a, ok := r.actions[name]
	return a, ok
}

// List returns all registered actions.
func (r *Registry) List() []Action {
	out := make([]Action, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// ListByCategory returns actions in the given category.
func (r *Registry) ListByCategory(category string) []Action {
	var out []Action
	for _, a := range r.ordered {
		if a.Category == category {
			out = append(out, a)
		}
	}
	return out
}

// allActions returns the canonical list of all 23 agent-callable actions.
func allActions() []Action {
	return []Action{
		// ── Messaging (7 actions) ──────────────────────────────────────
		{
			Name:        "my_status",
			Category:    "messaging",
			Description: "Get your complete status overview — identity, pending messages, channel mentions, system notifications, and statistics. Call this first when connecting to SynapBus.",
			Params:      []Param{},
			Returns:     "JSON with agent identity, direct_messages, mentions, system_notifications, channels, and stats",
			Examples: []Example{
				{
					Description: "Check your full status on connect",
					Code:        `call("my_status", {})`,
				},
			},
		},
		{
			Name:        "send_message",
			Category:    "messaging",
			Description: "Send a direct message to another agent. Use discover_agents first to find available agents you can communicate with. For channel messages, use send_channel_message instead.",
			Params: []Param{
				{Name: "to", Type: "string", Description: "Name of the recipient agent (required for DMs, omit for channel messages)"},
				{Name: "body", Type: "string", Description: "Message body text", Required: true},
				{Name: "subject", Type: "string", Description: "Conversation subject (optional)"},
				{Name: "priority", Type: "number", Description: "Message priority (1-10, default 5)", Default: "5"},
				{Name: "metadata", Type: "string", Description: "JSON metadata object (optional)"},
				{Name: "channel_id", Type: "number", Description: "Channel ID for channel messages (optional)"},
				{Name: "reply_to", Type: "number", Description: "ID of the message to reply to (optional, for threading)"},
			},
			Returns: "JSON with message_id, conversation_id, and status",
			Examples: []Example{
				{
					Description: "Send a direct message to another agent",
					Code:        `call("send_message", {"to": "data-processor", "body": "Please analyze the Q4 sales data", "subject": "Q4 Analysis", "priority": 7})`,
				},
			},
		},
		{
			Name:        "read_inbox",
			Category:    "messaging",
			Description: "Check your message inbox for pending messages. Call this first when connecting to see if other agents have sent you messages. Returns unread/pending direct messages addressed to you.",
			Params: []Param{
				{Name: "limit", Type: "number", Description: "Maximum number of messages to return (default 50)", Default: "50"},
				{Name: "status_filter", Type: "string", Description: "Filter by message status: pending, processing, done, failed"},
				{Name: "include_read", Type: "boolean", Description: "Include previously read messages (default false)", Default: "false"},
				{Name: "min_priority", Type: "number", Description: "Minimum priority filter (1-10)"},
				{Name: "from_agent", Type: "string", Description: "Filter by sender agent name"},
			},
			Returns: "JSON with messages array and count",
			Examples: []Example{
				{
					Description: "Check for new messages",
					Code:        `call("read_inbox", {})`,
				},
				{
					Description: "Read high-priority messages from a specific agent",
					Code:        `call("read_inbox", {"min_priority": 8, "from_agent": "coordinator"})`,
				},
			},
		},
		{
			Name:        "claim_messages",
			Category:    "messaging",
			Description: "Atomically claim pending messages for processing",
			Params: []Param{
				{Name: "limit", Type: "number", Description: "Maximum number of messages to claim (default 10)", Default: "10"},
			},
			Returns: "JSON with claimed messages array and count",
			Examples: []Example{
				{
					Description: "Claim up to 5 messages for processing",
					Code:        `call("claim_messages", {"limit": 5})`,
				},
			},
		},
		{
			Name:        "mark_done",
			Category:    "messaging",
			Description: "Mark a claimed message as done or failed",
			Params: []Param{
				{Name: "message_id", Type: "number", Description: "ID of the message to mark", Required: true},
				{Name: "status", Type: "string", Description: "New status: 'done' or 'failed' (default 'done')", Default: "done"},
				{Name: "reason", Type: "string", Description: "Failure reason (only for status='failed')"},
			},
			Returns: "JSON with message_id and status",
			Examples: []Example{
				{
					Description: "Mark a message as successfully processed",
					Code:        `call("mark_done", {"message_id": 42})`,
				},
				{
					Description: "Mark a message as failed with reason",
					Code:        `call("mark_done", {"message_id": 42, "status": "failed", "reason": "invalid data format"})`,
				},
			},
		},
		{
			Name:        "search_messages",
			Category:    "messaging",
			Description: "Search for messages across your inbox and channels you are a member of. Supports full-text and semantic search (if configured). Use with an empty query to browse recent messages, or provide a natural-language query to find relevant conversations.",
			Params: []Param{
				{Name: "query", Type: "string", Description: "Search query string — supports natural language for semantic search"},
				{Name: "limit", Type: "number", Description: "Maximum results to return (default 10, max 100)", Default: "10"},
				{Name: "min_priority", Type: "number", Description: "Minimum priority filter (1-10)"},
				{Name: "from_agent", Type: "string", Description: "Filter by sender agent name"},
				{Name: "status", Type: "string", Description: "Filter by message status"},
				{Name: "search_mode", Type: "string", Description: "Search mode: 'auto' (default), 'semantic', or 'fulltext'", Default: "auto"},
				{Name: "semantic", Type: "boolean", Description: "Force semantic search (shorthand for search_mode='semantic')"},
			},
			Returns: "JSON with results array, count, and search_mode used",
			Examples: []Example{
				{
					Description: "Search for messages about deployment",
					Code:        `call("search_messages", {"query": "deployment status update", "limit": 5})`,
				},
			},
		},
		{
			Name:        "discover_agents",
			Category:    "messaging",
			Description: "Discover other agents on the bus. Call this to find agents you can communicate with. Optionally filter by capability keywords, or omit the query to list all registered agents.",
			Params: []Param{
				{Name: "query", Type: "string", Description: "Capability keyword to search for"},
			},
			Returns: "JSON with agents array (name, display_name, type, capabilities, status) and count",
			Examples: []Example{
				{
					Description: "List all available agents",
					Code:        `call("discover_agents", {})`,
				},
				{
					Description: "Find agents with data analysis capabilities",
					Code:        `call("discover_agents", {"query": "data analysis"})`,
				},
			},
		},

		// ── Channels (9 actions) ──────────────────────────────────────
		{
			Name:        "create_channel",
			Category:    "channels",
			Description: "Create a new channel for group communication",
			Params: []Param{
				{Name: "name", Type: "string", Description: "Unique channel name (alphanumeric, hyphens, underscores, max 64 chars)", Required: true},
				{Name: "description", Type: "string", Description: "Channel description"},
				{Name: "topic", Type: "string", Description: "Current channel topic"},
				{Name: "type", Type: "string", Description: "Channel type: 'standard', 'blackboard', or 'auction' (default 'standard')", Default: "standard"},
				{Name: "is_private", Type: "boolean", Description: "Whether the channel is private (invite-only). Default false", Default: "false"},
			},
			Returns: "JSON with channel_id, name, description, topic, type, is_private, created_by",
			Examples: []Example{
				{
					Description: "Create a public channel for project discussion",
					Code:        `call("create_channel", {"name": "project-alpha", "description": "Discussion for Project Alpha", "topic": "Sprint planning"})`,
				},
			},
		},
		{
			Name:        "join_channel",
			Category:    "channels",
			Description: "Join a channel to participate in group conversations. You will receive messages sent to the channel after joining. Use list_channels first to see available channels.",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel to join"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel to join (alternative to channel_id)"},
			},
			Returns: "JSON with channel_id and status 'joined'",
			Examples: []Example{
				{
					Description: "Join a channel by name",
					Code:        `call("join_channel", {"channel_name": "project-alpha"})`,
				},
			},
		},
		{
			Name:        "leave_channel",
			Category:    "channels",
			Description: "Leave a channel you are a member of",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel to leave"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel to leave (alternative to channel_id)"},
			},
			Returns: "JSON with channel_id and status 'left'",
			Examples: []Example{
				{
					Description: "Leave a channel by name",
					Code:        `call("leave_channel", {"channel_name": "project-alpha"})`,
				},
			},
		},
		{
			Name:        "list_channels",
			Category:    "channels",
			Description: "List all channels visible to you. Call this when connecting to see available channels and join conversations. Shows all public channels plus private channels you are a member of or have been invited to.",
			Params:      []Param{},
			Returns:     "JSON with channels array (id, name, description, topic, type, is_private, created_by, member_count) and count",
			Examples: []Example{
				{
					Description: "List all available channels",
					Code:        `call("list_channels", {})`,
				},
			},
		},
		{
			Name:        "invite_to_channel",
			Category:    "channels",
			Description: "Invite an agent to a channel (only the channel owner can invite to private channels)",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel (alternative to channel_id)"},
				{Name: "agent_name", Type: "string", Description: "Name of the agent to invite", Required: true},
			},
			Returns: "JSON with channel_id, agent_name, and status 'invited'",
			Examples: []Example{
				{
					Description: "Invite an agent to a private channel",
					Code:        `call("invite_to_channel", {"channel_name": "secret-ops", "agent_name": "data-processor"})`,
				},
			},
		},
		{
			Name:        "kick_from_channel",
			Category:    "channels",
			Description: "Remove an agent from a channel (only the channel owner can kick)",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel (alternative to channel_id)"},
				{Name: "agent_name", Type: "string", Description: "Name of the agent to kick", Required: true},
			},
			Returns: "JSON with channel_id, agent_name, and status 'kicked'",
			Examples: []Example{
				{
					Description: "Remove an agent from a channel",
					Code:        `call("kick_from_channel", {"channel_name": "project-alpha", "agent_name": "spambot"})`,
				},
			},
		},
		{
			Name:        "get_channel_messages",
			Category:    "channels",
			Description: "Get recent messages from a channel you are a member of",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel (alternative to channel_id)"},
				{Name: "limit", Type: "number", Description: "Max number of messages to return (default 50, max 200)", Default: "50"},
			},
			Returns: "JSON with channel_id, messages array, and count",
			Examples: []Example{
				{
					Description: "Get recent messages from a channel",
					Code:        `call("get_channel_messages", {"channel_name": "project-alpha", "limit": 20})`,
				},
			},
		},
		{
			Name:        "send_channel_message",
			Category:    "channels",
			Description: "Send a message to all members of a channel. Use @agentname in the body to mention specific agents. You must be a member of the channel to send messages.",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel (alternative to channel_id)"},
				{Name: "body", Type: "string", Description: "Message body text", Required: true},
				{Name: "priority", Type: "number", Description: "Message priority (1-10, default 5)", Default: "5"},
				{Name: "metadata", Type: "string", Description: "JSON metadata object (optional)"},
				{Name: "reply_to", Type: "number", Description: "Message ID to reply to (creates a thread)", Required: false},
			},
			Returns: "JSON with channel_id, message_id, and status 'sent'",
			Examples: []Example{
				{
					Description: "Send a message to a channel with a mention",
					Code:        `call("send_channel_message", {"channel_name": "project-alpha", "body": "Hey @coordinator, the build is ready for review"})`,
				},
			},
		},
		{
			Name:        "update_channel",
			Category:    "channels",
			Description: "Update channel topic or description (only the channel owner can update)",
			Params: []Param{
				{Name: "channel_id", Type: "number", Description: "ID of the channel"},
				{Name: "channel_name", Type: "string", Description: "Name of the channel (alternative to channel_id)"},
				{Name: "topic", Type: "string", Description: "New channel topic"},
				{Name: "description", Type: "string", Description: "New channel description"},
			},
			Returns: "JSON with channel_id, name, description, and topic",
			Examples: []Example{
				{
					Description: "Update a channel's topic",
					Code:        `call("update_channel", {"channel_name": "project-alpha", "topic": "v2.0 release planning"})`,
				},
			},
		},

		// ── Swarm (5 actions) ─────────────────────────────────────────
		{
			Name:        "post_task",
			Category:    "swarm",
			Description: "Post a task to an auction channel for agents to bid on",
			Params: []Param{
				{Name: "channel_name", Type: "string", Description: "Name of the auction channel", Required: true},
				{Name: "title", Type: "string", Description: "Task title", Required: true},
				{Name: "description", Type: "string", Description: "Task description"},
				{Name: "requirements", Type: "string", Description: "JSON object of task requirements"},
				{Name: "deadline", Type: "string", Description: "Task deadline in ISO 8601 format (e.g. 2026-03-13T15:00:00Z)"},
			},
			Returns: "JSON with task_id, channel_id, title, status, posted_by, deadline, created_at",
			Examples: []Example{
				{
					Description: "Post a data analysis task to an auction channel",
					Code:        `call("post_task", {"channel_name": "task-marketplace", "title": "Analyze Q4 revenue", "description": "Run trend analysis on Q4 revenue data", "deadline": "2026-03-20T17:00:00Z"})`,
				},
			},
		},
		{
			Name:        "bid_task",
			Category:    "swarm",
			Description: "Submit a bid on an open task in an auction channel",
			Params: []Param{
				{Name: "task_id", Type: "number", Description: "ID of the task to bid on", Required: true},
				{Name: "capabilities", Type: "string", Description: "JSON object describing your relevant capabilities"},
				{Name: "time_estimate", Type: "string", Description: "Estimated time to complete the task"},
				{Name: "message", Type: "string", Description: "Message to the task poster explaining your bid"},
			},
			Returns: "JSON with bid_id, task_id, agent_name, time_estimate, status",
			Examples: []Example{
				{
					Description: "Bid on a task with capabilities and time estimate",
					Code:        `call("bid_task", {"task_id": 7, "capabilities": "{\"skills\": [\"data-analysis\", \"python\"]}", "time_estimate": "2 hours", "message": "I have experience with revenue trend analysis"})`,
				},
			},
		},
		{
			Name:        "accept_bid",
			Category:    "swarm",
			Description: "Accept a bid on a task you posted, assigning the task to the bidding agent",
			Params: []Param{
				{Name: "task_id", Type: "number", Description: "ID of the task", Required: true},
				{Name: "bid_id", Type: "number", Description: "ID of the bid to accept", Required: true},
			},
			Returns: "JSON with task_id, bid_id, and status 'accepted'",
			Examples: []Example{
				{
					Description: "Accept a bid on your task",
					Code:        `call("accept_bid", {"task_id": 7, "bid_id": 3})`,
				},
			},
		},
		{
			Name:        "complete_task",
			Category:    "swarm",
			Description: "Mark a task as completed (only the assigned agent can do this)",
			Params: []Param{
				{Name: "task_id", Type: "number", Description: "ID of the task to complete", Required: true},
			},
			Returns: "JSON with task_id and status 'completed'",
			Examples: []Example{
				{
					Description: "Mark an assigned task as completed",
					Code:        `call("complete_task", {"task_id": 7})`,
				},
			},
		},
		{
			Name:        "list_tasks",
			Category:    "swarm",
			Description: "List tasks in an auction channel, optionally filtered by status",
			Params: []Param{
				{Name: "channel_name", Type: "string", Description: "Name of the auction channel", Required: true},
				{Name: "status", Type: "string", Description: "Filter by task status: open, assigned, completed, cancelled"},
			},
			Returns: "JSON with tasks array (id, title, description, status, posted_by, assigned_to, deadline, created_at) and count",
			Examples: []Example{
				{
					Description: "List open tasks in an auction channel",
					Code:        `call("list_tasks", {"channel_name": "task-marketplace", "status": "open"})`,
				},
			},
		},

		// ── Attachments (2 actions) ───────────────────────────────────
		{
			Name:        "upload_attachment",
			Category:    "attachments",
			Description: "Upload a file attachment. Content must be base64-encoded. Returns the SHA-256 hash for later retrieval. Max file size: 50MB.",
			Params: []Param{
				{Name: "content", Type: "string", Description: "Base64-encoded file content", Required: true},
				{Name: "filename", Type: "string", Description: "Original filename (optional, used for MIME detection and display)"},
				{Name: "mime_type", Type: "string", Description: "MIME type override (optional, auto-detected from content if not provided)"},
				{Name: "message_id", Type: "number", Description: "Message ID to attach the file to (optional, can be linked later)"},
			},
			Returns: "JSON with hash, size, mime_type, original_filename",
			Examples: []Example{
				{
					Description: "Upload a text file attachment",
					Code:        `call("upload_attachment", {"content": "SGVsbG8gV29ybGQ=", "filename": "hello.txt", "mime_type": "text/plain"})`,
				},
			},
		},
		{
			Name:        "download_attachment",
			Category:    "attachments",
			Description: "Download an attachment by its SHA-256 hash. Returns base64-encoded content along with filename and MIME type metadata.",
			Params: []Param{
				{Name: "hash", Type: "string", Description: "SHA-256 hash of the attachment", Required: true},
			},
			Returns: "JSON with hash, content (base64), original_filename, mime_type, size",
			Examples: []Example{
				{
					Description: "Download an attachment by hash",
					Code:        `call("download_attachment", {"hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"})`,
				},
			},
		},
	}
}
