package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// minimalTestPolicyYAML is a small valid policy YAML used across proxy tests.
// It avoids a filesystem dependency on testdata/ from within a unit test.
const minimalTestPolicyYAML = `
version: "1"
agent: test-agent
enforcement_mode: block
may_use:
  - some_tool
`

// parseTestPolicy parses minimalTestPolicyYAML and fails the test on error.
func parseTestPolicy(t *testing.T) *policy.Policy {
	t.Helper()
	pol, err := policy.ParseBytes([]byte(minimalTestPolicyYAML), "test-policy.yaml")
	if err != nil {
		t.Fatalf("parsing test policy: %v", err)
	}
	return pol
}

// newTestUpstream returns an httptest.Server that records whether it received
// a request and responds with a minimal JSON-RPC 2.0 success body.
func newTestUpstream(t *testing.T, received *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if received != nil {
			*received = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestProxyServer sets up a complete proxy with a registered test agent and
// returns the proxy httptest.Server, the upstream httptest.Server, and a valid
// JWT for the registered "test-agent".
func newTestProxyServer(t *testing.T, upstreamReceived *bool) (proxyServer *httptest.Server, token string) {
	t.Helper()

	upstream := newTestUpstream(t, upstreamReceived)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, priv := registerTestAgent(t, st, "test-agent")
	token = mintTestToken(t, priv, "test-agent", time.Hour)

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "")

	proxyServer = httptest.NewServer(p.Handler())
	t.Cleanup(proxyServer.Close)
	return proxyServer, token
}

// TestProxy_HandlerServesRequests verifies that the proxy starts and accepts
// HTTP connections. An unauthenticated request should return 401, not a
// connection error — proving the server is listening.
func TestProxy_HandlerServesRequests(t *testing.T) {
	proxyServer, _ := newTestProxyServer(t, nil)

	resp, err := http.Post(proxyServer.URL+"/mcp/v1", "application/json", strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("proxy server not reachable: %v", err)
	}
	defer resp.Body.Close()

	// Without auth the proxy must return 401, confirming it is running and
	// handling requests (as opposed to returning a connection error).
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: want 401 (auth required to prove server is up), got %d", resp.StatusCode)
	}
}

// TestProxy_NonToolRequest_ForwardedUpstream verifies that non-tool MCP methods
// (e.g. tools/list) are forwarded directly to the upstream after JWT validation
// without requiring a session ID header.
func TestProxy_NonToolRequest_ForwardedUpstream(t *testing.T) {
	var upstreamReceived bool
	proxyServer, token := newTestProxyServer(t, &upstreamReceived)

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(toolsListBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: want 200, got %d", resp.StatusCode)
	}
	if !upstreamReceived {
		t.Error("upstream did not receive the forwarded non-tool request")
	}
}

// TestProxy_ToolCall_MissingJWT_Returns401 verifies that a tool call without
// an Authorization: Bearer header is rejected with 401 before any forwarding.
func TestProxy_ToolCall_MissingJWT_Returns401(t *testing.T) {
	var upstreamReceived bool
	proxyServer, _ := newTestProxyServer(t, &upstreamReceived)

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TrueBearing-Session-ID", "sess-test-123")
	// No Authorization header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", resp.StatusCode)
	}
	if upstreamReceived {
		t.Error("upstream must not receive a request with a missing JWT")
	}
}

// TestProxy_ToolCall_MissingSessionID_Returns400 verifies that a tool call
// with a valid JWT but no X-TrueBearing-Session-ID header is rejected with 400.
func TestProxy_ToolCall_MissingSessionID_Returns400(t *testing.T) {
	var upstreamReceived bool
	proxyServer, token := newTestProxyServer(t, &upstreamReceived)

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	// No X-TrueBearing-Session-ID header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", resp.StatusCode)
	}
	if upstreamReceived {
		t.Error("upstream must not receive a request with a missing session ID")
	}
}

// TestProxy_ToolCall_ValidAuth_ForwardedUpstream verifies that a tools/call
// request with a valid JWT and session ID header is forwarded to the upstream
// by the stub evaluation pipeline (which always allows).
func TestProxy_ToolCall_ValidAuth_ForwardedUpstream(t *testing.T) {
	var upstreamReceived bool
	proxyServer, token := newTestProxyServer(t, &upstreamReceived)

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-TrueBearing-Session-ID", "sess-test-123")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: want 200, got %d", resp.StatusCode)
	}
	if !upstreamReceived {
		t.Error("upstream did not receive the forwarded tool call")
	}
}
