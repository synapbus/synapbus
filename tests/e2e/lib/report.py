"""HTML report generator for SynapBus E2E test results."""
from __future__ import annotations

import html
import json
import os
import time
from typing import Any, Dict, List, Optional

from .agent_runner import TestResult, AgentRun, ToolCall


def _truncate(s: str, max_len: int = 500) -> str:
    if len(s) <= max_len:
        return s
    return s[:max_len] + "..."


def _json_pretty(data: Any, max_len: int = 500) -> str:
    try:
        raw = json.dumps(data, indent=2, default=str)
    except (TypeError, ValueError):
        raw = str(data)
    return html.escape(_truncate(raw, max_len))


def _status_badge(status: str) -> str:
    colors = {
        "pass": "#10b981",
        "fail": "#ef4444",
        "error": "#f59e0b",
    }
    bg = colors.get(status, "#6b7280")
    return '<span class="badge" style="background:{bg}">{label}</span>'.format(
        bg=bg, label=html.escape(status.upper()))


def _bool_icon(passed: bool) -> str:
    if passed:
        return '<span style="color:#10b981;font-weight:bold">PASS</span>'
    return '<span style="color:#ef4444;font-weight:bold">FAIL</span>'


def _format_duration(seconds: float) -> str:
    if seconds < 1:
        return "{:.0f}ms".format(seconds * 1000)
    return "{:.1f}s".format(seconds)


def _format_tokens(n: int) -> str:
    if n >= 1000:
        return "{:.1f}k".format(n / 1000)
    return str(n)


def _estimate_cost(input_tokens: int, output_tokens: int, model: str) -> float:
    """Estimate cost in USD. Sonnet pricing: $3/M input, $15/M output."""
    if "opus" in model:
        return (input_tokens * 15 + output_tokens * 75) / 1_000_000
    if "haiku" in model:
        return (input_tokens * 0.25 + output_tokens * 1.25) / 1_000_000
    # Default Sonnet
    return (input_tokens * 3 + output_tokens * 15) / 1_000_000


def generate_report(
    results: List[TestResult],
    model: str,
    base_url: str,
    total_duration: float,
    output_path: str,
) -> str:
    """Generate a self-contained HTML report file."""
    passed = sum(1 for r in results if r.status == "pass")
    failed = sum(1 for r in results if r.status == "fail")
    errored = sum(1 for r in results if r.status == "error")
    total_input = sum(r.total_input_tokens for r in results)
    total_output = sum(r.total_output_tokens for r in results)
    total_cost = _estimate_cost(total_input, total_output, model)
    total_tool_calls = sum(len(tc) for r in results for run in r.runs for tc in [run.tool_calls])

    scenarios_html = []
    for idx, result in enumerate(results):
        scenarios_html.append(_render_scenario(result, idx, model))

    report_html = TEMPLATE.format(
        timestamp=time.strftime("%Y-%m-%d %H:%M:%S"),
        model=html.escape(model),
        base_url=html.escape(base_url),
        total=len(results),
        passed=passed,
        failed=failed,
        errored=errored,
        total_duration=_format_duration(total_duration),
        total_input=_format_tokens(total_input),
        total_output=_format_tokens(total_output),
        total_cost="{:.4f}".format(total_cost),
        total_tool_calls=total_tool_calls,
        scenarios="\n".join(scenarios_html),
        pass_rate="{:.0f}".format(100 * passed / len(results)) if results else "0",
    )

    with open(output_path, "w") as f:
        f.write(report_html)

    return output_path


