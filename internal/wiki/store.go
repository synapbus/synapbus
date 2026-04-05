package wiki

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// slugRe validates article slugs: lowercase letters, digits, hyphens, 2-100 chars.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)

// wikiLinkRe matches [[slug]] or [[slug|Display Text]] in article bodies.
var wikiLinkRe = regexp.MustCompile(`\[\[([a-z0-9][a-z0-9-]*[a-z0-9])(?:\|([^\]]+))?\]\]`)

// ValidateSlug checks that a slug meets the wiki naming rules.
func ValidateSlug(slug string) error {
	if len(slug) < 2 || len(slug) > 100 {
		return fmt.Errorf("slug must be 2-100 characters")
	}
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("slug must be lowercase letters, numbers, and hyphens (e.g. 'mcp-security')")
	}
	return nil
}

// ExtractLinks parses [[slug]] and [[slug|display]] references from markdown body.
func ExtractLinks(body string) []ArticleLink {
	matches := wikiLinkRe.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	var links []ArticleLink
	for _, m := range matches {
		slug := m[1]
		if seen[slug] {
			continue
		}
		seen[slug] = true
		link := ArticleLink{ToSlug: slug}
		if len(m) > 2 && m[2] != "" {
			link.DisplayText = m[2]
		}
		links = append(links, link)
	}
	return links
}

// WordCount returns the number of whitespace-delimited words in s.
func WordCount(s string) int {
	return len(strings.Fields(s))
}

// Store provides SQLite-backed CRUD operations for wiki articles.
type Store struct {
	db *sql.DB
}

// NewStore creates a new wiki store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateArticle inserts a new article, its first revision, and link graph entries.
func (s *Store) CreateArticle(ctx context.Context, slug, title, body, author string) (*Article, error) {
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO articles (slug, title, body, created_by, updated_by, revision, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		slug, title, body, author, author,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, fmt.Errorf("article with slug %q already exists", slug)
		}
		return nil, fmt.Errorf("insert article: %w", err)
	}

	articleID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get article id: %w", err)
	}

	// Insert first revision
	_, err = tx.ExecContext(ctx,
		`INSERT INTO article_revisions (article_id, revision, body, changed_by, created_at)
		 VALUES (?, 1, ?, ?, CURRENT_TIMESTAMP)`,
		articleID, body, author,
	)
	if err != nil {
		return nil, fmt.Errorf("insert revision: %w", err)
	}

	// Extract and store links
	if err := s.updateLinks(ctx, tx, slug, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return s.GetArticle(ctx, slug)
}

