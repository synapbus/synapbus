# Quickstart — Write your first SynapBus plugin

Goal: add a "hello" plugin that exposes an MCP action, a REST route, a Web UI panel, and a tiny migration — in under 20 minutes.

## 1. Create the package

```
internal/plugins/hello/
├── plugin.go
├── schema/
│   └── 001_initial.sql
├── ui/
│   └── index.html
└── plugin_test.go
```

## 2. Write the plugin

`internal/plugins/hello/plugin.go`:

```go
package hello

import (
    "context"
    _ "embed"
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/synapbus/synapbus/internal/plugin"
)

//go:embed schema/001_initial.sql
var migration001 string

//go:embed ui/index.html
var panelHTML []byte

type HelloPlugin struct{ host plugin.Host }

func New() *HelloPlugin { return &HelloPlugin{} }

func (p *HelloPlugin) Name() string    { return "hello" }
func (p *HelloPlugin) Version() string { return "0.1.0" }

func (p *HelloPlugin) Init(ctx context.Context, host plugin.Host) error {
    p.host = host
    return nil
}

func (p *HelloPlugin) Migrations() []plugin.Migration {
    return []plugin.Migration{
        {Version: 1, Name: "001_initial", SQL: migration001},
    }
}

func (p *HelloPlugin) Actions() []plugin.ActionRegistration {
    return []plugin.ActionRegistration{{
        Name: "say_hello",
        Description: "Return a greeting for the given name.",
        InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
        RequiredScope: plugin.ScopeRead,
        Handler: p.sayHello,
    }}
}

func (p *HelloPlugin) sayHello(ctx context.Context, args map[string]any) (any, error) {
    name, _ := args["name"].(string)
    if name == "" { name = "world" }
    return map[string]string{"greeting": "hello, " + name}, nil
}

func (p *HelloPlugin) RegisterRoutes(r chi.Router) {
    r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
        _, _ = w.Write([]byte(`{"ok":true}`))
        w.Header().Set("Content-Type", "application/json")
    })
}

func (p *HelloPlugin) WebPanels() []plugin.PanelManifest {
    return []plugin.PanelManifest{{ID: "hello", Title: "Hello", Icon: "hand", Route: "/ui/plugins/hello", Scope: "member"}}
}

func (p *HelloPlugin) PanelHandler() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        _, _ = w.Write(panelHTML)
    })
}
```

## 3. Write the migration

`internal/plugins/hello/schema/001_initial.sql`:

```sql
CREATE TABLE IF NOT EXISTS plugin_hello_greetings (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## 4. Write the UI panel

`internal/plugins/hello/ui/index.html`:

```html
<!doctype html>
<html><head><title>Hello</title></head>
<body><h1>Hello, plugin world</h1></body></html>
```

## 5. Register it

`cmd/synapbus/plugins.go`:

```go
package main

import (
    "github.com/synapbus/synapbus/internal/plugin"
    "github.com/synapbus/synapbus/internal/plugins/hello"
    "github.com/synapbus/synapbus/internal/plugins/wiki"
)

func defaultPlugins() []plugin.Plugin {
    return []plugin.Plugin{
        wiki.New(),
        hello.New(),          // ← one line
    }
}
```

## 6. Write the smoke test

`internal/plugins/hello/plugin_test.go`:

```go
package hello_test

import (
    "testing"

    "github.com/synapbus/synapbus/internal/plugin/plugintest"
    "github.com/synapbus/synapbus/internal/plugins/hello"
)

func TestHelloPlugin(t *testing.T) {
    plugintest.Run(t, hello.New())
}
```

## 7. Enable in config

`synapbus.yaml`:

```yaml
plugins:
  wiki:  { enabled: true }
  hello: { enabled: true }
```

## 8. Run

```
make build
./synapbus serve --data ./data
```

- `curl http://localhost:8080/api/plugins/status | jq` — you see `hello` status=started.
- `curl http://localhost:8080/api/plugins/hello/ping` — returns `{"ok":true}`.
- Open `http://localhost:8080/ui/plugins/hello` in a browser — renders the HTML.
- Call `say_hello` via MCP: `mcp execute "call('say_hello', {name: 'you'})"` → `{"greeting":"hello, you"}`.
- Flip to `enabled: false`, send SIGHUP — everything above goes away cleanly.

Total new code: ~60 lines of Go. No core code modified.
