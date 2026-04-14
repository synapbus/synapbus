package main

const reportTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Doc-gardener run — {{.Goal.Title}}</title>
<style>
  :root {
    --bg:          #0b0f17;
    --panel:       #121826;
    --panel-alt:   #1a2233;
    --border:      #232c42;
    --text:        #e6ebf5;
    --muted:       #8893a8;
    --accent:      #7dd3fc;
    --accent-dim:  #38bdf8;
    --ok:          #4ade80;
    --warn:        #fbbf24;
    --err:         #f87171;
    --chip:        #2a364f;
  }
  * { box-sizing: border-box; }
  html, body { margin:0; padding:0; background:var(--bg); color:var(--text); font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; font-size:14px; line-height:1.5; }
  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }
  .container { max-width: 1200px; margin: 0 auto; padding: 32px; }
  h1 { font-size: 28px; margin: 0 0 4px 0; }
  h2 { font-size: 18px; color: var(--accent); margin: 32px 0 12px 0; border-bottom: 1px solid var(--border); padding-bottom: 6px; }
  h3 { font-size: 14px; color: var(--muted); margin: 12px 0 6px 0; text-transform: uppercase; letter-spacing: 0.05em; }
  .subtitle { color: var(--muted); font-size: 14px; margin: 0 0 18px 0; }
  .card { background: var(--panel); border: 1px solid var(--border); border-radius: 8px; padding: 16px; margin: 12px 0; }
  .grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  .grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
  .metric { background: var(--panel-alt); border-radius: 6px; padding: 12px 16px; }
  .metric .label { color: var(--muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em; }
  .metric .value { font-size: 22px; font-weight: 600; margin-top: 4px; font-variant-numeric: tabular-nums; }
  .status-badge { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; }
  .status-badge.done { background: rgba(74,222,128,0.15); color: var(--ok); border: 1px solid rgba(74,222,128,0.4); }
  .status-badge.failed { background: rgba(248,113,113,0.15); color: var(--err); border: 1px solid rgba(248,113,113,0.4); }
  .status-badge.in_progress, .status-badge.claimed, .status-badge.awaiting_verification { background: rgba(251,191,36,0.15); color: var(--warn); border: 1px solid rgba(251,191,36,0.4); }
  .status-badge.approved, .status-badge.proposed, .status-badge.active, .status-badge.draft, .status-badge.completed, .status-badge.paused, .status-badge.cancelled, .status-badge.stuck { background: var(--chip); color: var(--text); border: 1px solid var(--border); }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--border); font-variant-numeric: tabular-nums; }
  th { color: var(--muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; }
  tr:last-child td { border-bottom: none; }
  .tree { list-style: none; padding: 0; margin: 0; }
  .tree li { margin: 6px 0; }
  .tree-node { background: var(--panel-alt); border: 1px solid var(--border); border-left: 4px solid var(--border); border-radius: 6px; padding: 10px 14px; }
  .tree-node.done { border-left-color: var(--ok); }
  .tree-node.failed { border-left-color: var(--err); }
  .tree-node.in_progress, .tree-node.awaiting_verification, .tree-node.claimed { border-left-color: var(--warn); }
  .tree-node .title-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
  .tree-node .title { font-weight: 600; }
  .tree-node .meta { color: var(--muted); font-size: 12px; margin-top: 4px; }
  .tree ul.children { list-style: none; padding-left: 20px; border-left: 1px dashed var(--border); margin-top: 8px; }
  .agent-card { background: var(--panel-alt); border: 1px solid var(--border); border-radius: 6px; padding: 14px; }
  .agent-card .name { font-weight: 700; font-size: 15px; }
  .agent-card .hash { font-family: "SF Mono", Menlo, monospace; font-size: 11px; color: var(--muted); margin-top: 2px; }
  .agent-card .rep-bar { height: 6px; background: var(--border); border-radius: 3px; overflow: hidden; margin: 8px 0 4px 0; }
  .agent-card .rep-fill { height: 100%; background: linear-gradient(90deg, var(--accent-dim), var(--accent)); }
  .agent-card .tool-scope { margin-top: 8px; }
  .chip { display: inline-block; background: var(--chip); color: var(--text); font-size: 11px; padding: 2px 8px; border-radius: 10px; margin: 2px 4px 2px 0; font-family: "SF Mono", Menlo, monospace; }
  .timeline { position: relative; padding-left: 24px; border-left: 2px solid var(--border); }
  .timeline-item { position: relative; padding: 10px 14px; margin: 6px 0; background: var(--panel-alt); border: 1px solid var(--border); border-radius: 6px; }
  .timeline-item::before { content: ""; position: absolute; left: -30px; top: 16px; width: 10px; height: 10px; background: var(--accent); border-radius: 50%; box-shadow: 0 0 0 3px var(--bg); }
  .timeline-item .when { color: var(--muted); font-size: 11px; font-family: "SF Mono", Menlo, monospace; }
  .timeline-item .actor { color: var(--accent); font-weight: 600; margin-left: 6px; }
  .timeline-item .body { margin-top: 4px; }
  .footer { color: var(--muted); font-size: 12px; text-align: center; margin: 40px 0 0 0; padding-top: 20px; border-top: 1px solid var(--border); }
  .artifact { background: var(--panel-alt); border-left: 4px solid var(--accent); padding: 12px 16px; margin: 8px 0; border-radius: 4px; font-family: "SF Mono", Menlo, monospace; font-size: 12px; white-space: pre-wrap; }
  .artifact-meta { color: var(--muted); font-size: 11px; margin-bottom: 4px; }
</style>
</head>
<body>
<div class="container">
  <h1>{{.Goal.Title}}</h1>
  <p class="subtitle">Goal #{{.Goal.ID}} · slug <code>{{.Goal.Slug}}</code> · owner <strong>{{.Goal.Owner}}</strong> · backing channel <code>#{{.Goal.ChannelName}}</code> · <span class="status-badge {{.Goal.Status}}">{{.Goal.Status}}</span></p>

  <div class="grid-3">
    <div class="metric">
      <div class="label">Spend</div>
      <div class="value">{{dollars .TotalDollarsC}}</div>
      <div class="label">of {{dollars .BudgetDollarsC}} budget · {{pct .SpendPctDollar}}% used</div>
    </div>
    <div class="metric">
      <div class="label">Tokens</div>
      <div class="value">{{.TotalTokens}}</div>
      <div class="label">of {{.BudgetTokens}} budget</div>
    </div>
    <div class="metric">
      <div class="label">Agents spawned</div>
      <div class="value">{{len .Agents}}</div>
      <div class="label">including coordinator</div>
    </div>
  </div>

  <h2>Goal description</h2>
  <div class="card">
    <p>{{.Goal.Description}}</p>
  </div>

  <h2>Task tree</h2>
  {{template "taskList" .Tree}}

  <h2>Spawned agents</h2>
  <div class="grid-2">
    {{range .Agents}}
    <div class="agent-card">
      <div class="name">{{.DisplayName}} <span style="color:var(--muted); font-weight: 400">({{.Name}})</span></div>
      <div class="hash">config_hash: <code>{{shortHash .ConfigHash}}…</code>
        {{if .ParentAgentName}}· parent: <strong>{{.ParentAgentName}}</strong>{{else}}· root{{end}}
        · depth {{.SpawnDepth}}
      </div>
      <div class="rep-bar"><div class="rep-fill" style="width: {{pct .RollingRep}}%"></div></div>
      <div style="display:flex; justify-content:space-between; font-size:12px; color:var(--muted)">
        <span>Reputation: <strong style="color:var(--text)">{{pct .RollingRep}}%</strong></span>
        <span>{{.EvidenceCount}} evidence row(s)</span>
        <span>Tier: <strong style="color:var(--text)">{{.AutonomyTier}}</strong></span>
      </div>
      <div class="tool-scope">
        {{range .ToolScope}}<span class="chip">{{.}}</span>{{end}}
      </div>
      {{if .SystemPromptFirst}}<div style="color:var(--muted); font-size: 12px; margin-top: 8px; font-style: italic">"{{.SystemPromptFirst}}"</div>{{end}}
    </div>
    {{end}}
  </div>

  <h2>Cost breakdown by billing code</h2>
  <div class="card">
    <table>
      <thead>
        <tr><th>Billing code</th><th>Tasks</th><th>Tokens</th><th>Dollars</th></tr>
      </thead>
      <tbody>
        {{range .BillingBreakdown}}
        <tr>
          <td><code>{{.Code}}</code></td>
          <td>{{.TaskCount}}</td>
          <td>{{.Tokens}}</td>
          <td>{{dollars .DollarsCents}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>

  {{if .Artifacts}}
  <h2>Artifacts posted by specialists</h2>
  {{range .Artifacts}}
  <div class="artifact">
    <div class="artifact-meta">from <strong>{{.From}}</strong> @ {{formatTime .When}}</div>
    {{.Body}}
  </div>
  {{end}}
  {{end}}

  <h2>Timeline</h2>
  <div class="timeline">
    {{range .Timeline}}
    <div class="timeline-item">
      <span class="when">{{formatTime .When}}</span>
      <span class="actor">{{.Actor}}</span>
      <span style="color: var(--muted); font-size: 11px; margin-left: 6px">{{.Kind}}</span>
      <div class="body">{{.Message}}</div>
    </div>
    {{end}}
  </div>

  <div class="footer">
    Generated at {{formatTime .GeneratedAt}} by docgardener · SynapBus feature 018-dynamic-agent-spawning
  </div>
</div>

{{define "taskList"}}
<ul class="tree">
  {{range .}}
  <li>
    <div class="tree-node {{.Status}}">
      <div class="title-row">
        <div>
          <div class="title">{{.Title}}</div>
          <div class="meta">
            #{{.ID}} · depth {{.Depth}}
            {{if .BillingCode}}· <code>{{.BillingCode}}</code>{{end}}
            {{if .Assignee}}· assignee <strong>{{.Assignee}}</strong>{{end}}
            {{if .VerifierKind}}· verifier <code>{{.VerifierKind}}</code>{{end}}
          </div>
        </div>
        <div style="display: flex; align-items: center; gap: 12px; white-space: nowrap;">
          {{if nonZero .SpentDollarsC}}<span style="color: var(--muted); font-size: 12px">{{dollars .SpentDollarsC}} · {{.SpentTokens}} tok</span>{{end}}
          <span class="status-badge {{.Status}}">{{.Status}}</span>
        </div>
      </div>
      {{if .Description}}<div style="color: var(--muted); font-size: 12px; margin-top: 6px;">{{.Description}}</div>{{end}}
    </div>
    {{if .Children}}
    <ul class="children">
      {{template "taskList" .Children}}
    </ul>
    {{end}}
  </li>
  {{end}}
</ul>
{{end}}

</body>
</html>
`
