package wiki

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/synapbus/synapbus/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	ctx := context.Background()
	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return db
}

func TestCreateArticle(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	t.Run("create success", func(t *testing.T) {
		a, err := store.CreateArticle(ctx, "mcp-security", "MCP Security", "# Security\n\nMCP security overview", "agent-a")
		if err != nil {
			t.Fatalf("create article: %v", err)
		}
		if a.Slug != "mcp-security" {
			t.Errorf("slug = %q, want %q", a.Slug, "mcp-security")
		}
		if a.Title != "MCP Security" {
			t.Errorf("title = %q, want %q", a.Title, "MCP Security")
		}
		if a.Revision != 1 {
			t.Errorf("revision = %d, want 1", a.Revision)
		}
		if a.CreatedBy != "agent-a" {
			t.Errorf("created_by = %q, want %q", a.CreatedBy, "agent-a")
		}
		if a.WordCount != 5 {
			t.Errorf("word_count = %d, want 5", a.WordCount)
		}
	})

	t.Run("duplicate slug error", func(t *testing.T) {
		_, err := store.CreateArticle(ctx, "mcp-security", "Another", "body", "agent-b")
		if err == nil {
			t.Fatal("expected error for duplicate slug")
		}
	})

	t.Run("invalid slug error", func(t *testing.T) {
		tests := []struct {
			slug string
		}{
			{""},
			{"a"},
			{"A-B"},
			{"-abc"},
			{"abc-"},
			{"a b c"},
		}
		for _, tt := range tests {
			_, err := store.CreateArticle(ctx, tt.slug, "Title", "Body", "agent-a")
			if err == nil {
				t.Errorf("expected error for slug %q", tt.slug)
			}
		}
	})

	t.Run("create with links", func(t *testing.T) {
		a, err := store.CreateArticle(ctx, "overview-page", "Overview", "See [[mcp-security]] and [[a2a-protocols|A2A]]", "agent-a")
		if err != nil {
			t.Fatalf("create article: %v", err)
		}
		if len(a.OutgoingLinks) != 2 {
			t.Fatalf("outgoing links = %d, want 2", len(a.OutgoingLinks))
		}
		linkMap := make(map[string]string)
		for _, l := range a.OutgoingLinks {
			linkMap[l.ToSlug] = l.DisplayText
		}
		if _, ok := linkMap["mcp-security"]; !ok {
			t.Error("expected link to mcp-security")
		}
		if dt, ok := linkMap["a2a-protocols"]; !ok {
			t.Error("expected link to a2a-protocols")
		} else if dt != "A2A" {
			t.Errorf("a2a-protocols display_text = %q, want %q", dt, "A2A")
		}
	})
}

func TestGetArticle(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store.CreateArticle(ctx, "test-get", "Test Get", "Body here", "agent-a")
		a, err := store.GetArticle(ctx, "test-get")
		if err != nil {
			t.Fatalf("get article: %v", err)
		}
		if a.Slug != "test-get" {
			t.Errorf("slug = %q, want %q", a.Slug, "test-get")
		}
		if a.Body != "Body here" {
			t.Errorf("body = %q, want %q", a.Body, "Body here")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.GetArticle(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent slug")
		}
	})
}

