"""Scenario: Direct messaging between two agents.

2 agents (dm_alice, dm_bob):
- Alice discovers Bob
- Alice sends research question to Bob
- Bob reads inbox, claims message
- Bob sends reply
- Bob marks original done
- Alice reads reply
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


SCENARIO_NAME = "direct_messaging"
DESCRIPTION = "Two agents exchange direct messages: discover, send, read, claim, reply, mark done"


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["dm_alice", "dm_bob"]
    runs: List[AgentRun] = []
    verifications: List[Dict[str, Any]] = []

    try:
        # Register agents
        creds = register_agents(base_url, [
            {"name": "dm_alice", "display_name": "Alice the Researcher",
             "capabilities": {"research": True, "summarization": True}},
            {"name": "dm_bob", "display_name": "Bob the Analyst",
             "capabilities": {"data_analysis": True, "coding": True}},
        ])
        alice_cred, bob_cred = creds[0], creds[1]

        # Initialize MCP sessions
        alice_mcp = SynapBusMCP(base_url, alice_cred.api_key)
        bob_mcp = SynapBusMCP(base_url, bob_cred.api_key)
        alice_mcp.initialize()
        bob_mcp.initialize()

        tools = get_tools_for_scenario(SCENARIO_NAME)

        # Step 1: Alice discovers and sends message
        print("\n  [dm] Alice discovers agents and sends message...")
        alice_run = run_agent(
            claude, alice_mcp, "dm_alice",
            system_prompt=(
                "You are Alice, a research agent on SynapBus. "
                "You communicate with other agents using the provided tools. "
                "Be concise. Complete your task in as few tool calls as possible."
            ),
            user_prompt=(
                "First, discover what other agents are available using discover_agents. "
                "Then send a message to 'dm_bob' asking: "
                "'What are the top 3 trade-offs of using MCP vs REST APIs "
                "for agent-to-agent communication?' "
                "Use subject 'MCP vs REST Analysis'."
            ),
            tools=tools, model=model, max_tool_rounds=3,
        )
        runs.append(alice_run)

        # Verify Alice sent a message
        alice_sent = any(tc.tool == "send_message" and tc.success for tc in alice_run.tool_calls)
        verifications.append(make_verification(
            "Alice sent message to Bob", alice_sent,
            "Found send_message tool call" if alice_sent else "No successful send_message call"))

        # Step 2: Bob reads, claims, replies, marks done
        print("\n  [dm] Bob reads inbox and replies...")
        bob_run = run_agent(
            claude, bob_mcp, "dm_bob",
            system_prompt=(
                "You are Bob, a data analyst agent on SynapBus. "
                "When you receive messages, process them and reply. "
                "Be concise and direct. Complete your task efficiently."
            ),
            user_prompt=(
                "1. Check your inbox using read_inbox\n"
                "2. Claim the message using claim_messages\n"
                "3. Send a reply to 'dm_alice' answering her question about MCP vs REST\n"
                "4. Mark the original message as done using mark_done with the message_id\n"
                "Do all steps."
            ),
            tools=tools, model=model, max_tool_rounds=5,
        )
        runs.append(bob_run)

        # Verify Bob's actions
        bob_read = any(tc.tool == "read_inbox" and tc.success for tc in bob_run.tool_calls)
        bob_claimed = any(tc.tool == "claim_messages" and tc.success for tc in bob_run.tool_calls)
        bob_replied = any(tc.tool == "send_message" and tc.success for tc in bob_run.tool_calls)
        bob_marked = any(tc.tool == "mark_done" and tc.success for tc in bob_run.tool_calls)

        verifications.append(make_verification("Bob read inbox", bob_read))
        verifications.append(make_verification("Bob claimed message", bob_claimed))
        verifications.append(make_verification("Bob sent reply", bob_replied))
        verifications.append(make_verification("Bob marked done", bob_marked))

        # Step 3: Verify Alice received the reply (direct MCP call, no Claude)
        print("\n  [dm] Verifying Alice received reply...")
        alice_inbox = alice_mcp.call_tool("read_inbox", {"limit": 10})
        msgs = alice_inbox.get("messages", [])
        bob_replies = [m for m in msgs if m.get("from_agent") == "dm_bob"]

        verifications.append(make_verification(
            "Alice received reply from Bob",
            len(bob_replies) > 0,
            "{} reply(ies) found".format(len(bob_replies)),
        ))

        alice_mcp.close()
        bob_mcp.close()

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
