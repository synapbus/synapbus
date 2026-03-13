"""Scenario: Channel-based group communication.

3 agents (ch_alice, ch_bob, ch_carol):
- Alice creates "research-hub" standard channel
- Bob and Carol join
- Alice broadcasts a question
- Bob and Carol each read and reply on channel
- Alice reads channel messages
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


SCENARIO_NAME = "channels"
DESCRIPTION = "Three agents communicate via a shared channel: create, join, broadcast, reply"


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["ch_alice", "ch_bob", "ch_carol"]
    runs: List[AgentRun] = []
    verifications: List[Dict[str, Any]] = []

    try:
        creds = register_agents(base_url, [
            {"name": "ch_alice", "display_name": "Alice (Channel Lead)",
             "capabilities": {"coordination": True}},
            {"name": "ch_bob", "display_name": "Bob (Channel Member)",
             "capabilities": {"analysis": True}},
            {"name": "ch_carol", "display_name": "Carol (Channel Member)",
             "capabilities": {"writing": True}},
        ])
        alice_cred, bob_cred, carol_cred = creds

        alice_mcp = SynapBusMCP(base_url, alice_cred.api_key)
        bob_mcp = SynapBusMCP(base_url, bob_cred.api_key)
        carol_mcp = SynapBusMCP(base_url, carol_cred.api_key)
        alice_mcp.initialize()
        bob_mcp.initialize()
        carol_mcp.initialize()

        tools = get_tools_for_scenario(SCENARIO_NAME)

        # Step 1: Alice creates channel (direct MCP call for reliability)
        print("\n  [ch] Alice creates channel...")
        create_result = alice_mcp.call_tool("create_channel", {
            "name": "research-hub",
            "description": "Research collaboration channel",
            "topic": "Current research topics",
            "type": "standard",
        })
        channel_created = "channel_id" in create_result or "name" in create_result
        verifications.append(make_verification(
            "Channel 'research-hub' created", channel_created,
            str(create_result)[:200]))

        # Step 2: Bob joins
        print("  [ch] Bob joins channel...")
        bob_join = bob_mcp.call_tool("join_channel", {"channel_name": "research-hub"})
        bob_joined = bob_join.get("status") == "joined"
        verifications.append(make_verification("Bob joined channel", bob_joined))

        # Step 3: Carol joins
        print("  [ch] Carol joins channel...")
        carol_join = carol_mcp.call_tool("join_channel", {"channel_name": "research-hub"})
        carol_joined = carol_join.get("status") == "joined"
        verifications.append(make_verification("Carol joined channel", carol_joined))

        # Step 4: Alice broadcasts a question via Claude
        print("\n  [ch] Alice broadcasts question...")
        alice_run = run_agent(
            claude, alice_mcp, "ch_alice",
            system_prompt=(
                "You are Alice, coordinator of the research-hub channel on SynapBus. "
                "Use the send_channel_message tool to broadcast to the channel. "
                "Be concise."
            ),
            user_prompt=(
                "Send a message to channel 'research-hub' asking: "
                "'Team, what are the most promising approaches to agent coordination? "
                "Please share your perspective.' "
                "Use the send_channel_message tool with channel_name='research-hub'."
            ),
            tools=tools, model=model, max_tool_rounds=2,
        )
        runs.append(alice_run)

        alice_broadcast = any(tc.tool == "send_channel_message" and tc.success
                             for tc in alice_run.tool_calls)
        verifications.append(make_verification("Alice broadcast to channel", alice_broadcast))

        # Step 5: Bob reads inbox and replies on channel
        print("\n  [ch] Bob reads and replies on channel...")
        bob_run = run_agent(
            claude, bob_mcp, "ch_bob",
            system_prompt=(
                "You are Bob, a member of the research-hub channel on SynapBus. "
                "Read your inbox for channel messages and reply on the channel. "
                "Be concise."
            ),
            user_prompt=(
                "1. Read your inbox to see channel messages\n"
                "2. Reply on the channel 'research-hub' with your analysis perspective "
                "using send_channel_message with channel_name='research-hub'"
            ),
            tools=tools, model=model, max_tool_rounds=3,
        )
        runs.append(bob_run)

        bob_replied = any(tc.tool == "send_channel_message" and tc.success
                          for tc in bob_run.tool_calls)
        verifications.append(make_verification("Bob replied on channel", bob_replied))

        # Step 6: Carol reads inbox and replies on channel
        print("\n  [ch] Carol reads and replies on channel...")
        carol_run = run_agent(
            claude, carol_mcp, "ch_carol",
            system_prompt=(
                "You are Carol, a member of the research-hub channel on SynapBus. "
                "Read your inbox for channel messages and reply on the channel. "
                "Be concise."
            ),
            user_prompt=(
                "1. Read your inbox to see channel messages\n"
                "2. Reply on the channel 'research-hub' with your writing perspective "
                "using send_channel_message with channel_name='research-hub'"
            ),
            tools=tools, model=model, max_tool_rounds=3,
        )
        runs.append(carol_run)

        carol_replied = any(tc.tool == "send_channel_message" and tc.success
                            for tc in carol_run.tool_calls)
        verifications.append(make_verification("Carol replied on channel", carol_replied))

        # Step 7: Verify Alice received channel messages
        print("  [ch] Verifying Alice received messages...")
        alice_inbox = alice_mcp.call_tool("read_inbox", {"limit": 20, "include_read": True})
        msgs = alice_inbox.get("messages", [])
        channel_msgs = [m for m in msgs
                        if m.get("from_agent") in ("ch_bob", "ch_carol")]
        verifications.append(make_verification(
            "Alice received channel replies",
            len(channel_msgs) >= 1,
            "{} channel messages received".format(len(channel_msgs)),
        ))

        alice_mcp.close()
        bob_mcp.close()
        carol_mcp.close()

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
