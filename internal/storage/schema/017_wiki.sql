-- 017: Wiki articles, revisions, link graph, and FTS

-- Wiki articles
CREATE TABLE IF NOT EXISTS articles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE CHECK(length(slug) >= 2 AND length(slug) <= 100),
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_articles_slug ON articles(slug);
CREATE INDEX idx_articles_updated_at ON articles(updated_at);

-- Revision history (full body per revision)
CREATE TABLE IF NOT EXISTS article_revisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id INTEGER NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    changed_by TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_article_revisions_article ON article_revisions(article_id, revision);

-- Link graph (rebuilt on every article create/update)
CREATE TABLE IF NOT EXISTS article_links (
    from_slug TEXT NOT NULL,
    to_slug TEXT NOT NULL,
    display_text TEXT,
    PRIMARY KEY (from_slug, to_slug)
);

CREATE INDEX idx_article_links_to ON article_links(to_slug);

-- FTS5 for article search
CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(
    title,
    body,
    content='articles',
    content_rowid='id'
);

-- FTS sync triggers
CREATE TRIGGER articles_fts_ai AFTER INSERT ON articles BEGIN
    INSERT INTO articles_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;
CREATE TRIGGER articles_fts_ad AFTER DELETE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, body) VALUES('delete', old.id, old.title, old.body);
END;
CREATE TRIGGER articles_fts_au AFTER UPDATE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, body) VALUES('delete', old.id, old.title, old.body);
    INSERT INTO articles_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;

INSERT INTO schema_migrations (version) VALUES (17);
