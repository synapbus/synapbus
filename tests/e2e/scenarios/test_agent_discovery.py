"""Scenario: Agent discovery by capabilities.

4 agents with different capabilities:
- disc_researcher (research, writing)
- disc_coder (python, golang, devops)
- disc_analyst (data_analysis, statistics)
- disc_translator (translation, languages)

Researcher discovers agents with "analysis" capability,
then sends a targeted message to the discovered analyst.
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


SCENARIO_NAME = "agent_discovery"
DESCRIPTION = "Four agents with different capabilities: discover by keyword, send targeted message"


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["disc_researcher", "disc_coder", "disc_analyst", "disc_translator"]
    runs: List[AgentRun] = []
    verifications: List[Dict[str, Any]] = []

    try:
        creds = register_agents(base_url, [
            {"name": "disc_researcher", "display_name": "Researcher Agent",
             "capabilities": {"research": True, "writing": True}},
            {"name": "disc_coder", "display_name": "Coder Agent",
             "capabilities": {"python": True, "golang": True, "devops": True}},
            {"name": "disc_analyst", "display_name": "Analyst Agent",
             "capabilities": {"data_analysis": True, "statistics": True}},
            {"name": "disc_translator", "display_name": "Translator Agent",
             "capabilities": {"translation": True, "languages": True}},
        ])
        researcher_cred = creds[0]
        analyst_cred = creds[2]

        researcher_mcp = SynapBusMCP(base_url, researcher_cred.api_key)
        analyst_mcp = SynapBusMCP(base_url, analyst_cred.api_key)
        researcher_mcp.initialize()
        analyst_mcp.initialize()

        tools = get_tools_for_scenario(SCENARIO_NAME)

        # Step 1: Researcher discovers agents with "analysis" capability
        print("\n  [disc] Researcher discovers agents...")
        researcher_run = run_agent(
            claude, researcher_mcp, "disc_researcher",
            system_prompt=(
                "You are a researcher agent on SynapBus. "
                "You need to find agents with data analysis capabilities and "
                "send them a message. Be concise and efficient."
            ),
            user_prompt=(
                "1. Use discover_agents with query 'analysis' to find agents with "
                "data analysis capabilities.\n"
                "2. Send a message to the agent named 'disc_analyst' asking them to "
                "'Analyze the correlation between agent communication frequency and "
                "task completion rates.' Use subject 'Analysis Request'."
            ),
            tools=tools, model=model, max_tool_rounds=3,
        )
        runs.append(researcher_run)

        # Verify discovery
        discovered = any(tc.tool == "discover_agents" and tc.success
                         for tc in researcher_run.tool_calls)
        verifications.append(make_verification("Researcher discovered agents", discovered))

        # Check discovery returned the analyst
        for tc in researcher_run.tool_calls:
            if tc.tool == "discover_agents" and tc.success:
                agents_found = tc.output.get("agents", [])
                analyst_found = any(a.get("name") == "disc_analyst" for a in agents_found)
                verifications.append(make_verification(
                    "Discovery found disc_analyst", analyst_found,
                    "{} agents returned".format(len(agents_found))))
                break
        else:
            verifications.append(make_verification(
                "Discovery found disc_analyst", False, "No discover_agents call"))

        # Verify message sent
        msg_sent = any(tc.tool == "send_message" and tc.success
                       for tc in researcher_run.tool_calls)
        verifications.append(make_verification("Researcher sent message", msg_sent))

        # Step 2: Verify analyst received the message
        print("  [disc] Verifying analyst received message...")
        analyst_inbox = analyst_mcp.call_tool("read_inbox", {"limit": 10})
        msgs = analyst_inbox.get("messages", [])
        researcher_msgs = [m for m in msgs if m.get("from_agent") == "disc_researcher"]
        verifications.append(make_verification(
            "Analyst received message from researcher",
            len(researcher_msgs) > 0,
            "{} messages from researcher".format(len(researcher_msgs)),
        ))

        researcher_mcp.close()
        analyst_mcp.close()

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
