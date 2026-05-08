-- Demo plugin initial schema.
-- Verifies the plugin migration runner + namespaced-table rule.
CREATE TABLE IF NOT EXISTS plugin_demo_notes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    slug       TEXT NOT NULL UNIQUE,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_plugin_demo_notes_slug
    ON plugin_demo_notes(slug);