func TestUpdateArticle(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	t.Run("update body and revision increments", func(t *testing.T) {
		store.CreateArticle(ctx, "update-me", "Update Me", "Original body", "agent-a")

		a, err := store.UpdateArticle(ctx, "update-me", "", "New body with [[linked-article]]", "agent-b")
		if err != nil {
			t.Fatalf("update article: %v", err)
		}
		if a.Revision != 2 {
			t.Errorf("revision = %d, want 2", a.Revision)
		}
		if a.Body != "New body with [[linked-article]]" {
			t.Errorf("body = %q, want updated body", a.Body)
		}
		if a.UpdatedBy != "agent-b" {
			t.Errorf("updated_by = %q, want %q", a.UpdatedBy, "agent-b")
		}
		if a.Title != "Update Me" {
			t.Errorf("title = %q, want %q (should keep original)", a.Title, "Update Me")
		}
		if len(a.OutgoingLinks) != 1 {
			t.Errorf("outgoing links = %d, want 1", len(a.OutgoingLinks))
		}
	})

	t.Run("update title", func(t *testing.T) {
		a, err := store.UpdateArticle(ctx, "update-me", "New Title", "New body v3", "agent-a")
		if err != nil {
			t.Fatalf("update article: %v", err)
		}
		if a.Title != "New Title" {
			t.Errorf("title = %q, want %q", a.Title, "New Title")
		}
		if a.Revision != 3 {
			t.Errorf("revision = %d, want 3", a.Revision)
		}
	})

	t.Run("links re-extracted on update", func(t *testing.T) {
		store.CreateArticle(ctx, "link-test", "Link Test", "See [[update-me]]", "agent-a")

		// Verify backlinks to update-me
		backlinks, err := store.GetBacklinks(ctx, "update-me")
		if err != nil {
			t.Fatalf("get backlinks: %v", err)
		}
		found := false
		for _, bl := range backlinks {
			if bl.Slug == "link-test" {
				found = true
			}
		}
		if !found {
			t.Error("expected backlink from link-test to update-me")
		}

		// Update link-test to remove the link
		store.UpdateArticle(ctx, "link-test", "", "No more links here", "agent-a")
		backlinks, err = store.GetBacklinks(ctx, "update-me")
		if err != nil {
			t.Fatalf("get backlinks after update: %v", err)
		}
		for _, bl := range backlinks {
			if bl.Slug == "link-test" {
				t.Error("backlink from link-test should have been removed")
			}
		}
	})

	t.Run("update nonexistent", func(t *testing.T) {
		_, err := store.UpdateArticle(ctx, "no-such-article", "", "body", "agent-a")
		if err == nil {
			t.Fatal("expected error for nonexistent slug")
		}
	})
}

func TestListArticles(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	store.CreateArticle(ctx, "alpha-topic", "Alpha Topic", "This is about alpha testing", "agent-a")
	store.CreateArticle(ctx, "beta-topic", "Beta Topic", "This is about beta testing", "agent-a")
	store.CreateArticle(ctx, "gamma-topic", "Gamma Topic", "Gamma is totally different", "agent-a")

	t.Run("list all", func(t *testing.T) {
		articles, err := store.ListArticles(ctx, "", 50)
		if err != nil {
			t.Fatalf("list articles: %v", err)
		}
		if len(articles) != 3 {
			t.Errorf("count = %d, want 3", len(articles))
		}
	})

	t.Run("FTS search", func(t *testing.T) {
		articles, err := store.ListArticles(ctx, "alpha", 50)
		if err != nil {
			t.Fatalf("search articles: %v", err)
		}
		if len(articles) != 1 {
			t.Errorf("count = %d, want 1", len(articles))
		}
		if len(articles) > 0 && articles[0].Slug != "alpha-topic" {
			t.Errorf("slug = %q, want %q", articles[0].Slug, "alpha-topic")
		}
	})

	t.Run("FTS search title", func(t *testing.T) {
		articles, err := store.ListArticles(ctx, "Gamma", 50)
		if err != nil {
			t.Fatalf("search articles: %v", err)
		}
		if len(articles) != 1 {
			t.Errorf("count = %d, want 1", len(articles))
		}
	})

	t.Run("limit", func(t *testing.T) {
		articles, err := store.ListArticles(ctx, "", 2)
		if err != nil {
			t.Fatalf("list articles: %v", err)
		}
		if len(articles) != 2 {
			t.Errorf("count = %d, want 2", len(articles))
		}
	})

	t.Run("no results", func(t *testing.T) {
		articles, err := store.ListArticles(ctx, "zzzznonexistent", 50)
		if err != nil {
			t.Fatalf("search articles: %v", err)
		}
		if len(articles) != 0 {
			t.Errorf("count = %d, want 0", len(articles))
		}
	})
}

