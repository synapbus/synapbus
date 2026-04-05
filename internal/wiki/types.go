package wiki

import "time"

// Article represents a wiki article with its current state.
type Article struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	CreatedBy string    `json:"created_by"`
	UpdatedBy string    `json:"updated_by"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Computed fields
	WordCount     int              `json:"word_count,omitempty"`
	OutgoingLinks []ArticleLink    `json:"outgoing_links,omitempty"`
	Backlinks     []ArticleSummary `json:"backlinks,omitempty"`
}

// ArticleSummary is a lightweight article representation for lists.
type ArticleSummary struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Revision  int       `json:"revision"`
	WordCount int       `json:"word_count"`
}

// ArticleRevision represents a single revision of an article.
type ArticleRevision struct {
	ID        int64     `json:"id"`
	Revision  int       `json:"revision"`
	Body      string    `json:"body,omitempty"`
	ChangedBy string    `json:"changed_by"`
	CreatedAt time.Time `json:"created_at"`
	WordCount int       `json:"word_count"`
}

// ArticleLink represents a [[backlink]] from one article to another.
type ArticleLink struct {
	FromSlug    string `json:"from_slug"`
	ToSlug      string `json:"to_slug"`
	DisplayText string `json:"display_text,omitempty"`
}

// MapOfContent provides a bird's-eye view of the wiki knowledge graph.
type MapOfContent struct {
	Hubs     []ArticleWithLinks `json:"hubs"`
	Articles []ArticleSummary   `json:"articles"`
	Orphans  []ArticleSummary   `json:"orphans"`
	Wanted   []WantedArticle    `json:"wanted"`
	Total    int                `json:"total"`
}

// ArticleWithLinks is a summary enriched with backlink count.
type ArticleWithLinks struct {
	ArticleSummary
	BacklinkCount int `json:"backlink_count"`
}

// WantedArticle is a slug referenced via [[slug]] but not yet created.
type WantedArticle struct {
	Slug            string `json:"slug"`
	ReferencedCount int    `json:"referenced_count"`
}
