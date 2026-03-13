-- Auth schema extensions for OAuth 2.1
-- Adds role to users, extends oauth_clients, creates session and auth code tables

-- Add role column to users table
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'));

-- Add scopes and owner_id columns to oauth_clients
ALTER TABLE oauth_clients ADD COLUMN scopes TEXT NOT NULL DEFAULT '[]';
ALTER TABLE oauth_clients ADD COLUMN owner_id INTEGER REFERENCES users(id);

-- Web UI sessions
CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    last_active_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- OAuth authorization codes
CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id),
    redirect_uri TEXT NOT NULL,
    scopes TEXT NOT NULL DEFAULT '',
    code_challenge TEXT NOT NULL DEFAULT '',
    code_challenge_method TEXT NOT NULL DEFAULT '',
    session_data TEXT NOT NULL DEFAULT '{}',
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    used INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_oauth_auth_codes_client ON oauth_authorization_codes(client_id);

-- Extend oauth_tokens: add token_type, session_data, consumed, parent_signature
ALTER TABLE oauth_tokens ADD COLUMN token_type TEXT NOT NULL DEFAULT 'access';
ALTER TABLE oauth_tokens ADD COLUMN session_data TEXT NOT NULL DEFAULT '{}';
ALTER TABLE oauth_tokens ADD COLUMN consumed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE oauth_tokens ADD COLUMN parent_signature TEXT;
ALTER TABLE oauth_tokens ADD COLUMN signature TEXT;

CREATE INDEX idx_oauth_tokens_signature ON oauth_tokens(signature);
CREATE INDEX idx_oauth_tokens_type ON oauth_tokens(token_type);
CREATE INDEX idx_oauth_tokens_parent ON oauth_tokens(parent_signature);

INSERT INTO schema_migrations (version) VALUES (4);
