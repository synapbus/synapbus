#!/usr/bin/env python3
"""SynapBus dream-agent runner — memory consolidation worker.

Dispatched by SynapBus's ConsolidatorWorker via the k8sjob harness.
Runs Claude Code (via claude-agent-sdk) against SynapBus's MCP server,
using a one-time dispatch token to authorize the six memory_* tools.

Environment contract (set by ConsolidatorWorker.runJob + k8sjob harness):
    SYNAPBUS_URL                     base URL, e.g. http://synapbus.synapbus.svc.cluster.local:8080
    SYNAPBUS_API_KEY                 dream-claude agent API key (Bearer auth)
    SYNAPBUS_DISPATCH_TOKEN          one-shot token authorizing memory_* tools
    SYNAPBUS_CONSOLIDATION_JOB_ID    parent job id (audit anchor)
    SYNAPBUS_JOB_TYPE                reflection | core_rewrite | dedup_contradiction | link_gen
    SYNAPBUS_OWNER_ID                target owner id
    SYNAPBUS_DREAM_PROMPT            job-type prompt (PromptFor)
    SYNAPBUS_RUN_ID                  harness-injected run id
Optional:
    ANTHROPIC_API_KEY or CLAUDE_CONFIG_DIR  Claude Code credentials
    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT      OTLP/HTTP traces endpoint
    DREAM_MAX_TURNS                          override max_turns (default 20)
    DREAM_MODEL                              override model (default claude-sonnet-4-6)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import shutil
import sys
import tempfile
import time
from typing import Any

# --- Structured JSON logging (one obj per line for Loki) -------------------

class _JsonFormatter(logging.Formatter):
    def __init__(self) -> None:
        super().__init__()
        self.job_id = os.environ.get("SYNAPBUS_CONSOLIDATION_JOB_ID", "")
        self.job_type = os.environ.get("SYNAPBUS_JOB_TYPE", "")
        self.owner_id = os.environ.get("SYNAPBUS_OWNER_ID", "")
        self.run_id = os.environ.get("SYNAPBUS_RUN_ID", "")
        self.trace_id: str = ""

    def format(self, record: logging.LogRecord) -> str:
        entry: dict[str, Any] = {
            "ts": self.formatTime(record, "%Y-%m-%dT%H:%M:%SZ"),
            "level": record.levelname,
            "logger": record.name,
            "job_id": self.job_id,
            "job_type": self.job_type,
            "owner_id": self.owner_id,
            "run_id": self.run_id,
        }
        if self.trace_id:
            entry["traceID"] = self.trace_id
        if isinstance(record.msg, dict):
            entry.update(record.msg)
        else:
            entry["msg"] = record.getMessage()
        return json.dumps(entry, default=str)


def _setup_logging() -> logging.Logger:
    lg = logging.getLogger("dream-agent")
    lg.setLevel(logging.INFO)
    lg.handlers.clear()
    lg.propagate = False
    h = logging.StreamHandler(sys.stdout)
    h.setFormatter(_JsonFormatter())
    lg.addHandler(h)
    root = logging.getLogger()
    root.handlers.clear()
    root.addHandler(h)
    return lg


logger = logging.getLogger("dream-agent")


# --- OTEL tracing (best-effort) --------------------------------------------

_tracer = None


def _init_tracing() -> None:
    global _tracer
    ep = os.environ.get("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
    if not ep:
        return
    try:
        from opentelemetry import trace
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter

        resource = Resource.create({
            "service.name": "synapbus-dream-agent",
            "service.version": "0.1.0",
            "synapbus.job_id": os.environ.get("SYNAPBUS_CONSOLIDATION_JOB_ID", ""),
            "synapbus.job_type": os.environ.get("SYNAPBUS_JOB_TYPE", ""),
            "synapbus.owner_id": os.environ.get("SYNAPBUS_OWNER_ID", ""),
        })
        provider = TracerProvider(resource=resource)
        provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=ep)))
        trace.set_tracer_provider(provider)
        _tracer = trace.get_tracer("synapbus-dream-agent", "0.1.0")
        logger.info({"msg": "OTEL tracing enabled", "endpoint": ep})
    except ImportError:
        logger.info({"msg": "OTEL packages not installed; tracing disabled"})
    except Exception as e:  # noqa: BLE001
        logger.warning({"msg": "OTEL init failed", "error": str(e)})


def _shutdown_tracing() -> None:
    try:
        from opentelemetry import trace
        p = trace.get_tracer_provider()
        if hasattr(p, "shutdown"):
            p.shutdown()
    except Exception:  # noqa: BLE001
        pass


# --- Claude Code creds: ensure config dir is writable ----------------------

def _ensure_writable_config() -> str:
    src = os.environ.get("CLAUDE_CONFIG_DIR", os.path.expanduser("~/.claude"))
    test = os.path.join(src, ".write_test")
    try:
        os.makedirs(src, exist_ok=True)
        with open(test, "w") as f:
            f.write("ok")
        os.remove(test)
        return src
    except OSError:
        pass
    tmp = tempfile.mkdtemp(prefix="claude_config_")
    for fn in (".credentials.json", "credentials.json", "settings.json"):
        s = os.path.join(src, fn)
        if os.path.exists(s):
            shutil.copy2(s, os.path.join(tmp, fn))
    logger.info({"msg": "Created writable Claude config dir", "path": tmp})
    return tmp


# --- Required-env helper ---------------------------------------------------

_REQUIRED = (
    "SYNAPBUS_URL",
    "SYNAPBUS_API_KEY",
    "SYNAPBUS_DISPATCH_TOKEN",
    "SYNAPBUS_CONSOLIDATION_JOB_ID",
    "SYNAPBUS_JOB_TYPE",
    "SYNAPBUS_OWNER_ID",
    "SYNAPBUS_DREAM_PROMPT",
)


def _read_env() -> dict[str, str]:
    out: dict[str, str] = {}
    missing: list[str] = []
    for k in _REQUIRED:
        v = os.environ.get(k, "")
        if not v:
            missing.append(k)
        out[k] = v
    if missing:
        raise RuntimeError(f"missing required env vars: {','.join(missing)}")
    out["SYNAPBUS_RUN_ID"] = os.environ.get("SYNAPBUS_RUN_ID", "")
    return out


# --- Prompt builder --------------------------------------------------------

_ALLOWED_TOOLS = [
    "mcp__synapbus__memory_list_unprocessed",
    "mcp__synapbus__memory_write_reflection",
    "mcp__synapbus__memory_rewrite_core",
    "mcp__synapbus__memory_mark_duplicate",
    "mcp__synapbus__memory_supersede",
    "mcp__synapbus__memory_add_link",
]


def _build_prompt(env: dict[str, str]) -> str:
    return (
        f"{env['SYNAPBUS_DREAM_PROMPT']}\n\n"
        "Context:\n"
        f"- job_id: {env['SYNAPBUS_CONSOLIDATION_JOB_ID']}\n"
        f"- job_type: {env['SYNAPBUS_JOB_TYPE']}\n"
        f"- owner_id: {env['SYNAPBUS_OWNER_ID']}\n"
        f"- run_id: {env['SYNAPBUS_RUN_ID']}\n"
        "- The dispatch token is forwarded automatically on every MCP "
        "request via the `X-Synapbus-Dispatch-Token` header. You do not "
        "need to pass it as a tool argument.\n"
        "- Pass `owner_id` from the context above on every memory_* call.\n"
        "- Use ONLY the memory_* tools listed in `allowed_tools`. Do not "
        "call send_message, execute, search, or any other tool.\n"
        "- When you are done, output a one-line JSON summary and exit.\n"
    )


# --- Session runner --------------------------------------------------------

async def run_session(env: dict[str, str], model: str, max_turns: int, config_dir: str) -> dict[str, Any]:
    from claude_agent_sdk import (
        AssistantMessage,
        ClaudeAgentOptions,
        ResultMessage,
        TextBlock,
        query,
    )
    try:
        from claude_agent_sdk import ToolUseBlock, ToolResultBlock, ThinkingBlock, UserMessage
    except ImportError:
        ToolUseBlock = ToolResultBlock = ThinkingBlock = UserMessage = None

    base = env["SYNAPBUS_URL"].rstrip("/")
    mcp_servers: dict[str, Any] = {
        "synapbus": {
            "type": "http",
            "url": f"{base}/mcp",
            "headers": {
                "Authorization": f"Bearer {env['SYNAPBUS_API_KEY']}",
                "X-Synapbus-Dispatch-Token": env["SYNAPBUS_DISPATCH_TOKEN"],
            },
        },
    }

    prompt = _build_prompt(env)

    def _on_stderr(line: str) -> None:
        logger.warning({"msg": "sdk_stderr", "line": line.rstrip()})

    tokens_in = 0
    tokens_out = 0
    tool_calls = 0
    turn = 0
    started = time.time()
    status = "ok"
    error_msg = ""

    try:
        async for message in query(
            prompt=prompt,
            options=ClaudeAgentOptions(
                model=model,
                max_turns=max_turns,
                mcp_servers=mcp_servers,
                permission_mode="bypassPermissions",
                allowed_tools=_ALLOWED_TOOLS,
                env={"CLAUDE_CONFIG_DIR": config_dir},
                stderr=_on_stderr,
            ),
        ):
            if isinstance(message, ResultMessage):
                usage = getattr(message, "usage", None)
                tokens_in = getattr(usage, "input_tokens", 0) if usage else 0
                tokens_out = getattr(usage, "output_tokens", 0) if usage else 0
                is_error = getattr(message, "is_error", False)
                duration_s = round(time.time() - started, 1)
                logger.info({
                    "type": "result",
                    "turns": getattr(message, "num_turns", 0),
                    "cost_usd": getattr(message, "cost_usd", 0) or 0,
                    "tokens_in": tokens_in,
                    "tokens_out": tokens_out,
                    "tool_calls": tool_calls,
                    "duration_s": duration_s,
                    "is_error": is_error,
                })
                if is_error:
                    status = "error"
                    error_msg = "result_message.is_error=true"
            elif isinstance(message, AssistantMessage):
                turn += 1
                for block in message.content:
                    if isinstance(block, TextBlock):
                        logger.info({
                            "type": "text",
                            "turn": turn,
                            "text": block.text[:300].replace("\n", " "),
                        })
                    elif ToolUseBlock and isinstance(block, ToolUseBlock):
                        tool_calls += 1
                        logger.info({
                            "type": "tool_use",
                            "turn": turn,
                            "tool": getattr(block, "name", "unknown"),
                            "input": str(getattr(block, "input", ""))[:200],
                        })
                    elif ThinkingBlock and isinstance(block, ThinkingBlock):
                        logger.info({
                            "type": "thinking",
                            "turn": turn,
                            "text": getattr(block, "text", "")[:200].replace("\n", " "),
                        })
            elif UserMessage and isinstance(message, UserMessage):
                for block in message.content:
                    if ToolResultBlock and isinstance(block, ToolResultBlock):
                        is_err = getattr(block, "is_error", False)
                        logger.info({
                            "type": "tool_result",
                            "turn": turn,
                            "is_error": is_err,
                            "content": str(getattr(block, "content", ""))[:200],
                        })
    except Exception as e:  # noqa: BLE001
        status = "error"
        error_msg = f"{type(e).__name__}: {e}"
        logger.error({"msg": "session failed", "error": error_msg})

    return {
        "final": True,
        "tokens_in": tokens_in,
        "tokens_out": tokens_out,
        "tool_calls": tool_calls,
        "turns": turn,
        "status": status,
        "error": error_msg,
    }


# --- main ------------------------------------------------------------------

def main() -> int:
    global logger
    parser = argparse.ArgumentParser(description="SynapBus dream-agent runner")
    parser.add_argument("--mock", action="store_true",
                        help="Log env contract and exit without invoking the SDK")
    parser.add_argument("--max-turns", type=int,
                        default=int(os.environ.get("DREAM_MAX_TURNS", "20")))
    parser.add_argument("--model", default=os.environ.get("DREAM_MODEL", "claude-sonnet-4-6"))
    args = parser.parse_args()

    logger = _setup_logging()
    _init_tracing()

    try:
        env = _read_env()
    except RuntimeError as e:
        logger.error({"msg": "env validation failed", "error": str(e)})
        print(json.dumps({
            "final": True, "tokens_in": 0, "tokens_out": 0,
            "tool_calls": 0, "status": "error", "error": str(e),
        }))
        return 1

    logger.info({
        "msg": "dream-agent starting",
        "synapbus_url": env["SYNAPBUS_URL"],
        "model": args.model,
        "max_turns": args.max_turns,
    })

    if args.mock:
        logger.info({"msg": "--mock; skipping SDK invocation"})
        print(json.dumps({
            "final": True, "tokens_in": 0, "tokens_out": 0,
            "tool_calls": 0, "status": "ok", "error": "",
        }))
        return 0

    config_dir = _ensure_writable_config()

    try:
        result = asyncio.run(run_session(env, args.model, args.max_turns, config_dir))
    except Exception as e:  # noqa: BLE001
        logger.error({"msg": "fatal", "error": f"{type(e).__name__}: {e}"})
        print(json.dumps({
            "final": True, "tokens_in": 0, "tokens_out": 0,
            "tool_calls": 0, "status": "error", "error": str(e),
        }))
        _shutdown_tracing()
        return 1

    # Final single-line envelope for harness Usage parsing.
    print(json.dumps(result))
    _shutdown_tracing()
    return 0 if result.get("status") == "ok" else 1


if __name__ == "__main__":
    sys.exit(main())