def _render_scenario(result: TestResult, idx: int, model: str) -> str:
    cost = _estimate_cost(result.total_input_tokens, result.total_output_tokens, model)

    # Render verifications
    verifications_html = ""
    if result.verifications:
        rows = []
        for v in result.verifications:
            rows.append(
                '<tr><td>{icon}</td><td>{check}</td><td class="mono">{detail}</td></tr>'.format(
                    icon=_bool_icon(v.get("passed", False)),
                    check=html.escape(v.get("check", "")),
                    detail=html.escape(_truncate(v.get("detail", ""), 200)),
                )
            )
            verifications_html = """
            <h4>Verifications</h4>
            <table class="verification-table">
                <tr><th>Status</th><th>Check</th><th>Detail</th></tr>
                {rows}
            </table>
            """.format(rows="\n".join(rows))

    # Render agent runs
    runs_html_parts = []
    for run in result.runs:
        runs_html_parts.append(_render_agent_run(run))

    # Error section
    error_html = ""
    if result.error:
        error_html = """
        <div class="error-block">
            <h4>Error</h4>
            <pre>{error}</pre>
        </div>
        """.format(error=html.escape(result.error))

    border_color = {"pass": "#10b981", "fail": "#ef4444", "error": "#f59e0b"}.get(
        result.status, "#6b7280")

    return """
    <div class="scenario-card" style="border-left: 4px solid {border_color}" id="scenario-{idx}">
        <div class="scenario-header" onclick="toggleSection('scenario-body-{idx}')">
            <div class="scenario-title">
                {badge} <span>{name}</span>
            </div>
            <div class="scenario-meta">
                <span class="meta-item">{duration}</span>
                <span class="meta-item">{input_tokens} in / {output_tokens} out</span>
                <span class="meta-item">${cost}</span>
                <span class="meta-item">{agent_count} agents</span>
            </div>
        </div>
        <div class="scenario-body" id="scenario-body-{idx}">
            <p class="description">{description}</p>
            <div class="agents-list">Agents: {agents}</div>
            {verifications}
            {error}
            <div class="runs-section">
                <h4>Agent Runs</h4>
                {runs}
            </div>
        </div>
    </div>
    """.format(
        border_color=border_color,
        idx=idx,
        badge=_status_badge(result.status),
        name=html.escape(result.name),
        duration=_format_duration(result.duration),
        input_tokens=_format_tokens(result.total_input_tokens),
        output_tokens=_format_tokens(result.total_output_tokens),
        cost="{:.4f}".format(cost),
        agent_count=len(result.agents),
        description=html.escape(result.description),
        agents=", ".join(html.escape(a) for a in result.agents),
        verifications=verifications_html,
        error=error_html,
        runs="\n".join(runs_html_parts),
    )


def _render_agent_run(run: AgentRun) -> str:
    # Render tool calls
    tc_rows = []
    for tc in run.tool_calls:
        status_cls = "tc-pass" if tc.success else "tc-fail"
        tc_id = "tc-{}-{}".format(id(tc), int(tc.timestamp * 1000))
        tc_rows.append("""
        <div class="tool-call {status_cls}">
            <div class="tc-header" onclick="toggleSection('{tc_id}')">
                <span class="tc-tool">{tool}</span>
                <span class="tc-status">{status}</span>
                <span class="tc-duration">{duration}</span>
            </div>
            <div class="tc-body" id="{tc_id}" style="display:none">
                <div class="tc-section">
                    <strong>Input:</strong>
                    <pre>{input}</pre>
                </div>
                <div class="tc-section">
                    <strong>Output:</strong>
                    <pre>{output}</pre>
                </div>
            </div>
        </div>
        """.format(
            status_cls=status_cls,
            tc_id=tc_id,
            tool=html.escape(tc.tool),
            status="OK" if tc.success else "ERR",
            duration=_format_duration(tc.duration),
            input=_json_pretty(tc.input),
            output=_json_pretty(tc.output),
        ))

    error_html = ""
    if run.error:
        error_html = '<div class="run-error">Error: {}</div>'.format(html.escape(run.error))

    return """
    <div class="agent-run">
        <div class="run-header">
            <span class="agent-name">{agent_name}</span>
            <span class="run-meta">{duration} | {input_tokens} in / {output_tokens} out | {tc_count} tool calls</span>
        </div>
        {error}
        <div class="run-prompt">
            <details>
                <summary>Prompts</summary>
                <div class="prompt-section">
                    <strong>System:</strong>
                    <pre>{system_prompt}</pre>
                </div>
                <div class="prompt-section">
                    <strong>User:</strong>
                    <pre>{user_prompt}</pre>
                </div>
            </details>
        </div>
        <div class="run-response">
            <details>
                <summary>Response</summary>
                <pre>{response}</pre>
            </details>
        </div>
        <div class="tool-calls">
            {tool_calls}
        </div>
    </div>
    """.format(
        agent_name=html.escape(run.agent_name),
        duration=_format_duration(run.duration),
        input_tokens=_format_tokens(run.input_tokens),
        output_tokens=_format_tokens(run.output_tokens),
        tc_count=len(run.tool_calls),
        error=error_html,
        system_prompt=html.escape(_truncate(run.system_prompt, 300)),
        user_prompt=html.escape(_truncate(run.user_prompt, 500)),
        response=html.escape(_truncate(run.response_text, 800)),
        tool_calls="\n".join(tc_rows),
    )


