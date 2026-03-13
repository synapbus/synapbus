#!/usr/bin/env python3
"""
SynapBus E2E Test Suite

Comprehensive end-to-end tests using Claude-powered agents communicating
through SynapBus MCP. Generates a self-contained HTML report.

Prerequisites:
    pip install anthropic httpx

Usage:
    # Auto-start server, run all scenarios, generate report:
    python tests/e2e/run_tests.py --auto-server

    # Run against existing server:
    python tests/e2e/run_tests.py --port 8080

    # Run single scenario:
    python tests/e2e/run_tests.py --auto-server --scenario direct_messaging

    # Keep server running for UI inspection:
    python tests/e2e/run_tests.py --auto-server --keep-server

    # Use a different model:
    python tests/e2e/run_tests.py --auto-server --model claude-haiku-4-5
"""
from __future__ import annotations

import argparse
import os
import signal
import sys
import time
from typing import List, Optional

# Ensure the test package is importable
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from e2e.lib.auth import create_anthropic_client
from e2e.lib.server import find_free_port, start_server, stop_server, wait_for_server
from e2e.lib.report import generate_report
from e2e.lib.agent_runner import TestResult

# Import scenarios
from e2e.scenarios import test_direct_messaging
from e2e.scenarios import test_channels
from e2e.scenarios import test_task_auction
from e2e.scenarios import test_access_control
from e2e.scenarios import test_agent_discovery
from e2e.scenarios import test_blackboard
from e2e.scenarios import test_audit_trail


# Scenario registry: (name, module)
SCENARIOS = [
    ("direct_messaging", test_direct_messaging),
    ("channels", test_channels),
    ("task_auction", test_task_auction),
    ("access_control", test_access_control),
    ("agent_discovery", test_agent_discovery),
    ("blackboard", test_blackboard),
    ("audit_trail", test_audit_trail),
]


def print_banner(base_url: str, model: str, scenarios: List[str]) -> None:
    print()
    print("=" * 60)
    print("  SynapBus E2E Test Suite")
    print("=" * 60)
    print("  Server:    {}".format(base_url))
    print("  Model:     {}".format(model))
    print("  Scenarios: {} ({})".format(len(scenarios), ", ".join(scenarios)))
    print("=" * 60)


def print_summary(results: List[TestResult], total_duration: float, report_path: str) -> None:
    passed = sum(1 for r in results if r.status == "pass")
    failed = sum(1 for r in results if r.status == "fail")
    errored = sum(1 for r in results if r.status == "error")
    total_input = sum(r.total_input_tokens for r in results)
    total_output = sum(r.total_output_tokens for r in results)

    print()
    print("=" * 60)
    print("  RESULTS")
    print("=" * 60)
    for r in results:
        icon = {"pass": "  OK ", "fail": " FAIL", "error": " ERR "}[r.status]
        print("  [{}] {} ({:.1f}s)".format(icon, r.name, r.duration))
        if r.status != "pass" and r.error:
            # Print first line of error
            first_line = r.error.strip().split("\n")[-1]
            print("         {}".format(first_line[:80]))

    print()
    print("  Total:  {} passed, {} failed, {} errors".format(passed, failed, errored))
    print("  Time:   {:.1f}s".format(total_duration))
    if total_input > 0 or total_output > 0:
        print("  Tokens: {} in / {} out".format(total_input, total_output))
        # Estimate cost (Sonnet pricing)
        cost = (total_input * 3 + total_output * 15) / 1_000_000
        print("  Cost:   ${:.4f}".format(cost))
    print("  Report: {}".format(os.path.abspath(report_path)))
    print("=" * 60)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="SynapBus E2E Test Suite",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--port", type=int, default=0,
                        help="SynapBus server port (0 = auto-assign)")
    parser.add_argument("--auto-server", action="store_true",
                        help="Auto-start and stop SynapBus server")
    parser.add_argument("--keep-server", action="store_true",
                        help="Keep server running after tests for UI inspection")
    parser.add_argument("--model", default="claude-sonnet-4-6",
                        help="Claude model to use (default: claude-sonnet-4-6)")
    parser.add_argument("--scenario", type=str, default=None,
                        help="Run a single scenario by name")
    parser.add_argument("--report", type=str, default="report.html",
                        help="Output HTML report path (default: report.html)")
    args = parser.parse_args()

    server_proc = None
    data_dir = None

    # Determine which scenarios to run
    if args.scenario:
        selected = [(name, mod) for name, mod in SCENARIOS if name == args.scenario]
        if not selected:
            available = [name for name, _ in SCENARIOS]
            print("ERROR: Unknown scenario '{}'. Available: {}".format(
                args.scenario, ", ".join(available)))
            return 1
    else:
        selected = SCENARIOS

    # Start or connect to server
    if args.auto_server or args.port == 0:
        port = args.port if args.port != 0 else find_free_port()
        print("Starting SynapBus server on port {}...".format(port))
        server_proc, data_dir, port = start_server(port)
        print("  Server started (pid={})".format(server_proc.pid))
    else:
        port = args.port
        base_url = "http://localhost:{}".format(port)
        if not wait_for_server(base_url, timeout=5):
            print("ERROR: Server at {} is not responding".format(base_url))
            return 1

    base_url = "http://localhost:{}".format(port)

    try:
        scenario_names = [name for name, _ in selected]
        print_banner(base_url, args.model, scenario_names)

        # Authenticate with Anthropic
        print("\n  Authenticating with Anthropic...")
        claude = create_anthropic_client()

        # Run scenarios
        results: List[TestResult] = []
        total_start = time.time()

        for i, (name, module) in enumerate(selected):
            print("\n--- [{}/{}] {} ---".format(i + 1, len(selected), name))
            result = module.run(claude, base_url, args.model)
            results.append(result)
            status_str = {"pass": "PASS", "fail": "FAIL", "error": "ERROR"}[result.status]
            print("  => {} ({:.1f}s)".format(status_str, result.duration))

        total_duration = time.time() - total_start

        # Generate report
        report_path = generate_report(
            results=results,
            model=args.model,
            base_url=base_url,
            total_duration=total_duration,
            output_path=args.report,
        )

        print_summary(results, total_duration, report_path)

        # Keep server running if requested
        if args.keep_server and server_proc:
            print("\n  Server running at {} -- press Ctrl+C to stop".format(base_url))
            try:
                signal.pause()
            except (KeyboardInterrupt, AttributeError):
                # AttributeError: signal.pause not available on some platforms
                try:
                    while True:
                        time.sleep(1)
                except KeyboardInterrupt:
                    pass
            print("\nStopping server...")

        # Return exit code
        if all(r.status == "pass" for r in results):
            return 0
        return 1

    finally:
        if server_proc and not args.keep_server:
            print("Stopping server...")
            stop_server(server_proc, data_dir)
        elif server_proc and args.keep_server:
            stop_server(server_proc, data_dir)


if __name__ == "__main__":
    sys.exit(main())
