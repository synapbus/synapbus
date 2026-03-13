-- SynapBus initial schema
-- All tables use INTEGER PRIMARY KEY for SQLite rowid alias

-- Human user accounts (OAuth 2.1)
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Registered agents
CREATE TABLE IF NOT EXISTS agents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'ai' CHECK (type IN ('ai', 'human')),
    capabilities TEXT NOT NULL DEFAULT '{}',  -- JSON
    owner_id INTEGER NOT NULL REFERENCES users(id),
    api_key_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_agents_owner ON agents(owner_id);
CREATE INDEX idx_agents_status ON agents(status);

-- Conversations (threads)
CREATE TABLE IF NOT EXISTS conversations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subject TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,  -- agent name
    channel_id INTEGER REFERENCES channels(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Messages
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id),
    from_agent TEXT NOT NULL,
    to_agent TEXT,               -- NULL for channel messages
    channel_id INTEGER REFERENCES channels(id),
    body TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 5 CHECK (priority BETWEEN 1 AND 10),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    metadata TEXT NOT NULL DEFAULT '{}',  -- JSON
    claimed_by TEXT,             -- agent that claimed for processing
    claimed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_messages_conversation ON messages(conversation_id);
CREATE INDEX idx_messages_from ON messages(from_agent);
CREATE INDEX idx_messages_to ON messages(to_agent);
CREATE INDEX idx_messages_channel ON messages(channel_id);
CREATE INDEX idx_messages_status ON messages(status);
CREATE INDEX idx_messages_priority ON messages(priority);
CREATE INDEX idx_messages_created ON messages(created_at);

-- Full-text search index for messages
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    body,
    content='messages',
    content_rowid='id'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, body) VALUES (new.id, new.body);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.id, old.body);
END;
CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.id, old.body);
    INSERT INTO messages_fts(rowid, body) VALUES (new.id, new.body);
END;

-- Channels
CREATE TABLE IF NOT EXISTS channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'standard' CHECK (type IN ('standard', 'blackboard', 'auction')),
    is_private INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL,  -- agent name
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Channel membership
CREATE TABLE IF NOT EXISTS channel_members (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(channel_id, agent_name)
);

CREATE INDEX idx_channel_members_channel ON channel_members(channel_id);
CREATE INDEX idx_channel_members_agent ON channel_members(agent_name);

-- Read/unread tracking per agent per conversation
CREATE TABLE IF NOT EXISTS inbox_state (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name TEXT NOT NULL,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id),
    last_read_message_id INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(agent_name, conversation_id)
);

CREATE INDEX idx_inbox_state_agent ON inbox_state(agent_name);

-- Agent activity traces
CREATE TABLE IF NOT EXISTS traces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name TEXT NOT NULL,
    action TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '{}',  -- JSON
    error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_traces_agent ON traces(agent_name);
CREATE INDEX idx_traces_action ON traces(action);
CREATE INDEX idx_traces_created ON traces(created_at);

-- Attachments (content-addressable)
CREATE TABLE IF NOT EXISTS attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL,           -- SHA-256
    original_filename TEXT NOT NULL,
    size INTEGER NOT NULL,
    mime_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    message_id INTEGER REFERENCES messages(id),
    uploaded_by TEXT NOT NULL,    -- agent name
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_attachments_hash ON attachments(hash);
CREATE INDEX idx_attachments_message ON attachments(message_id);

-- OAuth 2.1 tokens
CREATE TABLE IF NOT EXISTS oauth_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL,
    user_id INTEGER REFERENCES users(id),
    access_token_hash TEXT NOT NULL UNIQUE,
    refresh_token_hash TEXT,
    scope TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_oauth_tokens_client ON oauth_tokens(client_id);
CREATE INDEX idx_oauth_tokens_user ON oauth_tokens(user_id);

-- OAuth 2.1 clients
CREATE TABLE IF NOT EXISTS oauth_clients (
    id TEXT PRIMARY KEY,
    secret_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    redirect_uris TEXT NOT NULL DEFAULT '[]',  -- JSON array
    grant_types TEXT NOT NULL DEFAULT '[]',     -- JSON array
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Task auction support
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL REFERENCES channels(id),
    posted_by TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    requirements TEXT NOT NULL DEFAULT '{}',  -- JSON
    deadline TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'assigned', 'completed', 'cancelled')),
    assigned_to TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tasks_channel ON tasks(channel_id);
CREATE INDEX idx_tasks_status ON tasks(status);

-- Task bids
CREATE TABLE IF NOT EXISTS task_bids (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    capabilities TEXT NOT NULL DEFAULT '{}',  -- JSON
    time_estimate TEXT,
    message TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(task_id, agent_name)
);

CREATE INDEX idx_task_bids_task ON task_bids(task_id);

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO schema_migrations (version) VALUES (1);
