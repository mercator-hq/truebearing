package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// policyAYAML is a policy that allows some_tool (the tool used by toolsCallBody).
const policyAYAML = `
version: "1"
agent: reload-test-agent
enforcement_mode: block
may_use:
  - some_tool
`

// policyBYAML is a policy that does NOT allow some_tool. After hot-reload
// to policy B, a new session calling some_tool must be denied by MayUse.
const policyBYAML = `
version: "1"
agent: reload-test-agent
enforcement_mode: block
may_use:
  - other_tool
`

// policyBadLintYAML triggers lint rule L001 (empty may_use is an ERROR).
const policyBadLintYAML = `
version: "1"
agent: reload-test-agent
enforcement_mode: block
may_use: []
`

// writePolicyFile writes content to a temporary policy file and returns its path.
func writePolicyFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.policy.yaml")
	if err != nil {
		t.Fatalf("creating temp policy file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		t.Fatalf("writing temp policy file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing temp policy file: %v", err)
	}
	return f.Name()
}

// newReloadTestProxy creates a Proxy from the given policy file path and
// registers the default test agent. Returns the proxy, its httptest.Server,
// and a valid JWT for the test agent.
func newReloadTestProxy(t *testing.T, policyPath string) (*Proxy, *httptest.Server, string) {
	t.Helper()

	upstream := newTestUpstream(t, nil)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	pol, err := policy.ParseFile(policyPath)
	if err != nil {
		t.Fatalf("parsing policy from %s: %v", policyPath, err)
	}

	p := New(upstreamURL, st, pol, "", nil)
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return p, srv, token
}

// doToolCall sends a tools/call request to the given proxy server using the
// provided sessionID and Authorization token, and returns the HTTP response.
func doToolCall(t *testing.T, srv *httptest.Server, sessionID, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/v1",
		strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("building tool call request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TrueBearing-Session-ID", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("executing tool call request: %v", err)
	}
	return resp
}

// TestReloadPolicy_UpdatesHealthFingerprint verifies that after a successful
// hot-reload, GET /health returns the new policy fingerprint.
func TestReloadPolicy_UpdatesHealthFingerprint(t *testing.T) {
	path := writePolicyFile(t, policyAYAML)
	p, srv, _ := newReloadTestProxy(t, path)

	// Record the fingerprint before reload.
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	var before healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&before); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}

	// Overwrite the policy file with policy B and reload.
	if err := os.WriteFile(path, []byte(policyBYAML), 0o600); err != nil {
		t.Fatalf("overwriting policy file: %v", err)
	}
	if err := p.ReloadPolicy(); err != nil {
		t.Fatalf("ReloadPolicy: %v", err)
	}

	// /health must now report the new fingerprint.
	resp2, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health after reload: %v", err)
	}
	defer resp2.Body.Close()
	var after healthResponse
	if err := json.NewDecoder(resp2.Body).Decode(&after); err != nil {
		t.Fatalf("decoding health response after reload: %v", err)
	}

	if after.PolicyFingerprint == before.PolicyFingerprint {
		t.Errorf("fingerprint unchanged after reload: both are %q; policy B must produce a different fingerprint", after.PolicyFingerprint)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status after reload: want 200, got %d", resp2.StatusCode)
	}
}

