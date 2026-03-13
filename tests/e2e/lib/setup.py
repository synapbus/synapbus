"""Register users and agents, return credentials."""
from __future__ import annotations

import json
import sys
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

import httpx


@dataclass
class AgentCredentials:
    """Credentials for a registered agent."""
    name: str
    display_name: str
    api_key: str
    agent_type: str
    capabilities: Dict[str, Any]


def register_user(base_url: str, username: str = "e2e_tester",
                   password: str = "testpass123456") -> httpx.Cookies:
    """Register a test user (ignore if exists) and return session cookies."""
    client = httpx.Client(timeout=10)
    try:
        # Register (ignore if exists)
        client.post("{}/auth/register".format(base_url), json={
            "username": username,
            "password": password,
            "display_name": "E2E Tester",
        })

        # Login
        resp = client.post("{}/auth/login".format(base_url), json={
            "username": username,
            "password": password,
        })
        if resp.status_code != 200:
            print("  [!] Login failed (status {}). Server may need a fresh data directory.".format(
                resp.status_code))
            sys.exit(1)

        return resp.cookies
    finally:
        client.close()


def register_agent(base_url: str, cookies: httpx.Cookies,
                    name: str, display_name: str,
                    agent_type: str = "ai",
                    capabilities: Optional[Dict[str, Any]] = None) -> AgentCredentials:
    """Register a single agent via the REST API, return credentials."""
    caps = capabilities or {}
    client = httpx.Client(timeout=10)
    try:
        resp = client.post("{}/api/agents".format(base_url), json={
            "name": name,
            "display_name": display_name,
            "type": agent_type,
            "capabilities": caps,
        }, cookies=cookies)

        data = resp.json()
        api_key = data.get("api_key", "")
        if not api_key:
            print("  [!] Agent registration failed for '{}': {}".format(name, data))
            sys.exit(1)

        return AgentCredentials(
            name=name,
            display_name=display_name,
            api_key=api_key,
            agent_type=agent_type,
            capabilities=caps,
        )
    finally:
        client.close()


def register_agents(base_url: str,
                     agents: List[Dict[str, Any]],
                     username: str = "e2e_tester",
                     password: str = "testpass123456") -> List[AgentCredentials]:
    """Register user + multiple agents. Returns list of AgentCredentials.

    Each dict in agents should have: name, display_name, and optionally
    type and capabilities.
    """
    cookies = register_user(base_url, username, password)
    result = []
    for agent_def in agents:
        cred = register_agent(
            base_url, cookies,
            name=agent_def["name"],
            display_name=agent_def["display_name"],
            agent_type=agent_def.get("type", "ai"),
            capabilities=agent_def.get("capabilities"),
        )
        result.append(cred)
    return result
