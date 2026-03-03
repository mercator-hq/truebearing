"""Tests for truebearing.PolicyProxy.

All tests run without requiring a live TrueBearing binary or a real Anthropic
SDK installation. SDK detection is exercised by patching sys.modules; subprocess
lifecycle is exercised with unittest.mock.patch.
"""

import subprocess
import uuid
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from truebearing import PolicyProxy
from truebearing._proxy import _find_free_port, _resolve_jwt


# ---------------------------------------------------------------------------
# Minimal stand-in for anthropic.Anthropic used across all tests.
# ---------------------------------------------------------------------------


class _FakeAnthropic:
    """Minimal anthropic.Anthropic substitute that records with_options calls."""

    # Class-level attribute so all instances (including those returned by
    # with_options) expose proxy.messages for the delegation test.
    messages = "messages-object"

    def __init__(self, base_url=None, default_headers=None):
        self.base_url = base_url
        self.default_headers = default_headers or {}
        self._configured: dict = {}

    def with_options(self, base_url=None, default_headers=None):
        configured = _FakeAnthropic(base_url=base_url, default_headers=default_headers)
        configured._configured = {"base_url": base_url, "default_headers": default_headers or {}}
        return configured


def _fake_anthropic_module():
    """Return a mock of the anthropic module with _FakeAnthropic as both client classes."""
    mod = MagicMock()
    mod.Anthropic = _FakeAnthropic
    # AsyncAnthropic uses the same fake so isinstance checks work for both branches.
    mod.AsyncAnthropic = _FakeAnthropic
    return mod


# ---------------------------------------------------------------------------
# _find_free_port
# ---------------------------------------------------------------------------


def test_find_free_port_returns_nonzero_int():
    port = _find_free_port()
    assert isinstance(port, int)
    assert 1024 <= port <= 65535


# ---------------------------------------------------------------------------
# _resolve_jwt
# ---------------------------------------------------------------------------


def test_resolve_jwt_kwarg_wins(monkeypatch):
    monkeypatch.setenv("TRUEBEARING_AGENT_JWT", "env-value")
    assert _resolve_jwt("kwarg-value", None) == "kwarg-value"


def test_resolve_jwt_env_var(monkeypatch):
    monkeypatch.setenv("TRUEBEARING_AGENT_JWT", "env-value")
    assert _resolve_jwt(None, None) == "env-value"


def test_resolve_jwt_key_file(tmp_path, monkeypatch):
    monkeypatch.delenv("TRUEBEARING_AGENT_JWT", raising=False)
    keys_dir = tmp_path / ".truebearing" / "keys"
    keys_dir.mkdir(parents=True)
    (keys_dir / "my-agent.jwt").write_text("file-value\n")
    with patch.object(Path, "home", return_value=tmp_path):
        assert _resolve_jwt(None, "my-agent") == "file-value"


def test_resolve_jwt_none_when_nothing_provided(monkeypatch):
    monkeypatch.delenv("TRUEBEARING_AGENT_JWT", raising=False)
    assert _resolve_jwt(None, None) is None


# ---------------------------------------------------------------------------
# Session ID
# ---------------------------------------------------------------------------


def test_session_id_generated_when_not_provided():
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            _FakeAnthropic(),
            proxy_url="http://localhost:7773",
        )
    # Must be a valid uuid4.
    parsed = uuid.UUID(proxy._session_id)
    assert parsed.version == 4


def test_session_id_explicit_preserved():
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            _FakeAnthropic(),
            proxy_url="http://localhost:7773",
            session_id="explicit-session-id",
        )
    assert proxy._session_id == "explicit-session-id"


def test_two_proxies_get_different_session_ids():
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        p1 = PolicyProxy(_FakeAnthropic(), proxy_url="http://localhost:7773")
        p2 = PolicyProxy(_FakeAnthropic(), proxy_url="http://localhost:7773")
    assert p1._session_id != p2._session_id


# ---------------------------------------------------------------------------
# Header injection (Anthropic client)
# ---------------------------------------------------------------------------


def test_headers_injected_into_anthropic_client():
    """PolicyProxy reconfigures the Anthropic client with proxy URL and headers."""
    client = _FakeAnthropic()
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            client,
            proxy_url="http://localhost:7773",
            agent_jwt="test-jwt",
            session_id="fixed-session-id",
        )
    assert proxy._client._configured["base_url"] == "http://localhost:7773"
    headers = proxy._client._configured["default_headers"]
    assert headers["Authorization"] == "Bearer test-jwt"
    assert headers["X-TrueBearing-Session-ID"] == "fixed-session-id"


