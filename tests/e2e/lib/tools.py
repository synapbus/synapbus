"""All 25 SynapBus MCP tool JSON schemas for Claude tool-use."""
from __future__ import annotations

from typing import Any, Dict, List

# -- Messaging tools (5) --

SEND_MESSAGE = {
    "name": "send_message",
    "description": "Send a direct message to another agent or to a channel",
    "input_schema": {
        "type": "object",
        "properties": {
            "to": {"type": "string", "description": "Name of the recipient agent"},
            "body": {"type": "string", "description": "Message body text"},
            "subject": {"type": "string", "description": "Conversation subject (optional)"},
            "priority": {"type": "number", "description": "Message priority (1-10, default 5)", "minimum": 1, "maximum": 10},
            "metadata": {"type": "string", "description": "JSON metadata object (optional)"},
            "channel_id": {"type": "number", "description": "Channel ID for channel messages (optional)"},
        },
        "required": ["to", "body"],
    },
}

READ_INBOX = {
    "name": "read_inbox",
    "description": "Read messages from your inbox",
    "input_schema": {
        "type": "object",
        "properties": {
            "limit": {"type": "integer", "description": "Maximum number of messages to return (default 50)"},
            "status_filter": {"type": "string", "description": "Filter by status: pending, processing, done, failed"},
            "include_read": {"type": "boolean", "description": "Include previously read messages (default false)"},
            "min_priority": {"type": "number", "description": "Minimum priority filter (1-10)"},
            "from_agent": {"type": "string", "description": "Filter by sender agent name"},
        },
    },
}

CLAIM_MESSAGES = {
    "name": "claim_messages",
    "description": "Atomically claim pending messages for processing",
    "input_schema": {
        "type": "object",
        "properties": {
            "limit": {"type": "integer", "description": "Maximum number of messages to claim (default 10)"},
        },
    },
}

MARK_DONE = {
    "name": "mark_done",
    "description": "Mark a claimed message as done or failed",
    "input_schema": {
        "type": "object",
        "properties": {
            "message_id": {"type": "number", "description": "ID of the message to mark"},
            "status": {"type": "string", "enum": ["done", "failed"], "description": "New status: 'done' or 'failed'"},
            "reason": {"type": "string", "description": "Failure reason (only for status='failed')"},
        },
        "required": ["message_id"],
    },
}

SEARCH_MESSAGES = {
    "name": "search_messages",
    "description": "Search messages using semantic or full-text search. Returns messages ranked by relevance.",
    "input_schema": {
        "type": "object",
        "properties": {
            "query": {"type": "string", "description": "Search query string"},
            "limit": {"type": "number", "description": "Maximum results to return (default 10)"},
            "min_priority": {"type": "number", "description": "Minimum priority filter (1-10)"},
            "from_agent": {"type": "string", "description": "Filter by sender agent name"},
            "status": {"type": "string", "description": "Filter by message status"},
            "search_mode": {"type": "string", "description": "Search mode: 'auto', 'semantic', or 'fulltext'"},
        },
    },
}

# -- Agent tools (4) --

REGISTER_AGENT = {
    "name": "register_agent",
    "description": "Register a new agent and receive an API key",
    "input_schema": {
        "type": "object",
        "properties": {
            "name": {"type": "string", "description": "Unique agent name"},
            "display_name": {"type": "string", "description": "Human-readable display name"},
            "type": {"type": "string", "description": "Agent type: 'ai' or 'human' (default 'ai')"},
            "capabilities": {"type": "string", "description": "JSON capabilities object"},
        },
        "required": ["name"],
    },
}

DISCOVER_AGENTS = {
    "name": "discover_agents",
    "description": "Discover agents by capability keywords",
    "input_schema": {
        "type": "object",
        "properties": {
            "query": {"type": "string", "description": "Capability keyword to search for"},
        },
    },
}

UPDATE_AGENT = {
    "name": "update_agent",
    "description": "Update your display name or capabilities",
    "input_schema": {
        "type": "object",
        "properties": {
            "display_name": {"type": "string", "description": "New display name"},
            "capabilities": {"type": "string", "description": "New JSON capabilities object"},
        },
    },
}

DEREGISTER_AGENT = {
    "name": "deregister_agent",
    "description": "Deregister the authenticated agent (soft delete)",
    "input_schema": {
        "type": "object",
        "properties": {},
    },
}

# -- Channel tools (8) --

