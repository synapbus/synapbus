"""Scenario: Access control for private channels.

3 agents (acl_owner, acl_member, acl_outsider):
- Owner creates private channel
- Owner invites Member
- Member joins
- Outsider tries to join (should fail)
- Outsider tries to send channel message (should fail)
- Member tries to kick Owner (should fail)
- Member tries to invite Outsider (should fail)
- Owner kicks Member

This test does NOT use Claude agents -- it directly calls MCP tools
to test error responses (cheaper and deterministic).
"""
from __future__ import annotations

import time
import traceback
from typing import Any, Dict, List

import anthropic

from ..lib.agent_runner import AgentRun, TestResult, make_verification
from ..lib.mcp_client import SynapBusMCP
from ..lib.setup import register_agents


SCENARIO_NAME = "access_control"
DESCRIPTION = "Private channel access control: unauthorized join, send, kick, invite all return errors"


def _is_error(result: Dict[str, Any]) -> bool:
    """Check if an MCP tool result indicates an error."""
    if result.get("_mcp_error"):
        return True
    if "error" in result:
        return True
    # Check for error in raw response
    if result.get("_raw", ""):
        return "error" in result["_raw"].lower() or "failed" in result["_raw"].lower()
    return False


def _has_error_keyword(result: Dict[str, Any]) -> bool:
    """Check if result text contains error-related keywords."""
    text = str(result).lower()
    return any(kw in text for kw in ["error", "failed", "denied", "unauthorized",
                                      "not a member", "not the owner", "not invited",
                                      "permission", "forbidden", "private"])


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["acl_owner", "acl_member", "acl_outsider"]
    runs: List[AgentRun] = []  # No Claude runs in this scenario
    verifications: List[Dict[str, Any]] = []

    try:
        creds = register_agents(base_url, [
            {"name": "acl_owner", "display_name": "Channel Owner",
             "capabilities": {"admin": True}},
            {"name": "acl_member", "display_name": "Channel Member",
             "capabilities": {"research": True}},
            {"name": "acl_outsider", "display_name": "Outsider",
             "capabilities": {"hacking": True}},
        ])
        owner_cred, member_cred, outsider_cred = creds

        owner_mcp = SynapBusMCP(base_url, owner_cred.api_key)
        member_mcp = SynapBusMCP(base_url, member_cred.api_key)
        outsider_mcp = SynapBusMCP(base_url, outsider_cred.api_key)
        owner_mcp.initialize()
        member_mcp.initialize()
        outsider_mcp.initialize()

        # 1. Owner creates private channel
        print("\n  [acl] Owner creates private channel...")
        create_result = owner_mcp.call_tool("create_channel", {
            "name": "secret-ops",
            "description": "Top secret operations",
            "type": "standard",
            "is_private": True,
        })
        ch_created = create_result.get("is_private", False) is True
        verifications.append(make_verification(
            "Private channel created", ch_created,
            str(create_result)[:200]))

        # 2. Owner invites Member
        print("  [acl] Owner invites member...")
        invite_result = owner_mcp.call_tool("invite_to_channel", {
            "channel_name": "secret-ops",
            "agent_name": "acl_member",
        })
        invited = invite_result.get("status") == "invited"
        verifications.append(make_verification("Owner invited member", invited,
                                               str(invite_result)[:200]))

        # 3. Member joins
        print("  [acl] Member joins channel...")
        member_join = member_mcp.call_tool("join_channel", {"channel_name": "secret-ops"})
        member_joined = member_join.get("status") == "joined"
        verifications.append(make_verification("Member joined channel", member_joined,
                                               str(member_join)[:200]))

        # 4. Outsider tries to join (should fail)
        print("  [acl] Outsider tries to join (should fail)...")
        outsider_join = outsider_mcp.call_tool("join_channel", {"channel_name": "secret-ops"})
        outsider_blocked = _has_error_keyword(outsider_join)
        verifications.append(make_verification(
            "Outsider join rejected", outsider_blocked,
            str(outsider_join)[:200]))

        # 5. Outsider tries to send channel message (should fail)
        print("  [acl] Outsider tries to send message (should fail)...")
        outsider_msg = outsider_mcp.call_tool("send_channel_message", {
            "channel_name": "secret-ops",
            "body": "I should not be able to send this",
        })
        outsider_msg_blocked = _has_error_keyword(outsider_msg)
        verifications.append(make_verification(
            "Outsider message rejected", outsider_msg_blocked,
            str(outsider_msg)[:200]))

        # 6. Member tries to kick Owner (should fail)
        print("  [acl] Member tries to kick owner (should fail)...")
        member_kick = member_mcp.call_tool("kick_from_channel", {
            "channel_name": "secret-ops",
            "agent_name": "acl_owner",
        })
        member_kick_blocked = _has_error_keyword(member_kick)
        verifications.append(make_verification(
            "Member kick-owner rejected", member_kick_blocked,
            str(member_kick)[:200]))

        # 7. Member tries to invite Outsider (should fail for private channel)
        print("  [acl] Member tries to invite outsider (should fail)...")
        member_invite = member_mcp.call_tool("invite_to_channel", {
            "channel_name": "secret-ops",
            "agent_name": "acl_outsider",
        })
        member_invite_blocked = _has_error_keyword(member_invite)
        verifications.append(make_verification(
            "Member invite rejected", member_invite_blocked,
            str(member_invite)[:200]))

        # 8. Owner kicks Member (should succeed)
        print("  [acl] Owner kicks member...")
        owner_kick = owner_mcp.call_tool("kick_from_channel", {
            "channel_name": "secret-ops",
            "agent_name": "acl_member",
        })
        owner_kicked = owner_kick.get("status") == "kicked"
        verifications.append(make_verification(
            "Owner kicked member", owner_kicked,
            str(owner_kick)[:200]))

        owner_mcp.close()
        member_mcp.close()
        outsider_mcp.close()

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