TEMPLATE = """<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>SynapBus E2E Test Report</title>
<style>
:root {{
    --bg: #0f172a;
    --surface: #1e293b;
    --surface2: #334155;
    --text: #e2e8f0;
    --text-dim: #94a3b8;
    --border: #475569;
    --green: #10b981;
    --red: #ef4444;
    --yellow: #f59e0b;
    --blue: #3b82f6;
    --purple: #8b5cf6;
}}

* {{ margin: 0; padding: 0; box-sizing: border-box; }}

body {{
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg);
    color: var(--text);
    line-height: 1.6;
    padding: 2rem;
    max-width: 1200px;
    margin: 0 auto;
}}

h1 {{ font-size: 1.8rem; margin-bottom: 0.5rem; }}
h2 {{ font-size: 1.3rem; margin-bottom: 1rem; color: var(--text-dim); }}
h3 {{ font-size: 1.1rem; margin-bottom: 0.5rem; }}
h4 {{ font-size: 0.95rem; margin: 1rem 0 0.5rem; color: var(--text-dim); }}

.header {{
    text-align: center;
    padding: 2rem 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: 2rem;
}}

.header .subtitle {{
    color: var(--text-dim);
    font-size: 0.9rem;
}}

.dashboard {{
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 1rem;
    margin-bottom: 2rem;
}}

.stat-card {{
    background: var(--surface);
    border-radius: 8px;
    padding: 1.2rem;
    text-align: center;
}}

.stat-card .stat-value {{
    font-size: 2rem;
    font-weight: 700;
}}

.stat-card .stat-label {{
    font-size: 0.8rem;
    color: var(--text-dim);
    text-transform: uppercase;
    letter-spacing: 0.05em;
}}

.stat-value.green {{ color: var(--green); }}
.stat-value.red {{ color: var(--red); }}
.stat-value.yellow {{ color: var(--yellow); }}
.stat-value.blue {{ color: var(--blue); }}
.stat-value.purple {{ color: var(--purple); }}

.filter-bar {{
    display: flex;
    gap: 0.5rem;
    margin-bottom: 1.5rem;
    flex-wrap: wrap;
}}

.filter-btn {{
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.4rem 1rem;
    cursor: pointer;
    font-size: 0.85rem;
    transition: all 0.2s;
}}

.filter-btn:hover {{ background: var(--surface2); }}
.filter-btn.active {{ background: var(--blue); border-color: var(--blue); }}

.scenario-card {{
    background: var(--surface);
    border-radius: 8px;
    margin-bottom: 1rem;
    overflow: hidden;
}}

.scenario-header {{
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1rem 1.2rem;
    cursor: pointer;
    transition: background 0.2s;
    flex-wrap: wrap;
    gap: 0.5rem;
}}

.scenario-header:hover {{ background: var(--surface2); }}

.scenario-title {{
    display: flex;
    align-items: center;
    gap: 0.75rem;
    font-weight: 600;
    font-size: 1rem;
}}

.scenario-meta {{
    display: flex;
    gap: 1rem;
    font-size: 0.8rem;
    color: var(--text-dim);
}}

.meta-item {{
    white-space: nowrap;
}}

.badge {{
    display: inline-block;
    padding: 0.15rem 0.6rem;
    border-radius: 4px;
    font-size: 0.7rem;
    font-weight: 700;
    color: white;
    letter-spacing: 0.05em;
}}

.scenario-body {{
    padding: 0 1.2rem 1.2rem;
    display: none;
}}

.scenario-body.open {{ display: block; }}

.description {{
    color: var(--text-dim);
    font-size: 0.9rem;
    margin-bottom: 0.5rem;
}}

.agents-list {{
    font-size: 0.85rem;
    color: var(--text-dim);
    margin-bottom: 1rem;
    font-family: 'SF Mono', 'Fira Code', monospace;
}}

.verification-table {{
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
    margin-bottom: 1rem;
}}

.verification-table th {{
    text-align: left;
    padding: 0.4rem 0.6rem;
    background: var(--surface2);
    color: var(--text-dim);
    font-weight: 600;
    font-size: 0.75rem;
    text-transform: uppercase;
}}

.verification-table td {{
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid var(--border);
}}

.mono {{ font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.8rem; }}

.error-block {{
    background: rgba(239, 68, 68, 0.1);
    border: 1px solid var(--red);
    border-radius: 6px;
    padding: 1rem;
    margin: 1rem 0;
}}

.error-block pre {{
    color: var(--red);
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 0.8rem;
}}

.agent-run {{
    background: var(--bg);
    border-radius: 6px;
    padding: 1rem;
    margin-bottom: 0.75rem;
}}

.run-header {{
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.5rem;
    flex-wrap: wrap;
    gap: 0.5rem;
}}

.agent-name {{
    font-weight: 700;
    color: var(--purple);
    font-size: 0.95rem;
}}

.run-meta {{
    font-size: 0.8rem;
    color: var(--text-dim);
}}

.run-error {{
    background: rgba(239, 68, 68, 0.1);
    color: var(--red);
    padding: 0.5rem;
    border-radius: 4px;
    font-size: 0.85rem;
    margin-bottom: 0.5rem;
}}

details {{
    margin-bottom: 0.5rem;
}}

summary {{
    cursor: pointer;
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 0.3rem 0;
}}

summary:hover {{ color: var(--text); }}

pre {{
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 0.8rem;
    background: var(--surface2);
    padding: 0.6rem;
    border-radius: 4px;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 300px;
    overflow-y: auto;
    margin-top: 0.3rem;
}}

.tool-call {{
    border-left: 3px solid var(--border);
    padding: 0.3rem 0 0.3rem 0.8rem;
    margin-bottom: 0.4rem;
}}

.tool-call.tc-pass {{ border-left-color: var(--green); }}
.tool-call.tc-fail {{ border-left-color: var(--red); }}

.tc-header {{
    display: flex;
    gap: 1rem;
    align-items: center;
    cursor: pointer;
    font-size: 0.85rem;
    padding: 0.2rem 0;
}}

.tc-header:hover {{ color: var(--blue); }}

.tc-tool {{
    font-weight: 600;
    font-family: 'SF Mono', 'Fira Code', monospace;
}}

.tc-status {{ font-size: 0.75rem; }}
.tc-duration {{ font-size: 0.75rem; color: var(--text-dim); }}

.tc-body {{ padding: 0.5rem 0; }}
.tc-section {{ margin-bottom: 0.5rem; }}
.tc-section strong {{ font-size: 0.8rem; color: var(--text-dim); }}

.footer {{
    text-align: center;
    padding: 2rem 0;
    color: var(--text-dim);
    font-size: 0.8rem;
    border-top: 1px solid var(--border);
    margin-top: 2rem;
}}
</style>
</head>
<body>

<div class="header">
    <h1>SynapBus E2E Test Report</h1>
    <p class="subtitle">Generated {timestamp} | Model: {model} | Server: {base_url}</p>
</div>

<div class="dashboard">
    <div class="stat-card">
        <div class="stat-value">{total}</div>
        <div class="stat-label">Scenarios</div>
    </div>
    <div class="stat-card">
        <div class="stat-value green">{passed}</div>
        <div class="stat-label">Passed</div>
    </div>
    <div class="stat-card">
        <div class="stat-value red">{failed}</div>
        <div class="stat-label">Failed</div>
    </div>
    <div class="stat-card">
        <div class="stat-value yellow">{errored}</div>
        <div class="stat-label">Errors</div>
    </div>
    <div class="stat-card">
        <div class="stat-value blue">{total_duration}</div>
        <div class="stat-label">Duration</div>
    </div>
    <div class="stat-card">
        <div class="stat-value purple">{total_input} / {total_output}</div>
        <div class="stat-label">Tokens In/Out</div>
    </div>
    <div class="stat-card">
        <div class="stat-value">${total_cost}</div>
        <div class="stat-label">Est. Cost</div>
    </div>
    <div class="stat-card">
        <div class="stat-value">{total_tool_calls}</div>
        <div class="stat-label">Tool Calls</div>
    </div>
</div>

<div class="filter-bar">
    <button class="filter-btn active" onclick="filterScenarios('all')">All ({total})</button>
    <button class="filter-btn" onclick="filterScenarios('pass')">Passed ({passed})</button>
    <button class="filter-btn" onclick="filterScenarios('fail')">Failed ({failed})</button>
    <button class="filter-btn" onclick="filterScenarios('error')">Errors ({errored})</button>
</div>

<div id="scenarios">
{scenarios}
</div>

<div class="footer">
    SynapBus E2E Test Suite | Pass Rate: {pass_rate}%
</div>

<script>
function toggleSection(id) {{
    var el = document.getElementById(id);
    if (!el) return;
    if (el.style.display === 'none' || !el.style.display) {{
        el.style.display = 'block';
        el.classList.add('open');
    }} else {{
        el.style.display = 'none';
        el.classList.remove('open');
    }}
}}

function filterScenarios(status) {{
    var cards = document.querySelectorAll('.scenario-card');
    var btns = document.querySelectorAll('.filter-btn');
    btns.forEach(function(b) {{ b.classList.remove('active'); }});
    event.target.classList.add('active');
    cards.forEach(function(card) {{
        if (status === 'all') {{
            card.style.display = 'block';
        }} else {{
            var badge = card.querySelector('.badge');
            if (badge && badge.textContent.toLowerCase() === status) {{
                card.style.display = 'block';
            }} else {{
                card.style.display = 'none';
            }}
        }}
    }});
}}

// Auto-expand failed scenarios
document.querySelectorAll('.scenario-card').forEach(function(card) {{
    var badge = card.querySelector('.badge');
    if (badge && (badge.textContent === 'FAIL' || badge.textContent === 'ERROR')) {{
        var body = card.querySelector('.scenario-body');
        if (body) {{
            body.style.display = 'block';
            body.classList.add('open');
        }}
    }}
}});
</script>

</body>
</html>"""
