"""Scenario: Task auction lifecycle.

3 agents (auction_poster, auction_bidder1, auction_bidder2):
- Poster creates auction channel
- Bidders join
- Poster posts a task
- Both bidders submit bids
- Poster accepts bidder1's bid
- Bidder1 completes the task
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


SCENARIO_NAME = "task_auction"
DESCRIPTION = "Task auction lifecycle: post task, bid, accept bid, complete task"


def run(claude: anthropic.Anthropic, base_url: str, model: str) -> TestResult:
    start = time.time()
    agents_list: List[str] = ["auction_poster", "auction_bidder1", "auction_bidder2"]
    runs: List[AgentRun] = []
    verifications: List[Dict[str, Any]] = []

    try:
        creds = register_agents(base_url, [
            {"name": "auction_poster", "display_name": "Task Poster",
             "capabilities": {"project_management": True}},
            {"name": "auction_bidder1", "display_name": "Bidder One",
             "capabilities": {"python": True, "ml": True}},
            {"name": "auction_bidder2", "display_name": "Bidder Two",
             "capabilities": {"golang": True, "devops": True}},
        ])
        poster_cred, bidder1_cred, bidder2_cred = creds

        poster_mcp = SynapBusMCP(base_url, poster_cred.api_key)
        bidder1_mcp = SynapBusMCP(base_url, bidder1_cred.api_key)
        bidder2_mcp = SynapBusMCP(base_url, bidder2_cred.api_key)
        poster_mcp.initialize()
        bidder1_mcp.initialize()
        bidder2_mcp.initialize()

        tools = get_tools_for_scenario(SCENARIO_NAME)

        # Step 1: Create auction channel (direct MCP)
        print("\n  [auction] Creating auction channel...")
        ch_result = poster_mcp.call_tool("create_channel", {
            "name": "task-market",
            "description": "Task marketplace",
            "type": "auction",
        })
        ch_created = "channel_id" in ch_result or "name" in ch_result
        verifications.append(make_verification("Auction channel created", ch_created,
                                               str(ch_result)[:200]))

        # Step 2: Bidders join
        print("  [auction] Bidders joining channel...")
        b1_join = bidder1_mcp.call_tool("join_channel", {"channel_name": "task-market"})
        b2_join = bidder2_mcp.call_tool("join_channel", {"channel_name": "task-market"})
        verifications.append(make_verification("Bidder1 joined", b1_join.get("status") == "joined"))
        verifications.append(make_verification("Bidder2 joined", b2_join.get("status") == "joined"))

        # Step 3: Poster posts a task (direct MCP)
        print("  [auction] Posting task...")
        task_result = poster_mcp.call_tool("post_task", {
            "channel_name": "task-market",
            "title": "Build ML Pipeline",
            "description": "Build a data processing pipeline with feature engineering and model training",
            "requirements": '{"skills": ["python", "ml"], "experience": "intermediate"}',
        })
        task_id = task_result.get("task_id")
        verifications.append(make_verification(
            "Task posted", task_id is not None,
            "task_id={}".format(task_id)))

        if task_id is None:
            raise ValueError("Task creation failed: {}".format(task_result))

        # Step 4: Bidder1 bids (via Claude for variety)
        print("\n  [auction] Bidder1 submitting bid...")
        bidder1_run = run_agent(
            claude, bidder1_mcp, "auction_bidder1",
            system_prompt=(
                "You are Bidder One, a Python/ML specialist on SynapBus. "
                "Submit a bid on a task. Be concise."
            ),
            user_prompt=(
                "Submit a bid on task_id {task_id} using the bid_task tool. "
                "Include your capabilities as JSON: '{{\"python\": true, \"ml\": true}}', "
                "time_estimate '3 days', and message explaining why you are a good fit."
            ).format(task_id=task_id),
            tools=tools, model=model, max_tool_rounds=2,
        )
        runs.append(bidder1_run)

        bid1_submitted = any(tc.tool == "bid_task" and tc.success for tc in bidder1_run.tool_calls)
        # Extract bid_id from tool output
        bid1_id = None
        for tc in bidder1_run.tool_calls:
            if tc.tool == "bid_task" and tc.success:
                bid1_id = tc.output.get("bid_id")
        verifications.append(make_verification(
            "Bidder1 submitted bid", bid1_submitted,
            "bid_id={}".format(bid1_id)))

        # Step 5: Bidder2 bids (direct MCP)
        print("  [auction] Bidder2 submitting bid...")
        bid2_result = bidder2_mcp.call_tool("bid_task", {
            "task_id": task_id,
            "capabilities": '{"golang": true, "devops": true}',
            "time_estimate": "5 days",
            "message": "I can handle the infrastructure side with Go",
        })
        bid2_id = bid2_result.get("bid_id")
        verifications.append(make_verification(
            "Bidder2 submitted bid", bid2_id is not None,
            "bid_id={}".format(bid2_id)))

        # Step 6: Poster accepts Bidder1's bid
        if bid1_id is not None:
            print("  [auction] Poster accepting Bidder1's bid...")
            accept_result = poster_mcp.call_tool("accept_bid", {
                "task_id": task_id,
                "bid_id": bid1_id,
            })
            accepted = accept_result.get("status") == "accepted"
            verifications.append(make_verification(
                "Poster accepted Bidder1's bid", accepted,
                str(accept_result)[:200]))
        else:
            verifications.append(make_verification(
                "Poster accepted Bidder1's bid", False, "No bid_id available"))

        # Step 7: Bidder1 completes task
        print("  [auction] Bidder1 completing task...")
        complete_result = bidder1_mcp.call_tool("complete_task", {"task_id": task_id})
        completed = complete_result.get("status") == "completed"
        verifications.append(make_verification(
            "Bidder1 completed task", completed,
            str(complete_result)[:200]))

        # Step 8: Verify final task state
        print("  [auction] Verifying task state...")
        tasks = poster_mcp.call_tool("list_tasks", {
            "channel_name": "task-market",
            "status": "completed",
        })
        completed_tasks = tasks.get("tasks", [])
        task_completed = any(t.get("id") == task_id for t in completed_tasks)
        verifications.append(make_verification(
            "Task shows as completed", task_completed,
            "{} completed tasks found".format(len(completed_tasks))))

        poster_mcp.close()
        bidder1_mcp.close()
        bidder2_mcp.close()

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
