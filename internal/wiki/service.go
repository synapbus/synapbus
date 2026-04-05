package wiki

import (
	"context"
	"database/sql"
	"log/slog"
)

// Service provides business logic for wiki articles.
type Service struct {
	store  *Store
	logger *slog.Logger
}

// NewService creates a new wiki service.
func NewService(db *sql.DB) *Service {
	return &Service{
		store:  NewStore(db),
		logger: slog.Default().With("component", "wiki"),
	}
}

// CreateArticle creates a new wiki article.
func (s *Service) CreateArticle(ctx context.Context, slug, title, body, author string) (*Article, error) {
	s.logger.Info("creating article", "slug", slug, "author", author)
	return s.store.CreateArticle(ctx, slug, title, body, author)
}

// GetArticle retrieves an article by slug.
func (s *Service) GetArticle(ctx context.Context, slug string) (*Article, error) {
	return s.store.GetArticle(ctx, slug)
}

// UpdateArticle updates an article's body and/or title.
func (s *Service) UpdateArticle(ctx context.Context, slug, title, body, author string) (*Article, error) {
	s.logger.Info("updating article", "slug", slug, "author", author)
	return s.store.UpdateArticle(ctx, slug, title, body, author)
}

// ListArticles lists or searches wiki articles.
func (s *Service) ListArticles(ctx context.Context, query string, limit int) ([]ArticleSummary, error) {
	return s.store.ListArticles(ctx, query, limit)
}

// GetRevisions returns the revision history for an article.
func (s *Service) GetRevisions(ctx context.Context, slug string) ([]ArticleRevision, error) {
	return s.store.GetRevisions(ctx, slug)
}

// GetBacklinks returns articles linking to the given slug.
func (s *Service) GetBacklinks(ctx context.Context, slug string) ([]ArticleSummary, error) {
	return s.store.GetBacklinks(ctx, slug)
}

// GetOutgoingLinks returns links from the given article.
func (s *Service) GetOutgoingLinks(ctx context.Context, slug string) ([]ArticleLink, error) {
	return s.store.GetOutgoingLinks(ctx, slug)
}

// GetMapOfContent returns the wiki knowledge graph overview.
func (s *Service) GetMapOfContent(ctx context.Context) (*MapOfContent, error) {
	return s.store.GetMapOfContent(ctx)
}