CREATE_CHANNEL = {
    "name": "create_channel",
    "description": "Create a new channel for group communication",
    "input_schema": {
        "type": "object",
        "properties": {
            "name": {"type": "string", "description": "Unique channel name (alphanumeric, hyphens, underscores, max 64 chars)"},
            "description": {"type": "string", "description": "Channel description"},
            "topic": {"type": "string", "description": "Current channel topic"},
            "type": {"type": "string", "description": "Channel type: 'standard', 'blackboard', or 'auction' (default 'standard')"},
            "is_private": {"type": "boolean", "description": "Whether the channel is private (invite-only). Default false"},
        },
        "required": ["name"],
    },
}

JOIN_CHANNEL = {
    "name": "join_channel",
    "description": "Join an existing channel",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_id": {"type": "number", "description": "ID of the channel to join"},
            "channel_name": {"type": "string", "description": "Name of the channel to join (alternative to channel_id)"},
        },
    },
}

LEAVE_CHANNEL = {
    "name": "leave_channel",
    "description": "Leave a channel you are a member of",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_id": {"type": "number", "description": "ID of the channel to leave"},
            "channel_name": {"type": "string", "description": "Name of the channel to leave (alternative to channel_id)"},
        },
    },
}

LIST_CHANNELS = {
    "name": "list_channels",
    "description": "List all channels visible to you (public + your private channels)",
    "input_schema": {
        "type": "object",
        "properties": {},
    },
}

INVITE_TO_CHANNEL = {
    "name": "invite_to_channel",
    "description": "Invite an agent to a channel (only owner can invite to private channels)",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_id": {"type": "number", "description": "ID of the channel"},
            "channel_name": {"type": "string", "description": "Name of the channel (alternative to channel_id)"},
            "agent_name": {"type": "string", "description": "Name of the agent to invite"},
        },
        "required": ["agent_name"],
    },
}

KICK_FROM_CHANNEL = {
    "name": "kick_from_channel",
    "description": "Remove an agent from a channel (only owner can kick)",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_id": {"type": "number", "description": "ID of the channel"},
            "channel_name": {"type": "string", "description": "Name of the channel (alternative to channel_id)"},
            "agent_name": {"type": "string", "description": "Name of the agent to kick"},
        },
        "required": ["agent_name"],
    },
}

SEND_CHANNEL_MESSAGE = {
    "name": "send_channel_message",
    "description": "Send a message to all members of a channel",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_id": {"type": "number", "description": "ID of the channel"},
            "channel_name": {"type": "string", "description": "Name of the channel (alternative to channel_id)"},
            "body": {"type": "string", "description": "Message body text"},
            "priority": {"type": "number", "description": "Message priority (1-10, default 5)", "minimum": 1, "maximum": 10},
            "metadata": {"type": "string", "description": "JSON metadata object (optional)"},
        },
        "required": ["body"],
    },
}

UPDATE_CHANNEL = {
    "name": "update_channel",
    "description": "Update channel topic or description (only owner can update)",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_id": {"type": "number", "description": "ID of the channel"},
            "channel_name": {"type": "string", "description": "Name of the channel (alternative to channel_id)"},
            "topic": {"type": "string", "description": "New channel topic"},
            "description": {"type": "string", "description": "New channel description"},
        },
    },
}

# -- Swarm / Task tools (5) --

POST_TASK = {
    "name": "post_task",
    "description": "Post a task to an auction channel for agents to bid on",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_name": {"type": "string", "description": "Name of the auction channel"},
            "title": {"type": "string", "description": "Task title"},
            "description": {"type": "string", "description": "Task description"},
            "requirements": {"type": "string", "description": "JSON object of task requirements"},
            "deadline": {"type": "string", "description": "Task deadline in ISO 8601 format"},
        },
        "required": ["channel_name", "title"],
    },
}

BID_TASK = {
    "name": "bid_task",
    "description": "Submit a bid on an open task in an auction channel",
    "input_schema": {
        "type": "object",
        "properties": {
            "task_id": {"type": "number", "description": "ID of the task to bid on"},
            "capabilities": {"type": "string", "description": "JSON object of your relevant capabilities"},
            "time_estimate": {"type": "string", "description": "Estimated time to complete"},
            "message": {"type": "string", "description": "Message to the task poster explaining your bid"},
        },
        "required": ["task_id"],
    },
}

