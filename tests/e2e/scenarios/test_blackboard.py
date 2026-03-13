"""Scenario: Blackboard channel stigmergy pattern.

3 agents (bb_scout, bb_analyst, bb_coordinator):
- Coordinator creates blackboard channel
- All three join
- Scout writes initial findings
- Analyst reads findings, writes analysis
- Coordinator reads all, writes summary
"""
from __future__ import annotations

import time
import traceback
from typing import Any, Dict, List

import anthropic

from ..lib.agent_runner import AgentRun, TestResult, ToolCall, make_verification, run_agent
from ..lib.mcp_client import SynapBusMCP
from ..lib.setup import register_agents
from ..lib.tools import get_tools_for_scenario


SCENARIO_NAME = "blackboard"
DESCRIPTION = "Blackboard channel stigmergy: scout writes findings, analyst writes analysis, coordinator summarizes"


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["bb_scout", "bb_analyst", "bb_coordinator"]
    runs: List[AgentRun] = []
    verifications: List[Dict[str, Any]] = []

    try:
        creds = register_agents(base_url, [
            {"name": "bb_scout", "display_name": "Scout Agent",
             "capabilities": {"reconnaissance": True, "observation": True}},
            {"name": "bb_analyst", "display_name": "Analyst Agent",
             "capabilities": {"analysis": True, "pattern_recognition": True}},
            {"name": "bb_coordinator", "display_name": "Coordinator Agent",
             "capabilities": {"coordination": True, "summarization": True}},
        ])
        scout_cred, analyst_cred, coord_cred = creds

        scout_mcp = SynapBusMCP(base_url, scout_cred.api_key)
        analyst_mcp = SynapBusMCP(base_url, analyst_cred.api_key)
        coord_mcp = SynapBusMCP(base_url, coord_cred.api_key)
        scout_mcp.initialize()
        analyst_mcp.initialize()
        coord_mcp.initialize()

        tools = get_tools_for_scenario(SCENARIO_NAME)

        # Step 1: Coordinator creates blackboard channel
        print("\n  [bb] Coordinator creates blackboard channel...")
        ch_result = coord_mcp.call_tool("create_channel", {
            "name": "shared-findings",
            "description": "Shared blackboard for collaborative findings",
            "topic": "Current investigation",
            "type": "blackboard",
        })
        ch_created = "channel_id" in ch_result or "name" in ch_result
        verifications.append(make_verification(
            "Blackboard channel created", ch_created,
            str(ch_result)[:200]))

        # Step 2: Scout and Analyst join
        print("  [bb] Scout and Analyst joining...")
        scout_join = scout_mcp.call_tool("join_channel", {"channel_name": "shared-findings"})
        analyst_join = analyst_mcp.call_tool("join_channel", {"channel_name": "shared-findings"})
        verifications.append(make_verification("Scout joined", scout_join.get("status") == "joined"))
        verifications.append(make_verification("Analyst joined", analyst_join.get("status") == "joined"))

        # Step 3: Scout writes initial findings via Claude
        print("\n  [bb] Scout writes initial findings...")
        scout_run = run_agent(
            claude, scout_mcp, "bb_scout",
            system_prompt=(
                "You are a scout agent on SynapBus. Write your field observations "
                "to the shared blackboard channel. Be concise and factual."
            ),
            user_prompt=(
                "Post your findings to the 'shared-findings' channel using "
                "send_channel_message. Report: 'Field observation: Detected 3 anomalous "
                "patterns in network traffic at nodes 7, 12, and 15. Node 7 shows highest "
                "deviation (4.2 sigma). Timestamps correlate with batch processing windows.'"
            ),
            tools=tools, model=model, max_tool_rounds=2,
        )
        runs.append(scout_run)
        scout_posted = any(tc.tool == "send_channel_message" and tc.success
                           for tc in scout_run.tool_calls)
        verifications.append(make_verification("Scout posted findings", scout_posted))

        # Step 4: Analyst reads and writes analysis via Claude
        print("\n  [bb] Analyst reads and writes analysis...")
        analyst_run = run_agent(
            claude, analyst_mcp, "bb_analyst",
            system_prompt=(
                "You are an analyst agent on SynapBus. Read the blackboard, "
                "analyze the findings, and write your analysis back. Be concise."
            ),
            user_prompt=(
                "1. Read your inbox to see the scout's findings\n"
                "2. Post your analysis to 'shared-findings' channel using "
                "send_channel_message. Analyze the patterns mentioned and "
                "suggest root causes."
            ),
            tools=tools, model=model, max_tool_rounds=3,
        )
        runs.append(analyst_run)
        analyst_posted = any(tc.tool == "send_channel_message" and tc.success
                             for tc in analyst_run.tool_calls)
        verifications.append(make_verification("Analyst posted analysis", analyst_posted))

        # Step 5: Coordinator reads all and writes summary via Claude
        print("\n  [bb] Coordinator reads and writes summary...")
        coord_run = run_agent(
            claude, coord_mcp, "bb_coordinator",
            system_prompt=(
                "You are the coordinator agent on SynapBus. Read all blackboard "
                "entries, synthesize a summary, and post it. Be concise."
            ),
            user_prompt=(
                "1. Read your inbox to see all channel messages\n"
                "2. Post a summary to 'shared-findings' using send_channel_message. "
                "Synthesize the scout's observations and analyst's findings into "
                "an action plan."
            ),
            tools=tools, model=model, max_tool_rounds=3,
        )
        runs.append(coord_run)
        coord_posted = any(tc.tool == "send_channel_message" and tc.success
                           for tc in coord_run.tool_calls)
        verifications.append(make_verification("Coordinator posted summary", coord_posted))

        # Step 6: Verify all messages are visible
        print("  [bb] Verifying message visibility...")
        scout_inbox = scout_mcp.call_tool("read_inbox", {"limit": 20, "include_read": True})
        scout_msgs = scout_inbox.get("messages", [])
        # Scout should see messages from analyst and coordinator
        other_msgs = [m for m in scout_msgs
                      if m.get("from_agent") in ("bb_analyst", "bb_coordinator")]
        verifications.append(make_verification(
            "Scout sees other agents' messages",
            len(other_msgs) >= 1,
            "{} messages from other agents".format(len(other_msgs)),
        ))

        scout_mcp.close()
        analyst_mcp.close()
        coord_mcp.close()

        all_passed = all(v["passed"] for v in verifications)
        return TestResult(
            name=SCENARIO_NAME,
            description=DESCRIPTION,
            status="pass" if all_passed else "fail",
            agents=agents_list,
            runs=runs,
            verifications=verifications,
            error=None,
            duration=time.time() - start,
            total_input_tokens=sum(r.input_tokens for r in runs),
            total_output_tokens=sum(r.output_tokens for r in runs),
        )

    except Exception as e:
        return TestResult(
            name=SCENARIO_NAME,
            description=DESCRIPTION,
            status="error",
            agents=agents_list,
            runs=runs,
            verifications=verifications,
            error=traceback.format_exc(),
            duration=time.time() - start,
            total_input_tokens=sum(r.input_tokens for r in runs),
            total_output_tokens=sum(r.output_tokens for r in runs),
        )
