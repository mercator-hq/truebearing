"""PolicyProxy — wraps an LLM client to route tool calls through TrueBearing.

This module owns the lifecycle of the TrueBearing proxy subprocess (when running
in subprocess mode) and the header injection that associates every outbound
request with a session ID and a signed agent JWT.

Callers interact only with PolicyProxy. _find_free_port is module-level so it
can be patched in tests without needing to subclass PolicyProxy.
"""

import os
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Optional


class PolicyProxy:
    """Wraps an LLM client to route all MCP tool calls through the TrueBearing proxy.

    Two operating modes:

    Subprocess mode (default): PolicyProxy spawns ``truebearing serve`` on a
    random local port, waits for the /health endpoint to return 200, then
    reconfigures the wrapped client to use the proxy URL. The subprocess is
    terminated when the PolicyProxy is closed via context manager exit or
    garbage collection.

    Explicit proxy mode: caller provides ``proxy_url`` pointing to a proxy
    that is already running. No subprocess is spawned. This is the sidecar
    deployment model.

    Header injection: every outbound request carries:
      - ``Authorization: Bearer <jwt>`` — the agent JWT issued by
        ``truebearing agent register``.
      - ``X-TrueBearing-Session-ID: <uuid4>`` — the session identifier that
        scopes sequence evaluation and budget tracking to this agent run. One
        PolicyProxy instance equals one TrueBearing session.

    Usage (subprocess mode)::

        from truebearing import PolicyProxy
        import anthropic

        client = PolicyProxy(anthropic.Anthropic(), policy="./policy.yaml")
        # client.messages.create(...) routes through the proxy.

    Usage (explicit proxy URL)::

        client = PolicyProxy(
            anthropic.Anthropic(),
            proxy_url="http://localhost:7773",
            agent_jwt=os.environ["TRUEBEARING_AGENT_JWT"],
        )
    """

    # Maximum seconds to wait for the proxy subprocess to become ready.
    _READINESS_TIMEOUT_S = 30
    # Seconds between /health polls while waiting for subprocess readiness.
    _POLL_INTERVAL_S = 0.25

    def __init__(
        self,
        client,
        *,
        policy: Optional[str] = None,
        proxy_url: Optional[str] = None,
        agent_jwt: Optional[str] = None,
        agent_name: Optional[str] = None,
        session_id: Optional[str] = None,
        upstream: Optional[str] = None,
    ) -> None:
        """Initialise the PolicyProxy.

        Args:
            client: The LLM client to wrap (e.g. ``anthropic.Anthropic()``).
            policy: Path to a TrueBearing policy YAML file. Required when
                ``proxy_url`` is not provided (subprocess mode).
            proxy_url: URL of a running TrueBearing proxy. When given, no
                subprocess is spawned.
            agent_jwt: The agent JWT string. Takes precedence over
                ``TRUEBEARING_AGENT_JWT`` env var and the key file.
            agent_name: Agent name whose JWT is read from
                ``~/.truebearing/keys/<agent_name>.jwt``. Used only when
                ``agent_jwt`` is not provided and the env var is absent.
            session_id: Explicit session ID to continue a prior session.
                Defaults to a fresh ``uuid4`` when not provided.
            upstream: Upstream MCP server URL passed to ``truebearing serve``
                in subprocess mode. Defaults to ``http://localhost:8080``.
        """
        self._session_id: str = session_id or str(uuid.uuid4())
        # _proc is set only in subprocess mode; None means explicit proxy URL.
        self._proc: Optional[subprocess.Popen] = None

        self._jwt: Optional[str] = _resolve_jwt(agent_jwt, agent_name)

        if proxy_url is not None:
            self._proxy_url = proxy_url.rstrip("/")
        else:
            if policy is None:
                raise ValueError(
                    "'policy' is required when not providing an explicit 'proxy_url'. "
                    "Either pass policy='./policy.yaml' (subprocess mode) or "
                    "proxy_url='http://localhost:7773' (explicit proxy mode)."
                )
            port = _find_free_port()
            self._proxy_url = f"http://localhost:{port}"
            self._proc = _start_subprocess(port, policy, upstream)
            self._wait_for_ready()

        # Reconfigure the wrapped client to use the proxy URL and inject headers.
        self._client = _configure_client(client, self._proxy_url, self._jwt, self._session_id)

    def _wait_for_ready(self) -> None:
        """Poll /health until the proxy responds 200 or the timeout expires.

        Terminates the subprocess and raises RuntimeError on timeout so callers
        receive a clear error rather than a silent hang.
        """
        health_url = f"{self._proxy_url}/health"
        deadline = time.monotonic() + self._READINESS_TIMEOUT_S
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen(health_url, timeout=2) as resp:
                    if resp.status == 200:
                        return
            except (urllib.error.URLError, OSError):
                # Proxy not yet listening; keep polling.
                pass
            time.sleep(self._POLL_INTERVAL_S)

        # Timed out — kill the orphan subprocess before raising.
        if self._proc is not None:
            self._proc.terminate()
            self._proc = None
        raise RuntimeError(
            f"TrueBearing proxy did not become ready within {self._READINESS_TIMEOUT_S}s. "
            "Verify that the 'truebearing' binary is on PATH, the policy file is valid, "
            f"and no other process is occupying the port ({self._proxy_url})."
        )

    def __enter__(self) -> "PolicyProxy":
        return self

    def __exit__(self, *args: object) -> None:
        self._shutdown()

    def __del__(self) -> None:
        # Best-effort cleanup: do not raise from __del__.
        try:
            self._shutdown()
        except Exception:  # noqa: BLE001
            pass

    def _shutdown(self) -> None:
        """Terminate the proxy subprocess if one was spawned by this instance."""
        if self._proc is not None:
            self._proc.terminate()
            try:
                self._proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self._proc.kill()
                self._proc.wait()
            self._proc = None

    def __getattr__(self, name: str) -> object:
        """Delegate all attribute lookups to the reconfigured wrapped client.

        This makes PolicyProxy transparent: ``proxy.messages.create(...)``
        behaves identically to ``client.messages.create(...)`` but routes
        through the TrueBearing proxy with injected headers.
        """
        return getattr(self._client, name)


