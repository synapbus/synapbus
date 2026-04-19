# Phase 1 Data Model — Plugin System

## Entities

### Plugin (in-memory, code-defined)

Not persisted directly; it is a Go value constructed by a factory.

| Field | Type | Notes |
|---|---|---|
| Name | string | Unique, lowercased, snake-case. Panic on duplicate at registry build time. |
| Version | string | Semver ("0.1.0"). Informational only in this feature. |
| Stability | string | "stable" / "beta" / "experimental". Defaults to "stable". Non-stable emits a log warning but is not gated. |

### Plugin Registry (in-memory, one per process)

Assembled at boot from `defaultPlugins()` filtered by config.

| Field | Type | Notes |
|---|---|---|
| plugins | `map[string]*entry` | Name → entry, panic on duplicate. |
| order | `[]string` | Registration order, used for migrate/init/start sequence. |

Per-entry `struct` fields:

| Field | Type | Notes |
|---|---|---|
| plugin | `Plugin` | The plugin value. |
| config | `json.RawMessage` | From YAML sub-tree. |
| enabled | `bool` | From YAML `plugins.<name>.enabled`. Default false unless the plugin has a default-enabled hint. |
| status | `Status` | `registered` → `migrated` → `initialized` → `started` \| `failed` \| `disabled` |
| error | `error` | Populated when status == `failed`. |
| startedAt | `time.Time` | Populated on transition to `started`. |

### Plugin Migration Record (SQLite, core table)

Table `plugin_migrations`, created by core migration `000_initial.sql`.

| Column | Type | Notes |
|---|---|---|
| plugin | TEXT | Plugin name. PK (plugin, version). |
| version | INTEGER | Plugin-local migration number, monotonically increasing within plugin. |
| name | TEXT | Human-readable slug ("001_initial"). |
| applied_at | DATETIME | Default `CURRENT_TIMESTAMP`. |
| checksum | TEXT | SHA-256 of the migration SQL; compared on re-apply to detect drift. |

### Plugin-owned tables (example: wiki)

The wiki plugin's migration `001_initial.sql` (executed against the same SQLite handle as core):

```sql
CREATE TABLE IF NOT EXISTS plugin_wiki_articles (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    slug         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    body         TEXT NOT NULL,
    revision     INTEGER NOT NULL DEFAULT 1,
    word_count   INTEGER NOT NULL DEFAULT 0,
    created_by   TEXT,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_by   TEXT,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_wiki_backlinks (
    from_slug TEXT NOT NULL,
    to_slug   TEXT NOT NULL,
    PRIMARY KEY (from_slug, to_slug)
);

CREATE INDEX IF NOT EXISTS idx_plugin_wiki_articles_slug
    ON plugin_wiki_articles(slug);
```

Tables are prefixed `plugin_wiki_*` for audit / lint clarity.

### Host (in-memory, one per plugin per process)

Constructed by the registry when calling `Plugin.Init(ctx, host)`. Not persisted.

| Field | Type | Purpose |
|---|---|---|
| Logger | `*slog.Logger` | Pre-tagged `plugin=<name>`. |
| DB | `*sqlx.DB` | Shared core handle. |
| Messaging | `messaging.API` | Send DMs / channel messages. |
| Channels | `channels.API` | CRUD, post, reactions. |
| Attachments | `attachments.Store` | CAS blob API. |
| Search | `search.Index` | Read-only semantic + FTS. |
| Secrets | `secrets.Scoped` | Scoped to this plugin only. |
| Events | `eventbus.Bus` | Subscribe / publish. |
| Config | `json.RawMessage` | This plugin's YAML sub-tree. |
| DataDir | `string` | `<--data>/plugins/<name>/`, pre-created. |
| Tracer | `trace.Tracer` | OTel tracer scoped to plugin. |
| Metrics | `prometheus.Registerer` | Sub-registry scoped to plugin. |
| DefaultOwner | `*users.User` | For failure notifications. Read-only snapshot. |

### Plugin Status (runtime JSON, served by `/api/plugins/status`)

```json
{
  "plugins": [
    {
      "name": "wiki",
      "version": "0.1.0",
      "stability": "stable",
      "enabled": true,
      "status": "started",
      "started_at": "2026-04-19T10:30:12Z",
      "error": null,
      "capabilities": ["migrations", "mcp_tools", "actions", "http_routes", "web_panel", "config_schema"],
      "tools_registered": ["create_article", "get_article", "list_articles", "update_article", "get_backlinks"],
      "migration_versions": [1]
    },
    {
      "name": "demo_broken",
      "version": "0.0.1",
      "stability": "experimental",
      "enabled": true,
      "status": "failed",
      "started_at": null,
      "error": "Init: missing required config field 'token'",
      "capabilities": ["config_schema"],
      "tools_registered": [],
      "migration_versions": []
    }
  ]
}
```

### Backup Archive (filesystem artifact)

Produced by `scripts/backup-kubic.sh`. Structure:

```
synapbus-backup-YYYY-MM-DDTHH-MM-SSZ.tar.gz
├── manifest.json              # name + SHA-256 + size of each entry
├── synapbus.db                # SQLite main file
├── synapbus.db-wal            # SQLite WAL (if present)
├── attachments/               # CAS directory
│   └── <hash>/<rest>          # existing structure preserved
├── secrets.key                # master key (mode 0600)
└── hnsw.idx                   # vector index snapshot
```

`manifest.json` example:

```json
{
  "created_at": "2026-04-19T11:00:00Z",
  "synapbus_version": "0.12.3+pre-plugin-refactor",
  "source_host": "hub.synapbus.dev",
  "entries": [
    {"path": "synapbus.db", "sha256": "…", "bytes": 48_218_112},
    {"path": "secrets.key", "sha256": "…", "bytes": 32},
    …
  ]
}
```

## State transitions

```
Plugin lifecycle:
  registered → migrated → initialized → started     (happy path)
                 │            │            │
                 └── failed ──┴── failed ──┴── failed (on any error)

Disabled plugins stay in:
  registered → disabled
```

Transitions fire `EventBus` events on `plugin.status.changed` with `{name, from, to, error?}`.

## Validation rules (derived from FRs)

- Plugin name MUST match `^[a-z][a-z0-9_]{1,31}$`. (Prevents path traversal, YAML quirks, table name explosions.)
- Duplicate plugin name → panic at `defaultPlugins()` processing (FR-004).
- Duplicate tool name across plugins → panic during `Init` phase (FR-022).
- Plugin tables not prefixed `plugin_<name>_` → migration refused at apply time, plugin marked failed (FR-010).
- `HasConfigSchema` schema validation failure → plugin marked failed with the validator diagnostic (edge case: invalid config references).
- Cross-plugin secret access attempt → `secrets.Scoped` returns `ErrNotFound` deterministically; FR-007 + SC-006.