func TestGetRevisions(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	store.CreateArticle(ctx, "rev-test", "Rev Test", "Version 1 body", "agent-a")
	store.UpdateArticle(ctx, "rev-test", "", "Version 2 body", "agent-b")
	store.UpdateArticle(ctx, "rev-test", "", "Version 3 body", "agent-a")

	revisions, err := store.GetRevisions(ctx, "rev-test")
	if err != nil {
		t.Fatalf("get revisions: %v", err)
	}
	if len(revisions) != 3 {
		t.Fatalf("revision count = %d, want 3", len(revisions))
	}
	// Revisions are ordered DESC
	if revisions[0].Revision != 3 {
		t.Errorf("latest revision = %d, want 3", revisions[0].Revision)
	}
	if revisions[0].ChangedBy != "agent-a" {
		t.Errorf("latest changed_by = %q, want %q", revisions[0].ChangedBy, "agent-a")
	}
	if revisions[2].Revision != 1 {
		t.Errorf("oldest revision = %d, want 1", revisions[2].Revision)
	}
	if revisions[2].Body != "Version 1 body" {
		t.Errorf("oldest body = %q, want %q", revisions[2].Body, "Version 1 body")
	}
}

func TestGetBacklinks(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	store.CreateArticle(ctx, "target-article", "Target", "I am the target", "agent-a")
	store.CreateArticle(ctx, "linker-one", "Linker One", "See [[target-article]] for details", "agent-a")
	store.CreateArticle(ctx, "linker-two", "Linker Two", "Also references [[target-article]]", "agent-b")
	store.CreateArticle(ctx, "unrelated", "Unrelated", "No links here", "agent-a")

	backlinks, err := store.GetBacklinks(ctx, "target-article")
	if err != nil {
		t.Fatalf("get backlinks: %v", err)
	}
	if len(backlinks) != 2 {
		t.Fatalf("backlink count = %d, want 2", len(backlinks))
	}

	slugs := map[string]bool{}
	for _, bl := range backlinks {
		slugs[bl.Slug] = true
	}
	if !slugs["linker-one"] {
		t.Error("expected backlink from linker-one")
	}
	if !slugs["linker-two"] {
		t.Error("expected backlink from linker-two")
	}
}

func TestExtractLinks(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		want  int
		slugs []string
	}{
		{
			name:  "simple link",
			body:  "See [[mcp-security]] for details",
			want:  1,
			slugs: []string{"mcp-security"},
		},
		{
			name:  "link with display text",
			body:  "Check [[a2a-protocols|A2A Protocols]]",
			want:  1,
			slugs: []string{"a2a-protocols"},
		},
		{
			name:  "multiple links",
			body:  "See [[alpha-topic]] and [[beta-topic]] and [[gamma-topic]]",
			want:  3,
			slugs: []string{"alpha-topic", "beta-topic", "gamma-topic"},
		},
		{
			name:  "duplicate links deduped",
			body:  "[[mcp-security]] is great. Also see [[mcp-security]]",
			want:  1,
			slugs: []string{"mcp-security"},
		},
		{
			name:  "no matches",
			body:  "No wiki links here at all",
			want:  0,
			slugs: nil,
		},
		{
			name:  "invalid slug format not matched",
			body:  "[[A]] and [[-abc]] and [[abc-]] are invalid",
			want:  0,
			slugs: nil,
		},
		{
			name:  "mixed valid and invalid",
			body:  "Valid: [[good-slug]] Invalid: [[A]] [[ab]]",
			want:  2,
			slugs: []string{"good-slug", "ab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links := ExtractLinks(tt.body)
			if len(links) != tt.want {
				t.Errorf("link count = %d, want %d", len(links), tt.want)
			}
			for i, slug := range tt.slugs {
				if i < len(links) && links[i].ToSlug != slug {
					t.Errorf("link[%d].to_slug = %q, want %q", i, links[i].ToSlug, slug)
				}
			}
		})
	}
}

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		slug    string
		wantErr bool
	}{
		{"ab", false},
		{"mcp-security", false},
		{"a2a-protocols", false},
		{"topic123", false},
		{"a", true},           // too short
		{"", true},            // empty
		{"A-B", true},         // uppercase
		{"-abc", true},        // starts with hyphen
		{"abc-", true},        // ends with hyphen
		{"a b c", true},       // spaces
		{"a_b", true},         // underscores
		{"ab!c", true},        // special chars
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			err := ValidateSlug(tt.slug)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSlug(%q) error = %v, wantErr %v", tt.slug, err, tt.wantErr)
			}
		})
	}
}

