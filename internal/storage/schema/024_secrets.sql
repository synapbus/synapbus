-- 024: Secrets — encrypted per-scope values, injected into subprocess env
-- as sanitized A-Z0-9_ variable names. Values are NaCl-secretbox
-- encrypted under a local master key file (<data-dir>/secrets.key).
-- MCP tools never return the plaintext value — only names and
-- availability.

CREATE TABLE secrets (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,
    scope_type      TEXT    NOT NULL CHECK (scope_type IN ('user','agent','task')),
    scope_id        INTEGER NOT NULL,
    value_blob      BLOB    NOT NULL,    -- nonce(24) || ciphertext
    created_by      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at      DATETIME,
    last_used_at    DATETIME
);

CREATE UNIQUE INDEX idx_secrets_scope_name ON secrets(scope_type, scope_id, name) WHERE revoked_at IS NULL;
CREATE INDEX        idx_secrets_scope      ON secrets(scope_type, scope_id);
