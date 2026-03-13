"""3-tier Anthropic authentication: API key, OAuth env, macOS Keychain."""
from __future__ import annotations

import json
import os
import subprocess
import sys

import anthropic


def create_anthropic_client() -> anthropic.Anthropic:
    """Create Anthropic client with 3-tier auth fallback.

    1. ANTHROPIC_API_KEY env var (standard API key)
    2. CLAUDE_CODE_OAUTH_TOKEN env var (OAuth token)
    3. macOS Keychain (Claude Code subscription credentials)
    """
    # Tier 1: Standard API key
    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if api_key:
        print("  Auth: using ANTHROPIC_API_KEY")
        return anthropic.Anthropic(api_key=api_key)

    # Tier 2: OAuth token from env
    oauth_token = os.environ.get("CLAUDE_CODE_OAUTH_TOKEN")

    # Tier 3: macOS Keychain (Claude Code stores credentials as JSON)
    if not oauth_token:
        try:
            raw = subprocess.check_output(
                ["security", "find-generic-password",
                 "-s", "Claude Code-credentials", "-w"],
                text=True, stderr=subprocess.DEVNULL,
            ).strip()
            if raw:
                creds = json.loads(raw)
                oauth_token = creds.get("claudeAiOauth", {}).get("accessToken")
                if oauth_token:
                    print("  Auth: using macOS Keychain (Claude Code subscription)")
        except (subprocess.CalledProcessError, FileNotFoundError, json.JSONDecodeError):
            pass

    if oauth_token:
        return anthropic.Anthropic(
            auth_token=oauth_token,
            default_headers={"anthropic-beta": "oauth-2025-04-20"},
        )

    print("ERROR: No Anthropic credentials found.")
    print("  Set ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN,")
    print("  or ensure Claude Code is logged in (macOS Keychain).")
    sys.exit(1)
