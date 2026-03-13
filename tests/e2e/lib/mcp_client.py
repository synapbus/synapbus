"""SynapBus MCP Streamable HTTP client with request/response logging."""
from __future__ import annotations

import json
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

import httpx


@dataclass
class MCPLog:
    """Single MCP request/response log entry."""
    method: str
    params: Optional[Dict[str, Any]]
    response: Any
    error: Optional[str]
    duration: float
    timestamp: float


class SynapBusMCP:
    """Thin MCP client for SynapBus Streamable HTTP transport with logging."""

    def __init__(self, base_url: str, api_key: Optional[str] = None):
        self.url = "{}/mcp".format(base_url)
        self.api_key = api_key
        self.session_id: Optional[str] = None
        self._req_id = 0
        self._client = httpx.Client(timeout=30)
        self.logs: List[MCPLog] = []

    def _next_id(self) -> int:
        self._req_id += 1
        return self._req_id

    def _headers(self) -> Dict[str, str]:
        h: Dict[str, str] = {"Content-Type": "application/json"}
        if self.api_key:
            h["Authorization"] = "Bearer {}".format(self.api_key)
        if self.session_id:
            h["Mcp-Session-Id"] = self.session_id
        return h

    def _rpc(self, method: str, params: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        body: Dict[str, Any] = {
            "jsonrpc": "2.0",
            "id": self._next_id(),
            "method": method,
        }
        if params:
            body["params"] = params

        start = time.time()
        error_str: Optional[str] = None
        response: Any = None
        try:
            resp = self._client.post(self.url, json=body, headers=self._headers())
            resp.raise_for_status()

            if sid := resp.headers.get("Mcp-Session-Id"):
                self.session_id = sid

            response = resp.json()
            return response
        except Exception as e:
            error_str = str(e)
            raise
        finally:
            self.logs.append(MCPLog(
                method=method,
                params=params,
                response=response,
                error=error_str,
                duration=time.time() - start,
                timestamp=time.time(),
            ))

    def initialize(self) -> Dict[str, Any]:
        return self._rpc("initialize", {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "synapbus-e2e-test", "version": "2.0"},
        })

    def call_tool(self, name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
        """Call an MCP tool. Returns parsed JSON result or error dict."""
        result = self._rpc("tools/call", {"name": name, "arguments": arguments})
        if "error" in result:
            return result
        content = result.get("result", {}).get("content", [])
        # Check for isError flag in result
        is_error = result.get("result", {}).get("isError", False)
        for block in content:
            if block.get("type") == "text":
                try:
                    parsed = json.loads(block["text"])
                    if is_error:
                        parsed["_mcp_error"] = True
                    return parsed
                except json.JSONDecodeError:
                    return {"_raw": block["text"], "_mcp_error": is_error}
        return result

    def call_tool_raw(self, name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
        """Call an MCP tool, returning the full JSON-RPC response."""
        return self._rpc("tools/call", {"name": name, "arguments": arguments})

    def list_tools(self) -> List[Dict[str, Any]]:
        result = self._rpc("tools/list")
        return result.get("result", {}).get("tools", [])

    def close(self) -> None:
        self._client.close()