func TestGetMapOfContent(t *testing.T) {
	db := newTestDB(t)
	store := NewStore(db)
	ctx := context.Background()

	// Create a hub (article with 2+ backlinks)
	store.CreateArticle(ctx, "hub-article", "Hub", "I am a hub", "agent-a")
	store.CreateArticle(ctx, "spoke-one", "Spoke 1", "Links to [[hub-article]]", "agent-a")
	store.CreateArticle(ctx, "spoke-two", "Spoke 2", "Also links to [[hub-article]]", "agent-b")

	// Create an orphan (no links in or out)
	store.CreateArticle(ctx, "orphan-article", "Orphan", "I am alone", "agent-a")

	// Create a wanted article reference (referenced but not created)
	store.CreateArticle(ctx, "has-wanted", "Has Wanted", "See [[nonexistent-topic]]", "agent-a")

	moc, err := store.GetMapOfContent(ctx)
	if err != nil {
		t.Fatalf("get map: %v", err)
	}

	if moc.Total != 5 {
		t.Errorf("total = %d, want 5", moc.Total)
	}

	// Check hubs
	if len(moc.Hubs) != 1 {
		t.Errorf("hubs count = %d, want 1", len(moc.Hubs))
	} else if moc.Hubs[0].Slug != "hub-article" {
		t.Errorf("hub slug = %q, want %q", moc.Hubs[0].Slug, "hub-article")
	} else if moc.Hubs[0].BacklinkCount != 2 {
		t.Errorf("hub backlink count = %d, want 2", moc.Hubs[0].BacklinkCount)
	}

	// Check orphans
	if len(moc.Orphans) != 1 {
		t.Errorf("orphans count = %d, want 1", len(moc.Orphans))
	} else if moc.Orphans[0].Slug != "orphan-article" {
		t.Errorf("orphan slug = %q, want %q", moc.Orphans[0].Slug, "orphan-article")
	}

	// Check wanted
	if len(moc.Wanted) != 1 {
		t.Errorf("wanted count = %d, want 1", len(moc.Wanted))
	} else if moc.Wanted[0].Slug != "nonexistent-topic" {
		t.Errorf("wanted slug = %q, want %q", moc.Wanted[0].Slug, "nonexistent-topic")
	} else if moc.Wanted[0].ReferencedCount != 1 {
		t.Errorf("wanted ref count = %d, want 1", moc.Wanted[0].ReferencedCount)
	}

	// All articles listed
	if len(moc.Articles) != 5 {
		t.Errorf("articles count = %d, want 5", len(moc.Articles))
	}
}

func TestWordCount(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  spaces  everywhere  ", 2},
		{"one\ttwo\nthree", 3},
	}
	for _, tt := range tests {
		got := WordCount(tt.input)
		if got != tt.want {
			t.Errorf("WordCount(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", `"hello"`},
		{"hello world", `"hello" "world"`},
		{"to from and", `"to" "from" "and"`},
		{`"quoted"`, `"quoted"`},
		{`"quoted" unquoted`, `"quoted" "unquoted"`},
	}
	for _, tt := range tests {
		got := sanitizeFTS5Query(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
