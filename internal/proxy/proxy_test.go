package proxy

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/audit"
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
// returns the proxy httptest.Server and a valid JWT for the registered "test-agent".
// A throwaway Ed25519 signing keypair is generated for the proxy; tests that need
// to verify audit record signatures should use the expanded setup in
// TestProxy_ToolCall_AuditRecordWritten instead.
func newTestProxyServer(t *testing.T, upstreamReceived *bool) (proxyServer *httptest.Server, token string) {
	t.Helper()

	upstream := newTestUpstream(t, upstreamReceived)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token = mintTestToken(t, agentPriv, "test-agent", time.Hour)

	_, proxyPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating proxy signing keypair: %v", err)
	}

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", proxyPriv)

	proxyServer = httptest.NewServer(p.Handler())
	t.Cleanup(proxyServer.Close)
	return proxyServer, token
}

// TestProxy_ToolCall_AuditRecordWritten verifies that a successful tool call
// produces exactly one signed audit record in the store, with the correct
// session ID, tool name, decision, and a valid Ed25519 signature.
func TestProxy_ToolCall_AuditRecordWritten(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	proxyPub, proxyPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating proxy signing keypair: %v", err)
	}

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", proxyPriv)
	proxyServer := httptest.NewServer(p.Handler())
	t.Cleanup(proxyServer.Close)

	const sessionID = "sess-audit-test-123"
	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TrueBearing-Session-ID", sessionID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}

	records, err := st.QueryAuditLog(store.AuditFilter{SessionID: sessionID})
	if err != nil {
		t.Fatalf("querying audit log: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("audit records: want 1, got %d", len(records))
	}

	r := records[0]
	if r.SessionID != sessionID {
		t.Errorf("SessionID: want %q, got %q", sessionID, r.SessionID)
	}
	if r.ToolName != "some_tool" {
		t.Errorf("ToolName: want %q, got %q", "some_tool", r.ToolName)
	}
	if r.Decision != "allow" {
		t.Errorf("Decision: want %q, got %q", "allow", r.Decision)
	}
	if r.AgentName != "test-agent" {
		t.Errorf("AgentName: want %q, got %q", "test-agent", r.AgentName)
	}
	if r.Seq != 1 {
		t.Errorf("Seq: want 1, got %d", r.Seq)
	}
	if r.Signature == "" {
		t.Fatal("Signature must not be empty")
	}

	// Verify the Ed25519 signature against the proxy's public key.
	auditRec := &audit.AuditRecord{
		ID:                r.ID,
		SessionID:         r.SessionID,
		Seq:               r.Seq,
		AgentName:         r.AgentName,
		ToolName:          r.ToolName,
		ArgumentsSHA256:   r.ArgumentsSHA256,
		Decision:          r.Decision,
		DecisionReason:    r.DecisionReason,
		PolicyFingerprint: r.PolicyFingerprint,
		AgentJWTSHA256:    r.AgentJWTSHA256,
		ClientTraceID:     r.ClientTraceID,
		RecordedAt:        r.RecordedAt,
		Signature:         r.Signature,
	}
	if verr := audit.Verify(auditRec, proxyPub); verr != nil {
		t.Errorf("audit.Verify: %v", verr)
	}
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

// TestProxy_DenyResponse_ContainsStructuredFeedback verifies that a denied tool
// call response carries the structured data object required by Task 14.3:
// error.data.reason_code and error.data.suggestion must be non-empty so that
// LLM agents can parse the denial and self-correct without human intervention.
func TestProxy_DenyResponse_ContainsStructuredFeedback(t *testing.T) {
	proxyServer, token := newTestProxyServer(t, nil)

	// Call a tool that is NOT in the may_use list. This triggers MayUseEvaluator
	// to deny with reason_code "may_use_denied".
	deniedBody := `{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{"name":"not_in_policy","arguments":{}}}`

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(deniedBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TrueBearing-Session-ID", "sess-deny-feedback-test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200 (JSON-RPC errors use 200), got %d", resp.StatusCode)
	}

	// Parse the JSON-RPC error response to verify the structured data fields.
	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				BlockedTool string `json:"blocked_tool"`
				ReasonCode  string `json:"reason_code"`
				Suggestion  string `json:"suggestion"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}

	if body.Error.Code != -32000 {
		t.Errorf("error.code: want -32000, got %d", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("error.message must not be empty")
	}
	if body.Error.Data.BlockedTool != "not_in_policy" {
		t.Errorf("error.data.blocked_tool: want %q, got %q", "not_in_policy", body.Error.Data.BlockedTool)
	}
	if body.Error.Data.ReasonCode != "may_use_denied" {
		t.Errorf("error.data.reason_code: want %q, got %q", "may_use_denied", body.Error.Data.ReasonCode)
	}
	if body.Error.Data.Suggestion == "" {
		t.Error("error.data.suggestion must not be empty")
	}
}

// TestProxy_CaptureTrace_WritesEntries verifies that --capture-trace writes
// exactly one JSONL line per tool call, with the correct session_id, agent_name,
// tool_name, and a non-empty requested_at. Both calls share the same session ID
// to confirm entries accumulate in append order.
func TestProxy_CaptureTrace_WritesEntries(t *testing.T) {
	var upstreamReceived int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	// Create a temporary file for trace output. We close it immediately so
	// the TraceWriter can open it in append mode without a second file handle.
	traceFile, err := os.CreateTemp(t.TempDir(), "trace-*.jsonl")
	if err != nil {
		t.Fatalf("creating temp trace file: %v", err)
	}
	tracePath := traceFile.Name()
	if err := traceFile.Close(); err != nil {
		t.Fatalf("closing temp file before TraceWriter: %v", err)
	}

	tw, err := NewTraceWriter(tracePath)
	if err != nil {
		t.Fatalf("NewTraceWriter: %v", err)
	}
	t.Cleanup(func() { _ = tw.Close() })

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	_, proxyPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating proxy signing keypair: %v", err)
	}

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", proxyPriv)
	p.SetTraceWriter(tw)

	proxyServer := httptest.NewServer(p.Handler())
	t.Cleanup(proxyServer.Close)

	const sessionID = "sess-trace-capture-001"

	// Make two tool calls on the same session.
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/mcp/v1", strings.NewReader(toolsCallBody))
		if err != nil {
			t.Fatalf("building request %d: %v", i+1, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-TrueBearing-Session-ID", sessionID)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("making request %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
	}

	// Close the TraceWriter so the OS closes the file descriptor before we
	// read back the contents.
	if err := tw.Close(); err != nil {
		t.Fatalf("closing TraceWriter: %v", err)
	}

	// Read and validate the trace file.
	f, err := os.Open(tracePath)
	if err != nil {
		t.Fatalf("opening trace file: %v", err)
	}
	defer func() { _ = f.Close() }()

	var entries []TraceEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var e TraceEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parsing trace line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading trace file: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("trace entries: want 2, got %d", len(entries))
	}

	for i, e := range entries {
		if e.SessionID != sessionID {
			t.Errorf("entry %d SessionID: want %q, got %q", i+1, sessionID, e.SessionID)
		}
		if e.AgentName != "test-agent" {
			t.Errorf("entry %d AgentName: want %q, got %q", i+1, "test-agent", e.AgentName)
		}
		if e.ToolName != "some_tool" {
			t.Errorf("entry %d ToolName: want %q, got %q", i+1, "some_tool", e.ToolName)
		}
		if e.RequestedAt == "" {
			t.Errorf("entry %d RequestedAt must not be empty", i+1)
		}
		if len(e.Arguments) == 0 {
			t.Errorf("entry %d Arguments must not be empty", i+1)
		}
	}
}