// GetArticle retrieves the current version of an article by slug.
func (s *Store) GetArticle(ctx context.Context, slug string) (*Article, error) {
	var a Article
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, title, body, created_by, updated_by, revision, created_at, updated_at
		 FROM articles WHERE slug = ?`, slug,
	).Scan(&a.ID, &a.Slug, &a.Title, &a.Body, &a.CreatedBy, &a.UpdatedBy, &a.Revision, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("article %q not found", slug)
	}
	if err != nil {
		return nil, fmt.Errorf("get article: %w", err)
	}

	a.WordCount = WordCount(a.Body)

	// Get outgoing links
	outgoing, err := s.GetOutgoingLinks(ctx, slug)
	if err != nil {
		return nil, err
	}
	a.OutgoingLinks = outgoing

	// Get backlinks
	backlinks, err := s.GetBacklinks(ctx, slug)
	if err != nil {
		return nil, err
	}
	a.Backlinks = backlinks

	return &a, nil
}

// UpdateArticle updates an article's body and/or title, creating a new revision.
func (s *Store) UpdateArticle(ctx context.Context, slug, title, body, author string) (*Article, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get current article
	var articleID int64
	var currentRevision int
	var currentTitle string
	err = tx.QueryRowContext(ctx,
		`SELECT id, revision, title FROM articles WHERE slug = ?`, slug,
	).Scan(&articleID, &currentRevision, &currentTitle)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("article %q not found", slug)
	}
	if err != nil {
		return nil, fmt.Errorf("get article for update: %w", err)
	}

	newRevision := currentRevision + 1
	if title == "" {
		title = currentTitle
	}

	// Update article
	_, err = tx.ExecContext(ctx,
		`UPDATE articles SET title = ?, body = ?, updated_by = ?, revision = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE slug = ?`,
		title, body, author, newRevision, slug,
	)
	if err != nil {
		return nil, fmt.Errorf("update article: %w", err)
	}

	// Insert new revision
	_, err = tx.ExecContext(ctx,
		`INSERT INTO article_revisions (article_id, revision, body, changed_by, created_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		articleID, newRevision, body, author,
	)
	if err != nil {
		return nil, fmt.Errorf("insert revision: %w", err)
	}

	// Re-extract and store links
	if err := s.updateLinks(ctx, tx, slug, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return s.GetArticle(ctx, slug)
}

// ListArticles returns article summaries, optionally filtered by FTS query.
func (s *Store) ListArticles(ctx context.Context, query string, limit int) ([]ArticleSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var rows *sql.Rows
	var err error

	if query != "" {
		sanitized := sanitizeFTS5Query(query)
		rows, err = s.db.QueryContext(ctx,
			`SELECT a.slug, a.title, a.updated_at, a.revision, a.body
			 FROM articles a
			 JOIN articles_fts ON articles_fts.rowid = a.id
			 WHERE articles_fts MATCH ?
			 ORDER BY rank
			 LIMIT ?`, sanitized, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT slug, title, updated_at, revision, body
			 FROM articles
			 ORDER BY updated_at DESC
			 LIMIT ?`, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list articles: %w", err)
	}
	defer rows.Close()

	var articles []ArticleSummary
	for rows.Next() {
		var as ArticleSummary
		var body string
		if err := rows.Scan(&as.Slug, &as.Title, &as.UpdatedAt, &as.Revision, &body); err != nil {
			return nil, fmt.Errorf("scan article: %w", err)
		}
		as.WordCount = WordCount(body)
		articles = append(articles, as)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate articles: %w", err)
	}

	if articles == nil {
		articles = []ArticleSummary{}
	}
	return articles, nil
}

// GetRevisions returns the revision history for an article.
func (s *Store) GetRevisions(ctx context.Context, slug string) ([]ArticleRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.revision, r.body, r.changed_by, r.created_at
		 FROM article_revisions r
		 JOIN articles a ON a.id = r.article_id
		 WHERE a.slug = ?
		 ORDER BY r.revision DESC`, slug,
	)
	if err != nil {
		return nil, fmt.Errorf("get revisions: %w", err)
	}
	defer rows.Close()

	var revisions []ArticleRevision
	for rows.Next() {
		var rev ArticleRevision
		if err := rows.Scan(&rev.ID, &rev.Revision, &rev.Body, &rev.ChangedBy, &rev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan revision: %w", err)
		}
		rev.WordCount = WordCount(rev.Body)
		revisions = append(revisions, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate revisions: %w", err)
	}

	if revisions == nil {
		revisions = []ArticleRevision{}
	}
	return revisions, nil
}

// GetBacklinks returns articles that link TO the given slug.
func (s *Store) GetBacklinks(ctx context.Context, slug string) ([]ArticleSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.slug, a.title, a.updated_at, a.revision, a.body
		 FROM articles a
		 JOIN article_links l ON l.from_slug = a.slug
		 WHERE l.to_slug = ?
		 ORDER BY a.updated_at DESC`, slug,
	)
	if err != nil {
		return nil, fmt.Errorf("get backlinks: %w", err)
	}
	defer rows.Close()

	var backlinks []ArticleSummary
	for rows.Next() {
		var as ArticleSummary
		var body string
		if err := rows.Scan(&as.Slug, &as.Title, &as.UpdatedAt, &as.Revision, &body); err != nil {
			return nil, fmt.Errorf("scan backlink: %w", err)
		}
		as.WordCount = WordCount(body)
		backlinks = append(backlinks, as)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate backlinks: %w", err)
	}

	if backlinks == nil {
		backlinks = []ArticleSummary{}
	}
	return backlinks, nil
}

// GetOutgoingLinks returns links FROM the given article.
func (s *Store) GetOutgoingLinks(ctx context.Context, slug string) ([]ArticleLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_slug, to_slug, COALESCE(display_text, '') FROM article_links WHERE from_slug = ?`, slug,
	)
	if err != nil {
		return nil, fmt.Errorf("get outgoing links: %w", err)
	}
	defer rows.Close()

	var links []ArticleLink
	for rows.Next() {
		var l ArticleLink
		if err := rows.Scan(&l.FromSlug, &l.ToSlug, &l.DisplayText); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate links: %w", err)
	}

	if links == nil {
		links = []ArticleLink{}
	}
	return links, nil
}

// GetMapOfContent returns a bird's-eye view of the wiki knowledge graph.
func (s *Store) GetMapOfContent(ctx context.Context) (*MapOfContent, error) {
	moc := &MapOfContent{}

	// Get all articles
	rows, err := s.db.QueryContext(ctx,
		`SELECT slug, title, updated_at, revision, body FROM articles ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get all articles: %w", err)
	}
	defer rows.Close()

	type articleInfo struct {
		summary ArticleSummary
	}

	var allArticles []articleInfo
	slugSet := make(map[string]bool)
	for rows.Next() {
		var as ArticleSummary
		var body string
		if err := rows.Scan(&as.Slug, &as.Title, &as.UpdatedAt, &as.Revision, &body); err != nil {
			return nil, fmt.Errorf("scan article: %w", err)
		}
		as.WordCount = WordCount(body)
		allArticles = append(allArticles, articleInfo{summary: as})
		slugSet[as.Slug] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate articles: %w", err)
	}

	moc.Total = len(allArticles)

	// Get backlink counts per article
	backlinkCounts := make(map[string]int)
	blRows, err := s.db.QueryContext(ctx,
		`SELECT to_slug, COUNT(*) FROM article_links GROUP BY to_slug`,
	)
	if err != nil {
		return nil, fmt.Errorf("get backlink counts: %w", err)
	}
	defer blRows.Close()
	for blRows.Next() {
		var slug string
		var count int
		if err := blRows.Scan(&slug, &count); err != nil {
			return nil, fmt.Errorf("scan backlink count: %w", err)
		}
		backlinkCounts[slug] = count
	}
	if err := blRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate backlink counts: %w", err)
	}

	// Get outgoing link counts per article
	outgoingCounts := make(map[string]int)
	olRows, err := s.db.QueryContext(ctx,
		`SELECT from_slug, COUNT(*) FROM article_links GROUP BY from_slug`,
	)
	if err != nil {
		return nil, fmt.Errorf("get outgoing counts: %w", err)
	}
	defer olRows.Close()
	for olRows.Next() {
		var slug string
		var count int
		if err := olRows.Scan(&slug, &count); err != nil {
			return nil, fmt.Errorf("scan outgoing count: %w", err)
		}
		outgoingCounts[slug] = count
	}
	if err := olRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outgoing counts: %w", err)
	}

	// Classify articles
	for _, ai := range allArticles {
		blCount := backlinkCounts[ai.summary.Slug]

		// Hubs: articles with 2+ backlinks
		if blCount >= 2 {
			moc.Hubs = append(moc.Hubs, ArticleWithLinks{
				ArticleSummary: ai.summary,
				BacklinkCount:  blCount,
			})
		}

		// Orphans: articles with no backlinks AND no outgoing links
		if blCount == 0 && outgoingCounts[ai.summary.Slug] == 0 {
			moc.Orphans = append(moc.Orphans, ai.summary)
		}

		moc.Articles = append(moc.Articles, ai.summary)
	}

	// Wanted: slugs referenced in links but not existing as articles
	wantedRows, err := s.db.QueryContext(ctx,
		`SELECT to_slug, COUNT(*) as cnt
		 FROM article_links
		 WHERE to_slug NOT IN (SELECT slug FROM articles)
		 GROUP BY to_slug
		 ORDER BY cnt DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get wanted articles: %w", err)
	}
	defer wantedRows.Close()
	for wantedRows.Next() {
		var w WantedArticle
		if err := wantedRows.Scan(&w.Slug, &w.ReferencedCount); err != nil {
			return nil, fmt.Errorf("scan wanted: %w", err)
		}
		moc.Wanted = append(moc.Wanted, w)
	}
	if err := wantedRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wanted: %w", err)
	}

	// Ensure non-nil slices for JSON
	if moc.Hubs == nil {
		moc.Hubs = []ArticleWithLinks{}
	}
	if moc.Articles == nil {
		moc.Articles = []ArticleSummary{}
	}
	if moc.Orphans == nil {
		moc.Orphans = []ArticleSummary{}
	}
	if moc.Wanted == nil {
		moc.Wanted = []WantedArticle{}
	}

	return moc, nil
}

// updateLinks deletes old links from the article and inserts new ones extracted from body.
func (s *Store) updateLinks(ctx context.Context, tx *sql.Tx, fromSlug, body string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM article_links WHERE from_slug = ?`, fromSlug)
	if err != nil {
		return fmt.Errorf("delete old links: %w", err)
	}

	links := ExtractLinks(body)
	for _, l := range links {
		_, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO article_links (from_slug, to_slug, display_text) VALUES (?, ?, ?)`,
			fromSlug, l.ToSlug, l.DisplayText,
		)
		if err != nil {
			return fmt.Errorf("insert link %s -> %s: %w", fromSlug, l.ToSlug, err)
		}
	}
	return nil
}

// sanitizeFTS5Query escapes FTS5 reserved words and operators by wrapping each
// token in double quotes, turning them into literal phrase tokens.
func sanitizeFTS5Query(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	tokens := strings.Fields(query)
	quoted := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		// Already double-quoted — leave as-is
		if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
			quoted = append(quoted, tok)
			continue
		}
		// Wrap in double quotes to make it a literal FTS5 phrase.
		// Escape any embedded double quotes by doubling them.
		escaped := strings.ReplaceAll(tok, `"`, `""`)
		quoted = append(quoted, `"`+escaped+`"`)
	}
	return strings.Join(quoted, " ")
}