def test_session_id_header_matches_proxy_session_id():
    """X-TrueBearing-Session-ID header must equal proxy._session_id."""
    client = _FakeAnthropic()
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            client,
            proxy_url="http://localhost:7773",
            agent_jwt="test-jwt",
        )
    headers = proxy._client._configured["default_headers"]
    assert headers["X-TrueBearing-Session-ID"] == proxy._session_id


def test_authorization_header_absent_when_no_jwt(monkeypatch):
    """No Authorization header is injected when no JWT is resolvable."""
    monkeypatch.delenv("TRUEBEARING_AGENT_JWT", raising=False)
    client = _FakeAnthropic()
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            client,
            proxy_url="http://localhost:7773",
        )
    headers = proxy._client._configured["default_headers"]
    assert "Authorization" not in headers


def test_trailing_slash_stripped_from_proxy_url():
    """Trailing slash is stripped from proxy_url before header injection."""
    client = _FakeAnthropic()
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            client,
            proxy_url="http://localhost:7773/",
            agent_jwt="jwt",
        )
    assert proxy._client._configured["base_url"] == "http://localhost:7773"


# ---------------------------------------------------------------------------
# Attribute delegation
# ---------------------------------------------------------------------------


def test_getattr_delegates_to_wrapped_client():
    """proxy.messages delegates to the reconfigured client.messages."""
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(_FakeAnthropic(), proxy_url="http://localhost:7773")
    assert proxy.messages == "messages-object"


# ---------------------------------------------------------------------------
# Subprocess lifecycle
# ---------------------------------------------------------------------------


def _make_mock_proc():
    proc = MagicMock(spec=subprocess.Popen)
    proc.terminate = MagicMock()
    proc.wait = MagicMock()
    proc.kill = MagicMock()
    return proc


def test_subprocess_spawned_with_correct_args(tmp_path):
    """Subprocess mode runs 'truebearing serve' with the expected flags."""
    policy_file = tmp_path / "test.policy.yaml"
    policy_file.write_text('version: "1"\n')
    mock_proc = _make_mock_proc()

    with (
        patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}),
        patch("truebearing._proxy.subprocess.Popen", return_value=mock_proc) as mock_popen,
        patch("truebearing._proxy._find_free_port", return_value=19997),
        patch("truebearing._proxy.PolicyProxy._wait_for_ready"),
    ):
        PolicyProxy(
            _FakeAnthropic(),
            policy=str(policy_file),
            agent_jwt="test-jwt",
        )

    args = mock_popen.call_args[0][0]
    assert args[0] == "truebearing"
    assert "serve" in args
    assert "--port" in args
    port_idx = args.index("--port")
    assert args[port_idx + 1] == "19997"
    assert "--policy" in args
    policy_idx = args.index("--policy")
    assert args[policy_idx + 1] == str(policy_file)
    assert "--upstream" in args


def test_subprocess_uses_provided_upstream(tmp_path):
    """The upstream kwarg is forwarded to truebearing serve."""
    policy_file = tmp_path / "p.yaml"
    policy_file.write_text('version: "1"\n')
    mock_proc = _make_mock_proc()

    with (
        patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}),
        patch("truebearing._proxy.subprocess.Popen", return_value=mock_proc) as mock_popen,
        patch("truebearing._proxy._find_free_port", return_value=19996),
        patch("truebearing._proxy.PolicyProxy._wait_for_ready"),
    ):
        PolicyProxy(
            _FakeAnthropic(),
            policy=str(policy_file),
            upstream="https://mcp.example.com",
        )

    args = mock_popen.call_args[0][0]
    upstream_idx = args.index("--upstream")
    assert args[upstream_idx + 1] == "https://mcp.example.com"


def test_subprocess_output_is_suppressed(tmp_path):
    """Proxy subprocess stdout and stderr are redirected to DEVNULL."""
    policy_file = tmp_path / "p.yaml"
    policy_file.write_text('version: "1"\n')
    mock_proc = _make_mock_proc()

    with (
        patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}),
        patch("truebearing._proxy.subprocess.Popen", return_value=mock_proc) as mock_popen,
        patch("truebearing._proxy._find_free_port", return_value=19995),
        patch("truebearing._proxy.PolicyProxy._wait_for_ready"),
    ):
        PolicyProxy(
            _FakeAnthropic(),
            policy=str(policy_file),
            agent_jwt="jwt",
        )

    kwargs = mock_popen.call_args[1]
    assert kwargs["stdout"] == subprocess.DEVNULL
    assert kwargs["stderr"] == subprocess.DEVNULL