# ---------------------------------------------------------------------------
# Module-level helpers (separate functions so they are patchable in tests)
# ---------------------------------------------------------------------------


def _resolve_jwt(agent_jwt: Optional[str], agent_name: Optional[str]) -> Optional[str]:
    """Return the agent JWT from the first available source.

    Priority:
    1. ``agent_jwt`` kwarg (explicit).
    2. ``TRUEBEARING_AGENT_JWT`` environment variable.
    3. ``~/.truebearing/keys/<agent_name>.jwt`` file (when ``agent_name`` is given).
    """
    if agent_jwt:
        return agent_jwt
    env_val = os.environ.get("TRUEBEARING_AGENT_JWT")
    if env_val:
        return env_val
    if agent_name:
        key_path = Path.home() / ".truebearing" / "keys" / f"{agent_name}.jwt"
        return key_path.read_text().strip()
    return None


def _find_free_port() -> int:
    """Return an available TCP port on localhost.

    The socket is closed immediately after binding so the port is free for
    the subprocess to bind. There is a small TOCTOU window, but it is
    acceptable for local development use.
    """
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("", 0))
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        return s.getsockname()[1]


def _start_subprocess(port: int, policy: str, upstream: Optional[str]) -> "subprocess.Popen[bytes]":
    """Spawn ``truebearing serve`` and return the process handle.

    Output is suppressed because operators observe proxy behaviour via
    ``truebearing audit query``, not subprocess stdout/stderr. Keeping output
    suppressed also prevents the subprocess from interfering with the host
    process's terminal.
    """
    upstream_url = upstream or "http://localhost:8080"
    cmd = [
        "truebearing",
        "serve",
        "--port",
        str(port),
        "--policy",
        policy,
        "--upstream",
        upstream_url,
    ]
    return subprocess.Popen(
        cmd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def _configure_client(
    client: object,
    proxy_url: str,
    jwt: Optional[str],
    session_id: str,
) -> object:
    """Reconfigure ``client`` to route through the proxy and inject required headers.

    Supported client types:
      - ``anthropic.Anthropic`` (sync)
      - ``anthropic.AsyncAnthropic`` (async)

    Passing any other type raises ``ValueError`` with actionable instructions
    pointing the caller to the manual ``base_url`` workaround and the
    integrations documentation.

    Design: silent pass-through for unrecognised SDKs was deliberately removed.
    An unrecognised client returned unchanged would route tool calls directly to
    the upstream, bypassing TrueBearing enforcement entirely, with no indication
    of failure. A loud ValueError at construction time is the correct failure
    mode for a security proxy (fail-closed).
    """
    extra_headers: dict[str, str] = {}
    if jwt:
        extra_headers["Authorization"] = f"Bearer {jwt}"
    extra_headers["X-TrueBearing-Session-ID"] = session_id

    # Anthropic SDK (anthropic>=0.40): with_options returns a new client
    # instance with the supplied overrides applied. base_url routes all API
    # calls through the TrueBearing proxy.
    try:
        import anthropic  # type: ignore[import]

        if isinstance(client, anthropic.Anthropic):
            return client.with_options(
                base_url=proxy_url,
                default_headers=extra_headers,
            )
        if isinstance(client, anthropic.AsyncAnthropic):
            return client.with_options(
                base_url=proxy_url,
                default_headers=extra_headers,
            )
    except ImportError:
        pass

    raise ValueError(
        f"Unsupported client type: {type(client).__name__!r}. "
        "TrueBearing currently supports anthropic.Anthropic and anthropic.AsyncAnthropic. "
        "To use a different SDK, configure the proxy URL on your client manually: "
        f"client = YourClient(base_url='{proxy_url}'). "
        "See https://docs.mercator.dev/integrations for the full list of supported clients."
    )
