package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/wiki"
)

// WikiHandler handles REST API requests for wiki articles.
type WikiHandler struct {
	wikiService *wiki.Service
	logger      *slog.Logger
}

// NewWikiHandler creates a new wiki handler.
func NewWikiHandler(svc *wiki.Service) *WikiHandler {
	return &WikiHandler{
		wikiService: svc,
		logger:      slog.Default().With("component", "api.wiki"),
	}
}

// ListArticles handles GET /api/wiki/articles?q=...&limit=50
func (h *WikiHandler) ListArticles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	articles, err := h.wikiService.ListArticles(r.Context(), query, limit)
	if err != nil {
		h.logger.Error("list articles failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", "Failed to list articles"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"articles": articles,
		"count":    len(articles),
	})
}

// GetArticle handles GET /api/wiki/articles/{slug}
func (h *WikiHandler) GetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "Slug is required"))
		return
	}

	article, err := h.wikiService.GetArticle(r.Context(), slug)
	if err != nil {
		h.logger.Error("get article failed", "slug", slug, "error", err)
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Article not found"))
		return
	}

	writeJSON(w, http.StatusOK, article)
}

// GetHistory handles GET /api/wiki/articles/{slug}/history
func (h *WikiHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("validation_error", "Slug is required"))
		return
	}

	revisions, err := h.wikiService.GetRevisions(r.Context(), slug)
	if err != nil {
		h.logger.Error("get history failed", "slug", slug, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", "Failed to get history"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":      slug,
		"revisions": revisions,
		"count":     len(revisions),
	})
}

// GetMap handles GET /api/wiki/map
func (h *WikiHandler) GetMap(w http.ResponseWriter, r *http.Request) {
	moc, err := h.wikiService.GetMapOfContent(r.Context())
	if err != nil {
		h.logger.Error("get map failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", "Failed to get map"))
		return
	}

	writeJSON(w, http.StatusOK, moc)
}
