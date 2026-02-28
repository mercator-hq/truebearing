package proxy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// toolsCallBodyDenied is a tools/call request for a tool that is NOT in the
// test policy's may_use list, so it will always be denied by MayUseEvaluator.
const toolsCallBodyDenied = `{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{"name":"forbidden_tool","arguments":{}}}`

// newTestProxy builds a Proxy pointing at a test upstream and returns the
// proxy, the store, and a valid JWT for "test-agent". Unlike
// newTestProxyServer it does not bind a TCP port — suitable for ServeStdio
// tests that drive the proxy directly.
func newTestProxy(t *testing.T, upstreamReceived *bool) (*Proxy, *store.Store, string) {
	t.Helper()

	upstream := newTestUpstream(t, upstreamReceived)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	_, proxyPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating proxy signing keypair: %v", err)
	}

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", proxyPriv)
	return p, st, token
}

// runStdio calls ServeStdio with stdinContent as the full input stream and
// returns the captured stdout string and any error. Because strings.NewReader
// produces EOF after its content, ServeStdio returns as soon as all lines are
// processed — making this helper synchronous.
func runStdio(t *testing.T, p *Proxy, stdinContent, token string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := p.ServeStdio(context.Background(), strings.NewReader(stdinContent), &out, token)
	return out.String(), err
}

// TestProxy_ServeStdio_ToolCall_Allowed verifies that a valid tools/call with a
// valid JWT produces a JSON-RPC result on stdout and is forwarded to upstream.
func TestProxy_ServeStdio_ToolCall_Allowed(t *testing.T) {
	var upstreamReceived bool
	p, _, token := newTestProxy(t, &upstreamReceived)

	stdout, err := runStdio(t, p, toolsCallBody+"\n", token)

	if err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	if !upstreamReceived {
		t.Error("upstream must receive a forwarded allowed tool call")
	}

	// Response must be valid JSON with a result field and no error field.
	line := strings.TrimRight(stdout, "\n")
	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("stdout is not valid JSON: %v — got %q", err, stdout)
	}
	if _, hasResult := resp["result"]; !hasResult {
		t.Errorf("stdout JSON-RPC response must have a result field, got %q", stdout)
	}
	if _, hasError := resp["error"]; hasError {
		t.Errorf("stdout JSON-RPC response must not have an error field for an allowed call, got %q", stdout)
	}
}

// TestProxy_ServeStdio_MissingJWT_DeniesToolCall verifies that an empty
// tokenString causes tool calls to be denied with an authorization error on
// stdout. The upstream must not receive any forwarded request.
func TestProxy_ServeStdio_MissingJWT_DeniesToolCall(t *testing.T) {
	var upstreamReceived bool
	p, _, _ := newTestProxy(t, &upstreamReceived)

	// Empty token — AuthMiddleware sees no Authorization header.
	stdout, err := runStdio(t, p, toolsCallBody+"\n", "")

	if err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	if upstreamReceived {
		t.Error("upstream must not receive a request when JWT is absent")
	}
	if !strings.Contains(stdout, `"unauthorized"`) {
		t.Errorf("stdout must contain an unauthorized error, got %q", stdout)
	}
}

// TestProxy_ServeStdio_ToolNotInMayUse_DeniedWithError verifies that calling a
// tool absent from may_use produces a JSON-RPC error on stdout and the upstream
// is not contacted.
func TestProxy_ServeStdio_ToolNotInMayUse_DeniedWithError(t *testing.T) {
	var upstreamReceived bool
	p, _, token := newTestProxy(t, &upstreamReceived)

	stdout, err := runStdio(t, p, toolsCallBodyDenied+"\n", token)

	if err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	if upstreamReceived {
		t.Error("upstream must not receive a request denied by may_use")
	}

	// Response must be a JSON-RPC error (not a result).
	line := strings.TrimRight(stdout, "\n")
	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("stdout is not valid JSON: %v — got %q", err, stdout)
	}
	if _, hasError := resp["error"]; !hasError {
		t.Errorf("stdout JSON-RPC response must have an error field for a denied call, got %q", stdout)
	}
}

// TestProxy_ServeStdio_NonToolRequest_Forwarded verifies that non-tool MCP
// methods (e.g. tools/list) are forwarded to upstream after JWT validation.
// No session ID header is required for non-tool methods.
func TestProxy_ServeStdio_NonToolRequest_Forwarded(t *testing.T) {
	var upstreamReceived bool
	p, _, token := newTestProxy(t, &upstreamReceived)

	stdout, err := runStdio(t, p, toolsListBody+"\n", token)

	if err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	if !upstreamReceived {
		t.Error("upstream must receive a forwarded non-tool request")
	}
	if !strings.Contains(stdout, `"result"`) {
		t.Errorf("stdout must contain a JSON-RPC result, got %q", stdout)
	}
}

// TestProxy_ServeStdio_EmptyLines_Skipped verifies that blank lines in the
// input stream produce no output and no error. Empty lines are valid whitespace
// in newline-delimited streams and must be silently ignored.
func TestProxy_ServeStdio_EmptyLines_Skipped(t *testing.T) {
	p, _, _ := newTestProxy(t, nil)

	stdout, err := runStdio(t, p, "\n\n\n", "")

	if err != nil {
		t.Fatalf("ServeStdio returned unexpected error for empty input: %v", err)
	}
	if stdout != "" {
		t.Errorf("empty input must produce no output, got %q", stdout)
	}
}

// TestProxy_ServeStdio_MultipleRequests_ShareSession verifies that successive
// tool calls in a single stdio stream share the same session ID. This matches
// the one-session-per-connection model described in the Design comment on
// ServeStdio.
func TestProxy_ServeStdio_MultipleRequests_ShareSession(t *testing.T) {
	p, st, token := newTestProxy(t, nil)

	twoLines := toolsCallBody + "\n" + toolsCallBody + "\n"
	_, err := runStdio(t, p, twoLines, token)
	if err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	// Both audit records must carry the same session ID.
	records, err := st.QueryAuditLog(store.AuditFilter{})
	if err != nil {
		t.Fatalf("querying audit log: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("audit records: want 2, got %d", len(records))
	}
	if records[0].SessionID != records[1].SessionID {
		t.Errorf("session IDs must match across a single stdio session; got %q and %q",
			records[0].SessionID, records[1].SessionID)
	}
	if records[0].SessionID == "" {
		t.Error("session ID must not be empty")
	}
}
