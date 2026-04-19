// Package demo is a canonical demonstration plugin exercising every
// HasX capability in the plugin framework. It persists "notes" into a
// namespaced table plugin_demo_notes, exposes bridged actions, serves
// a REST route, registers a Web UI panel, runs a background goroutine,
// and carries a config schema.
package demo

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/synapbus/synapbus/internal/plugin"
)

//go:embed schema/001_initial.sql
var migration001 string

//go:embed ui/index.html
var panelHTML []byte

// Plugin implements plugin.Plugin + every relevant HasX interface.
type Plugin struct {
	host     plugin.Host
	cfg      Config
	stopBg   context.CancelFunc
	bgWG     sync.WaitGroup
	bgSeen   int
	bgSeenMu sync.Mutex
}

type Config struct {
	// MaxNotes caps the notes table size (0 = unlimited).
	MaxNotes int `json:"max_notes"`
	// BackgroundSweepEvery controls how often the background goroutine ticks.
	// Parsed via time.ParseDuration so YAML can use "1h", "30s", etc.
	BackgroundSweepEvery string `json:"background_sweep_every"`

	sweepEvery time.Duration
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string      { return "demo" }
func (p *Plugin) Version() string   { return "0.1.0" }
func (p *Plugin) Stability() string { return "beta" }

func (p *Plugin) Init(ctx context.Context, host plugin.Host) error {
	p.host = host
	if err := p.decodeConfig(host.Config); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	host.Logger.Info("demo plugin initialized", "max_notes", p.cfg.MaxNotes)
	return nil
}

func (p *Plugin) decodeConfig(raw json.RawMessage) error {
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p.cfg); err != nil {
			return err
		}
	}
	if p.cfg.BackgroundSweepEvery == "" {
		p.cfg.sweepEvery = time.Hour
	} else {
		d, err := time.ParseDuration(p.cfg.BackgroundSweepEvery)
		if err != nil {
			return fmt.Errorf("background_sweep_every: %w", err)
		}
		p.cfg.sweepEvery = d
	}
	return nil
}

// --- HasMigrations ---

func (p *Plugin) Migrations() []plugin.Migration {
	return []plugin.Migration{{Version: 1, Name: "001_initial", SQL: migration001}}
}

// --- HasConfigSchema ---

func (p *Plugin) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{
        "type": "object",
        "properties": {
            "max_notes": {"type": "integer", "minimum": 0, "description": "Cap on notes count; 0 = unlimited"},
            "background_sweep_every": {"type": "string", "description": "Go duration string for background sweep (e.g. \"1h\")"}
        }
    }`)
}

// --- HasActions ---

func (p *Plugin) Actions() []plugin.ActionRegistration {
	return []plugin.ActionRegistration{
		{
			Name:          "create_note",
			Description:   "Create a new demo note.",
			RequiredScope: plugin.ScopeWrite,
			InputSchema: json.RawMessage(`{
                "type":"object",
                "required":["slug","title"],
                "properties":{
                    "slug":{"type":"string"},
                    "title":{"type":"string"},
                    "body":{"type":"string"}
                }
            }`),
			Handler: p.createNote,
		},
		{
			Name:          "get_note",
			Description:   "Fetch a note by slug.",
			RequiredScope: plugin.ScopeRead,
			Handler:       p.getNote,
		},
		{
			Name:          "list_notes",
			Description:   "List all notes.",
			RequiredScope: plugin.ScopeRead,
			Handler:       p.listNotes,
		},
	}
}

// --- HasHTTPRoutes ---

func (p *Plugin) RegisterRoutes(r plugin.Router) {
	r.Get("/notes", p.httpListNotes)
	r.Post("/notes", p.httpCreateNote)
	r.Get("/notes/", p.httpListNotes) // tolerate trailing slash
}

// --- HasWebPanels ---

func (p *Plugin) WebPanels() []plugin.PanelManifest {
	return []plugin.PanelManifest{{
		ID: "demo", Title: "Demo Notes", Icon: "file-text",
		Route: "/ui/plugins/demo", Scope: "member",
	}}
}

func (p *Plugin) PanelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(panelHTML)
	})
}

// --- HasLifecycle ---

func (p *Plugin) Start(ctx context.Context) error {
	bgCtx, cancel := context.WithCancel(context.Background())
	p.stopBg = cancel
	p.bgWG.Add(1)
	go p.backgroundLoop(bgCtx)
	return nil
}

func (p *Plugin) Shutdown(ctx context.Context) error {
	if p.stopBg != nil {
		p.stopBg()
	}
	done := make(chan struct{})
	go func() { p.bgWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		return errors.New("demo: shutdown timed out")
	}
	return nil
}

func (p *Plugin) backgroundLoop(ctx context.Context) {
	defer p.bgWG.Done()
	ticker := time.NewTicker(p.cfg.sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.bgSeenMu.Lock()
			p.bgSeen++
			p.bgSeenMu.Unlock()
		}
	}
}

// BgSweepCount exposes the background loop's tick count for tests.
func (p *Plugin) BgSweepCount() int {
	p.bgSeenMu.Lock()
	defer p.bgSeenMu.Unlock()
	return p.bgSeen
}

// --- action handlers ---

type note struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (p *Plugin) createNote(ctx context.Context, args map[string]any) (any, error) {
	slug, _ := args["slug"].(string)
	title, _ := args["title"].(string)
	body, _ := args["body"].(string)
	if slug == "" || title == "" {
		return nil, errors.New("slug and title are required")
	}
	if p.cfg.MaxNotes > 0 {
		var count int
		if err := p.host.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_demo_notes`).Scan(&count); err != nil {
			return nil, fmt.Errorf("count notes: %w", err)
		}
		if count >= p.cfg.MaxNotes {
			return nil, fmt.Errorf("max_notes=%d reached", p.cfg.MaxNotes)
		}
	}
	res, err := p.host.DB.ExecContext(ctx,
		`INSERT INTO plugin_demo_notes (slug, title, body) VALUES (?, ?, ?)`,
		slug, title, body)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("note with slug %q already exists", slug)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return map[string]any{"id": id, "slug": slug, "title": title}, nil
}

func (p *Plugin) getNote(ctx context.Context, args map[string]any) (any, error) {
	slug, _ := args["slug"].(string)
	if slug == "" {
		return nil, errors.New("slug is required")
	}
	var n note
	err := p.host.DB.QueryRowContext(ctx,
		`SELECT id, slug, title, body, created_at, updated_at FROM plugin_demo_notes WHERE slug=?`, slug,
	).Scan(&n.ID, &n.Slug, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("note %q not found", slug)
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (p *Plugin) listNotes(ctx context.Context, args map[string]any) (any, error) {
	rows, err := p.host.DB.QueryContext(ctx,
		`SELECT id, slug, title, body, created_at, updated_at FROM plugin_demo_notes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.ID, &n.Slug, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return map[string]any{"notes": out, "count": len(out)}, nil
}

// --- http handlers ---

func (p *Plugin) httpListNotes(w http.ResponseWriter, r *http.Request) {
	result, err := p.listNotes(r.Context(), nil)
	writeJSON(w, result, err)
}

func (p *Plugin) httpCreateNote(w http.ResponseWriter, r *http.Request) {
	var args map[string]any
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		writeJSON(w, nil, fmt.Errorf("decode: %w", err))
		return
	}
	result, err := p.createNote(r.Context(), args)
	writeJSON(w, result, err)
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
