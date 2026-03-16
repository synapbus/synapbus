-- External identity provider support (GitHub, Google, Azure AD)
-- Links external IdP accounts to local SynapBus users

CREATE TABLE IF NOT EXISTS user_identities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    external_id TEXT NOT NULL,
    email TEXT,
    display_name TEXT,
    raw_claims TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, external_id)
);

CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);
CREATE INDEX IF NOT EXISTS idx_user_identities_lookup ON user_identities(provider, external_id);

-- Add email column to users table for IdP linking
ALTER TABLE users ADD COLUMN email TEXT;

INSERT INTO schema_migrations (version) VALUES (11);