ACCEPT_BID = {
    "name": "accept_bid",
    "description": "Accept a bid on a task you posted, assigning it to the bidding agent",
    "input_schema": {
        "type": "object",
        "properties": {
            "task_id": {"type": "number", "description": "ID of the task"},
            "bid_id": {"type": "number", "description": "ID of the bid to accept"},
        },
        "required": ["task_id", "bid_id"],
    },
}

COMPLETE_TASK = {
    "name": "complete_task",
    "description": "Mark a task as completed (only the assigned agent can do this)",
    "input_schema": {
        "type": "object",
        "properties": {
            "task_id": {"type": "number", "description": "ID of the task to complete"},
        },
        "required": ["task_id"],
    },
}

LIST_TASKS = {
    "name": "list_tasks",
    "description": "List tasks in an auction channel, optionally filtered by status",
    "input_schema": {
        "type": "object",
        "properties": {
            "channel_name": {"type": "string", "description": "Name of the auction channel"},
            "status": {"type": "string", "description": "Filter by task status: open, assigned, completed, cancelled"},
        },
        "required": ["channel_name"],
    },
}

# -- Attachment tools (3) --

UPLOAD_ATTACHMENT = {
    "name": "upload_attachment",
    "description": "Upload a file attachment (base64-encoded). Returns SHA-256 hash.",
    "input_schema": {
        "type": "object",
        "properties": {
            "content": {"type": "string", "description": "Base64-encoded file content"},
            "filename": {"type": "string", "description": "Original filename"},
            "mime_type": {"type": "string", "description": "MIME type override"},
            "message_id": {"type": "number", "description": "Message ID to attach to (optional)"},
        },
        "required": ["content"],
    },
}

DOWNLOAD_ATTACHMENT = {
    "name": "download_attachment",
    "description": "Download an attachment by its SHA-256 hash",
    "input_schema": {
        "type": "object",
        "properties": {
            "hash": {"type": "string", "description": "SHA-256 hash of the attachment"},
        },
        "required": ["hash"],
    },
}

GC_ATTACHMENTS = {
    "name": "gc_attachments",
    "description": "Run garbage collection to remove orphaned attachments",
    "input_schema": {
        "type": "object",
        "properties": {},
    },
}


# -- Tool sets for different scenarios --

MESSAGING_TOOLS: List[Dict[str, Any]] = [
    SEND_MESSAGE, READ_INBOX, CLAIM_MESSAGES, MARK_DONE, SEARCH_MESSAGES,
]

AGENT_TOOLS: List[Dict[str, Any]] = [
    REGISTER_AGENT, DISCOVER_AGENTS, UPDATE_AGENT, DEREGISTER_AGENT,
]

CHANNEL_TOOLS: List[Dict[str, Any]] = [
    CREATE_CHANNEL, JOIN_CHANNEL, LEAVE_CHANNEL, LIST_CHANNELS,
    INVITE_TO_CHANNEL, KICK_FROM_CHANNEL, SEND_CHANNEL_MESSAGE, UPDATE_CHANNEL,
]

TASK_TOOLS: List[Dict[str, Any]] = [
    POST_TASK, BID_TASK, ACCEPT_BID, COMPLETE_TASK, LIST_TASKS,
]

ATTACHMENT_TOOLS: List[Dict[str, Any]] = [
    UPLOAD_ATTACHMENT, DOWNLOAD_ATTACHMENT, GC_ATTACHMENTS,
]

ALL_TOOLS: List[Dict[str, Any]] = (
    MESSAGING_TOOLS + AGENT_TOOLS + CHANNEL_TOOLS + TASK_TOOLS + ATTACHMENT_TOOLS
)


def get_tools_for_scenario(scenario: str) -> List[Dict[str, Any]]:
    """Return an appropriate subset of tools for a scenario."""
    tool_map = {
        "direct_messaging": MESSAGING_TOOLS + [DISCOVER_AGENTS],
        "channels": MESSAGING_TOOLS + CHANNEL_TOOLS,
        "task_auction": MESSAGING_TOOLS + CHANNEL_TOOLS + TASK_TOOLS,
        "access_control": CHANNEL_TOOLS + MESSAGING_TOOLS,
        "agent_discovery": MESSAGING_TOOLS + AGENT_TOOLS,
        "blackboard": MESSAGING_TOOLS + CHANNEL_TOOLS,
        "audit_trail": MESSAGING_TOOLS + CHANNEL_TOOLS,
    }
    return tool_map.get(scenario, ALL_TOOLS)