def test_no_subprocess_with_explicit_proxy_url():
    """No subprocess is spawned when proxy_url is supplied."""
    with (
        patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}),
        patch("truebearing._proxy.subprocess.Popen") as mock_popen,
    ):
        proxy = PolicyProxy(
            _FakeAnthropic(),
            proxy_url="http://localhost:7773",
            agent_jwt="jwt",
        )
    mock_popen.assert_not_called()
    assert proxy._proc is None


def test_context_manager_terminates_subprocess(tmp_path):
    """__exit__ calls terminate() on the subprocess handle."""
    policy_file = tmp_path / "p.yaml"
    policy_file.write_text('version: "1"\n')
    mock_proc = _make_mock_proc()

    with (
        patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}),
        patch("truebearing._proxy.subprocess.Popen", return_value=mock_proc),
        patch("truebearing._proxy._find_free_port", return_value=19994),
        patch("truebearing._proxy.PolicyProxy._wait_for_ready"),
    ):
        with PolicyProxy(
            _FakeAnthropic(),
            policy=str(policy_file),
            agent_jwt="jwt",
        ):
            pass

    mock_proc.terminate.assert_called_once()


def test_shutdown_idempotent(tmp_path):
    """Calling _shutdown() twice does not raise or double-terminate."""
    policy_file = tmp_path / "p.yaml"
    policy_file.write_text('version: "1"\n')
    mock_proc = _make_mock_proc()

    with (
        patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}),
        patch("truebearing._proxy.subprocess.Popen", return_value=mock_proc),
        patch("truebearing._proxy._find_free_port", return_value=19993),
        patch("truebearing._proxy.PolicyProxy._wait_for_ready"),
    ):
        proxy = PolicyProxy(
            _FakeAnthropic(),
            policy=str(policy_file),
            agent_jwt="jwt",
        )

    proxy._shutdown()
    proxy._shutdown()  # second call must be a no-op
    mock_proc.terminate.assert_called_once()


# ---------------------------------------------------------------------------
# Validation errors
# ---------------------------------------------------------------------------


def test_policy_required_without_proxy_url():
    """ValueError is raised when no policy and no proxy_url are given."""
    with pytest.raises(ValueError, match="'policy' is required"):
        PolicyProxy(_FakeAnthropic(), agent_jwt="jwt")


# ---------------------------------------------------------------------------
# _wait_for_ready timeout
# ---------------------------------------------------------------------------


def test_wait_for_ready_raises_on_timeout(tmp_path):
    """RuntimeError is raised and subprocess terminated if proxy never becomes ready."""
    policy_file = tmp_path / "p.yaml"
    policy_file.write_text('version: "1"\n')
    mock_proc = _make_mock_proc()

    with (
        patch("truebearing._proxy.subprocess.Popen", return_value=mock_proc),
        patch("truebearing._proxy._find_free_port", return_value=19992),
        # Simulate the health endpoint always failing.
        patch("truebearing._proxy.urllib.request.urlopen", side_effect=OSError("refused")),
        # Speed up the timeout so the test runs fast.
        patch.object(PolicyProxy, "_READINESS_TIMEOUT_S", 0.1),
        patch.object(PolicyProxy, "_POLL_INTERVAL_S", 0.05),
    ):
        with pytest.raises(RuntimeError, match="did not become ready"):
            PolicyProxy(
                _FakeAnthropic(),
                policy=str(policy_file),
                agent_jwt="jwt",
            )

    mock_proc.terminate.assert_called_once()


# ---------------------------------------------------------------------------
# Unsupported client type (Task 14.1)
# ---------------------------------------------------------------------------


def test_unsupported_client_raises_value_error():
    """Passing an unrecognised client type raises ValueError with actionable instructions."""
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        with pytest.raises(ValueError, match="Unsupported client type"):
            PolicyProxy(MagicMock(), proxy_url="http://localhost:7773")


def test_unsupported_client_error_names_the_type():
    """ValueError message names the actual unsupported type for easy diagnosis."""

    class _OpenAILike:
        pass

    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        with pytest.raises(ValueError, match="_OpenAILike"):
            PolicyProxy(_OpenAILike(), proxy_url="http://localhost:7773")


def test_unsupported_client_error_includes_docs_link():
    """ValueError message includes the integrations documentation link."""
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        with pytest.raises(ValueError, match="docs.mercator.dev/integrations"):
            PolicyProxy(MagicMock(), proxy_url="http://localhost:7773")


def test_anthropic_client_does_not_raise():
    """A recognised anthropic.Anthropic instance is accepted without error."""
    with patch.dict("sys.modules", {"anthropic": _fake_anthropic_module()}):
        proxy = PolicyProxy(
            _FakeAnthropic(),
            proxy_url="http://localhost:7773",
            agent_jwt="jwt",
        )
    assert proxy._client is not None
