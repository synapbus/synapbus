"""Claude agent loop (Anthropic SDK tool-use) with logging for reports."""
from __future__ import annotations

import json
import time
import traceback
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

import anthropic

from .mcp_client import SynapBusMCP


@dataclass
class ToolCall:
    """Record of a single tool call."""
    agent: str
    tool: str
    input: Dict[str, Any]
    output: Dict[str, Any]
    success: bool
    timestamp: float
    duration: float = 0.0


@dataclass
class AgentRun:
    """Record of a full agent conversation turn."""
    agent_name: str
    system_prompt: str
    user_prompt: str
    response_text: str
    tool_calls: List[ToolCall]
    input_tokens: int
    output_tokens: int
    duration: float
    error: Optional[str] = None


@dataclass
class TestResult:
    """Result of a single test scenario."""
    name: str
    description: str
    status: str  # "pass", "fail", "error"
    agents: List[str]
    runs: List[AgentRun]
    verifications: List[Dict[str, Any]]
    error: Optional[str]
    duration: float
    total_input_tokens: int
    total_output_tokens: int


def run_agent(
    claude: anthropic.Anthropic,
    mcp: SynapBusMCP,
    agent_name: str,
    system_prompt: str,
    user_prompt: str,
    tools: List[Dict[str, Any]],
    model: str = "claude-sonnet-4-6",
    max_tool_rounds: int = 5,
    timeout: float = 60.0,
) -> AgentRun:
    """Run a Claude agent with SynapBus MCP tools.

    Pattern from dialog-engine: iterate up to max_tool_rounds allowing
    tool use. On the final round, omit tools to force a text response.
    Returns an AgentRun with all details for the report.
    """
    messages: List[Dict[str, Any]] = [{"role": "user", "content": user_prompt}]
    all_tool_calls: List[ToolCall] = []
    total_input_tokens = 0
    total_output_tokens = 0
    start_time = time.time()

    for round_num in range(max_tool_rounds + 1):
        api_kwargs: Dict[str, Any] = dict(
            model=model,
            max_tokens=2048,
            system=system_prompt,
            messages=messages,
        )
        # Allow tool use except on final round
        if round_num < max_tool_rounds:
            api_kwargs["tools"] = tools

        try:
            response = claude.messages.create(**api_kwargs)
        except Exception as e:
            return AgentRun(
                agent_name=agent_name,
                system_prompt=system_prompt,
                user_prompt=user_prompt,
                response_text="",
                tool_calls=all_tool_calls,
                input_tokens=total_input_tokens,
                output_tokens=total_output_tokens,
                duration=time.time() - start_time,
                error="API error: {}".format(str(e)),
            )

        total_input_tokens += response.usage.input_tokens
        total_output_tokens += response.usage.output_tokens

        has_tool_use = any(b.type == "tool_use" for b in response.content)

        if not has_tool_use or round_num == max_tool_rounds:
            # Extract final text
            text_parts = []
            for block in response.content:
                if block.type == "text":
                    text_parts.append(block.text)
            final_text = "\n".join(text_parts) if text_parts else "(no response)"
            print("  [{}] {}".format(agent_name, final_text[:200]))
            return AgentRun(
                agent_name=agent_name,
                system_prompt=system_prompt,
                user_prompt=user_prompt,
                response_text=final_text,
                tool_calls=all_tool_calls,
                input_tokens=total_input_tokens,
                output_tokens=total_output_tokens,
                duration=time.time() - start_time,
            )

        # Process tool calls
        assistant_content: List[Dict[str, Any]] = []
        for block in response.content:
            if block.type == "text":
                assistant_content.append({"type": "text", "text": block.text})
                print("  [{}] {}".format(agent_name, block.text[:120]))
            elif block.type == "tool_use":
                assistant_content.append({
                    "type": "tool_use",
                    "id": block.id,
                    "name": block.name,
                    "input": block.input,
                })

        messages.append({"role": "assistant", "content": assistant_content})

        # Execute tools via MCP
        tool_results: List[Dict[str, Any]] = []
        for block in response.content:
            if block.type == "tool_use":
                tool_input_str = json.dumps(block.input)[:80]
                print("  [{}] -> {}({})".format(agent_name, block.name, tool_input_str))
                tc_start = time.time()
                try:
                    tool_result = mcp.call_tool(block.name, block.input)
                    result_str = json.dumps(tool_result)
                    tc_duration = time.time() - tc_start
                    print("  [{}] <- {}".format(agent_name, result_str[:120]))

                    is_error = isinstance(tool_result, dict) and tool_result.get("_mcp_error", False)
                    all_tool_calls.append(ToolCall(
                        agent=agent_name,
                        tool=block.name,
                        input=block.input,
                        output=tool_result,
                        success=not is_error,
                        timestamp=time.time(),
                        duration=tc_duration,
                    ))
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result_str,
                    })
                except Exception as e:
                    tc_duration = time.time() - tc_start
                    error_msg = str(e)
                    print("  [{}] <- ERROR: {}".format(agent_name, error_msg))
                    all_tool_calls.append(ToolCall(
                        agent=agent_name,
                        tool=block.name,
                        input=block.input,
                        output={"error": error_msg},
                        success=False,
                        timestamp=time.time(),
                        duration=tc_duration,
                    ))
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": "Error: {}".format(error_msg),
                        "is_error": True,
                    })

        messages.append({"role": "user", "content": tool_results})

    # Should not reach here
    return AgentRun(
        agent_name=agent_name,
        system_prompt=system_prompt,
        user_prompt=user_prompt,
        response_text="(max rounds exceeded)",
        tool_calls=all_tool_calls,
        input_tokens=total_input_tokens,
        output_tokens=total_output_tokens,
        duration=time.time() - start_time,
    )


def make_verification(check: str, passed: bool, detail: str = "") -> Dict[str, Any]:
    """Create a verification entry for the report."""
    return {"check": check, "passed": passed, "detail": detail}