// TestReloadPolicy_ExistingSessionUsesOldPolicy verifies that after a
// hot-reload, a session that was created under the previous policy continues
// to be evaluated against that policy version. The session must NOT receive a
// 409 Conflict, and MayUse must use the old policy's allowed tool list.
func TestReloadPolicy_ExistingSessionUsesOldPolicy(t *testing.T) {
	path := writePolicyFile(t, policyAYAML)
	p, srv, token := newReloadTestProxy(t, path)

	const oldSessionID = "sess-reload-old-111"
	const newSessionID = "sess-reload-new-222"

	// First call: creates session bound to policy A. some_tool is in policy A
	// may_use, so the call is allowed.
	resp := doToolCall(t, srv, oldSessionID, token)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first call (pre-reload): want 200, got %d", resp.StatusCode)
	}

	// Overwrite the policy file with policy B and reload. Policy B does NOT
	// list some_tool in may_use.
	if err := os.WriteFile(path, []byte(policyBYAML), 0o600); err != nil {
		t.Fatalf("overwriting policy file: %v", err)
	}
	if err := p.ReloadPolicy(); err != nil {
		t.Fatalf("ReloadPolicy: %v", err)
	}

	// Second call on the OLD session: must evaluate against policy A (which
	// has some_tool in may_use). A 409 Conflict would indicate the proxy
	// incorrectly compared the session fingerprint to the live policy.
	resp2 := doToolCall(t, srv, oldSessionID, token)
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusConflict {
		t.Errorf("old session received 409 Conflict after hot-reload; existing sessions must not be disrupted")
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("old session second call: want 200, got %d", resp2.StatusCode)
	}

	// Third call on a NEW session: must create the session under policy B.
	// Policy B does not include some_tool so the call must be denied (200 with
	// JSON-RPC error body containing a deny decision).
	resp3 := doToolCall(t, srv, newSessionID, token)
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("new session call: want 200, got %d", resp3.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp3.Body).Decode(&body); err != nil {
		t.Fatalf("decoding new session response: %v", err)
	}
	if _, hasError := body["error"]; !hasError {
		t.Errorf("new session call under policy B must be denied (tool not in may_use), but got no JSON-RPC error")
	}
}

// TestReloadPolicy_LintErrorRetainsPreviousPolicy verifies that a reload is
// rejected when the updated policy file triggers an ERROR-level lint rule.
// The proxy must continue serving the original policy fingerprint.
func TestReloadPolicy_LintErrorRetainsPreviousPolicy(t *testing.T) {
	path := writePolicyFile(t, policyAYAML)
	p, srv, _ := newReloadTestProxy(t, path)

	pol, _ := policy.ParseFile(path)
	originalFingerprint := pol.ShortFingerprint()

	// Overwrite with a policy that has an empty may_use (L001 ERROR).
	if err := os.WriteFile(path, []byte(policyBadLintYAML), 0o600); err != nil {
		t.Fatalf("overwriting policy file with lint-error policy: %v", err)
	}

	if err := p.ReloadPolicy(); err == nil {
		t.Error("ReloadPolicy must return an error when the new policy has lint ERRORs")
	}

	// /health must still report the original fingerprint.
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health after rejected reload: %v", err)
	}
	defer resp.Body.Close()
	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if body.PolicyFingerprint != originalFingerprint {
		t.Errorf("fingerprint changed despite lint-error reload: want %q, got %q",
			originalFingerprint, body.PolicyFingerprint)
	}
}

// TestReloadPolicy_ParseErrorRetainsPreviousPolicy verifies that a reload is
// rejected when the updated policy file is unparseable YAML. The proxy must
// continue serving the original policy fingerprint.
func TestReloadPolicy_ParseErrorRetainsPreviousPolicy(t *testing.T) {
	path := writePolicyFile(t, policyAYAML)
	p, srv, _ := newReloadTestProxy(t, path)

	pol, _ := policy.ParseFile(path)
	originalFingerprint := pol.ShortFingerprint()

	// Overwrite with malformed YAML.
	if err := os.WriteFile(path, []byte(":::not valid yaml:::"), 0o600); err != nil {
		t.Fatalf("overwriting policy file with bad YAML: %v", err)
	}

	if err := p.ReloadPolicy(); err == nil {
		t.Error("ReloadPolicy must return an error when the policy file is unparseable")
	}

	// /health must still report the original fingerprint.
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health after rejected reload: %v", err)
	}
	defer resp.Body.Close()
	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if body.PolicyFingerprint != originalFingerprint {
		t.Errorf("fingerprint changed despite parse-error reload: want %q, got %q",
			originalFingerprint, body.PolicyFingerprint)
	}
}

// TestReloadPolicy_NoSourcePath verifies that ReloadPolicy returns an error
// when the policy was not loaded from a file (SourcePath is empty), which
// is the case when policies are constructed in-memory (e.g. in tests).
func TestReloadPolicy_NoSourcePath(t *testing.T) {
	pol, err := policy.ParseBytes([]byte(policyAYAML), "")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	upstream, _ := url.Parse("http://localhost:9999")
	st := store.NewTestDB(t)
	p := New(upstream, st, pol, "", nil)

	if err := p.ReloadPolicy(); err == nil {
		t.Error("ReloadPolicy must return an error when SourcePath is empty")
	}
}
