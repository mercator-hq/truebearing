"""TrueBearing Python SDK.

Exposes PolicyProxy — the single entry point for wrapping an LLM client to
route all MCP tool calls through the TrueBearing transparent proxy.

Example::

    from truebearing import PolicyProxy
    import anthropic

    client = PolicyProxy(anthropic.Anthropic(), policy="./policy.yaml")
    # client.messages.create(...) now enforces your policy on every tool call.
"""

from truebearing._proxy import PolicyProxy

__all__ = ["PolicyProxy"]
