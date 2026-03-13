"""Scenario: Audit trail verification.

2 agents (audit_alice, audit_bob):
- Agents perform various actions (send messages, join channel, etc.)
- Query traces via REST API
- Verify all actions appear in traces with correct attribution

Uses httpx to query REST API directly after agents perform actions.
"""
from __future__ import annotations

import time
import traceback
from typing import Any, Dict, List

import anthropic
import httpx

from ..lib.agent_runner import AgentRun, TestResult, make_verification
from ..lib.mcp_client import SynapBusMCP
from ..lib.setup import register_agents, register_user


SCENARIO_NAME = "audit_trail"
DESCRIPTION = "Agents perform actions, then verify traces via REST API"


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["audit_alice", "audit_bob"]
    runs: List[AgentRun] = []
    verifications: List[Dict[str, Any]] = []

    try:
        creds = register_agents(base_url, [
            {"name": "audit_alice", "display_name": "Alice (Audit)",
             "capabilities": {"research": True}},
            {"name": "audit_bob", "display_name": "Bob (Audit)",
             "capabilities": {"analysis": True}},
        ])
        alice_cred, bob_cred = creds

        alice_mcp = SynapBusMCP(base_url, alice_cred.api_key)
        bob_mcp = SynapBusMCP(base_url, bob_cred.api_key)
        alice_mcp.initialize()
        bob_mcp.initialize()

        # Step 1: Perform some actions to generate traces
        print("\n  [audit] Generating actions...")

        # Alice creates a channel
        alice_mcp.call_tool("create_channel", {
            "name": "audit-channel",
            "description": "Channel for audit testing",
            "type": "standard",
        })

        # Bob joins
        bob_mcp.call_tool("join_channel", {"channel_name": "audit-channel"})

        # Alice sends a direct message to Bob
        alice_mcp.call_tool("send_message", {
            "to": "audit_bob",
            "body": "Hello Bob, this is an audit test message.",
            "subject": "Audit Test",
        })

        # Alice sends a channel message
        alice_mcp.call_tool("send_channel_message", {
            "channel_name": "audit-channel",
            "body": "Channel broadcast for audit test.",
        })

        # Bob reads inbox
        bob_mcp.call_tool("read_inbox", {"limit": 10})

        # Give traces a moment to be recorded
        time.sleep(0.5)

        # Step 2: Query traces via REST API
        print("  [audit] Querying traces via REST API...")

        # Get session cookies for REST API access
        cookies = register_user(base_url)
        http_client = httpx.Client(timeout=10, cookies=cookies)

        try:
            # Query all traces
            resp = http_client.get("{}/api/traces".format(base_url))
            verifications.append(make_verification(
                "GET /api/traces returns 200",
                resp.status_code == 200,
                "status={}".format(resp.status_code),
            ))

            if resp.status_code == 200:
                traces_data = resp.json()
                traces = traces_data.get("traces", [])
                total = traces_data.get("total", 0)
                verifications.append(make_verification(
                    "Traces returned",
                    total > 0,
                    "{} total traces".format(total),
                ))

                # Check for Alice's actions
                alice_traces = [t for t in traces
                                if t.get("agent_name") == "audit_alice"]
                verifications.append(make_verification(
                    "Alice's actions in traces",
                    len(alice_traces) > 0,
                    "{} traces for audit_alice".format(len(alice_traces)),
                ))

                # Check for specific action types
                actions = [t.get("action") for t in traces]
                verifications.append(make_verification(
                    "send_message action traced",
                    "send_message" in actions,
                    "actions found: {}".format(list(set(actions))[:10]),
                ))

            # Query traces filtered by agent
            resp_filtered = http_client.get(
                "{}/api/traces".format(base_url),
                params={"agent_name": "audit_alice"},
            )
            verifications.append(make_verification(
                "Filtered traces by agent_name",
                resp_filtered.status_code == 200,
                "status={}".format(resp_filtered.status_code),
            ))

            if resp_filtered.status_code == 200:
                filtered_data = resp_filtered.json()
                filtered_traces = filtered_data.get("traces", [])
                all_alice = all(t.get("agent_name") == "audit_alice"
                                for t in filtered_traces)
                verifications.append(make_verification(
                    "Filtered results only contain Alice",
                    all_alice and len(filtered_traces) > 0,
                    "{} traces, all Alice: {}".format(len(filtered_traces), all_alice),
                ))

            # Query trace stats
            print("  [audit] Querying trace stats...")
            resp_stats = http_client.get("{}/api/traces/stats".format(base_url))
            verifications.append(make_verification(
                "GET /api/traces/stats returns 200",
                resp_stats.status_code == 200,
                "status={}".format(resp_stats.status_code),
            ))

            if resp_stats.status_code == 200:
                stats_data = resp_stats.json()
                stats = stats_data.get("stats", {})
                verifications.append(make_verification(
                    "Stats contain action counts",
                    len(stats) > 0,
                    "stats: {}".format(stats),
                ))

            # Export traces as JSON
            print("  [audit] Exporting traces...")
            resp_export = http_client.get(
                "{}/api/traces/export".format(base_url),
                params={"format": "json"},
            )
            verifications.append(make_verification(
                "GET /api/traces/export returns 200",
                resp_export.status_code == 200,
                "status={}, content-type={}".format(
                    resp_export.status_code,
                    resp_export.headers.get("content-type", "unknown")),
            ))

            if resp_export.status_code == 200:
                export_data = resp_export.json()
                verifications.append(make_verification(
                    "Export contains traces",
                    isinstance(export_data, list) and len(export_data) > 0,
                    "{} exported traces".format(
                        len(export_data) if isinstance(export_data, list) else 0),
                ))

                # Verify exported traces have expected fields
                if isinstance(export_data, list) and len(export_data) > 0:
                    first_trace = export_data[0]
                    has_fields = all(
                        k in first_trace
                        for k in ["agent_name", "action", "timestamp"]
                    )
                    verifications.append(make_verification(
                        "Exported traces have expected fields",
                        has_fields,
                        "fields: {}".format(list(first_trace.keys())[:10]),
                    ))

        finally:
            http_client.close()

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
            total_input_tokens=0,
            total_output_tokens=0,
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
            total_input_tokens=0,
            total_output_tokens=0,
        )
