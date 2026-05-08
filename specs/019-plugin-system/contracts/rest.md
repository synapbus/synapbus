# Contract — REST endpoints

## Core endpoints (added by this feature)

### `GET /api/plugins/status`

Returns the registry state for all plugins.

**Auth**: session cookie or `Authorization: Bearer <api-key>` (any authenticated user).

**Response 200**:
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
      "capabilities": ["migrations", "actions", "http_routes", "web_panel", "config_schema"],
      "tools_registered": ["create_article","get_article","list_articles","update_article","get_backlinks"],
      "migration_versions": [1]
    }
  ]
}
```

Error shapes: standard `{"error":"…"}` with appropriate HTTP status.

### `POST /api/admin/plugins/{name}/enable` (admin only)

Sets `plugins.<name>.enabled: true` in `synapbus.yaml` and sends self-SIGHUP. Returns 200 with `{"restart": true}`; operator observes the graceful restart.

NOTE: admin endpoints are under `/api/admin/plugins/` rather than `/api/plugins/` to avoid URL collisions with per-plugin routes mounted at `/api/plugins/<name>/`.

### `POST /api/admin/plugins/{name}/disable` (admin only)

Sets `plugins.<name>.enabled: false` in `synapbus.yaml` and sends self-SIGHUP.

### `GET /api/plugins/{name}/schema` (any auth)

Returns the plugin's `HasConfigSchema().ConfigSchema()` JSON Schema, or 404 if the plugin does not implement it.

## Plugin-owned endpoints

Plugins mount routes via `HasHTTPRoutes.RegisterRoutes(r chi.Router)`. The registry mounts the returned router under `/api/plugins/<name>/`. Plugins MUST NOT register routes outside this namespace.

## UI panel endpoints

Plugins with `HasWebPanels` MUST serve assets under `/ui/plugins/<name>/`. The registry mounts `plugin.PanelHandler()` under this prefix. The plugin's `index.html` is served at `/ui/plugins/<name>/`.

## Core shell changes

The existing `/ui/*` shell (Svelte) fetches `/api/plugins/status` on load and renders plugin panels in the nav based on the `capabilities` including `"web_panel"` and the `panels` field derived from `HasWebPanels().WebPanels()`. Clicking a panel entry in the nav opens the route `/ui/plugins/<name>` in an iframe in the content pane.
