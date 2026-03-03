package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newTestAdminProxy sets up a Proxy backed by an in-memory test DB and a
// minimal policy, and returns it alongside the store for direct DB access.
// The upstream server is a stub that always returns a JSON-RPC success body.
func newTestAdminProxy(t *testing.T) (*Proxy, *store.Store) {
	t.Helper()
	upstream := newTestUpstream(t, nil)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}
	st := store.NewTestDB(t)
	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", nil)
	return p, st
}

// insertPendingEscalation inserts a minimal pending escalation directly into the
// test DB and returns its UUID. This bypasses the proxy evaluation pipeline so
// admin endpoint tests do not require a full JWT + MCP round trip.
func insertPendingEscalation(t *testing.T, st *store.Store, id, sessionID, tool string) {
	t.Helper()
	esc := &store.Escalation{
		ID:        id,
		SessionID: sessionID,
		Seq:       1,
		ToolName:  tool,
		Status:    "pending",
	}
	if err := st.CreateEscalation(esc); err != nil {
		t.Fatalf("inserting test escalation: %v", err)
	}
}

// TestAdminListEscalations_NoFilter verifies that GET /admin/escalations with no
// status filter returns all escalations as a JSON array.
func TestAdminListEscalations_NoFilter(t *testing.T) {
	p, st := newTestAdminProxy(t)
	insertPendingEscalation(t, st, "esc-aaa", "sess-1", "some_tool")
	insertPendingEscalation(t, st, "esc-bbb", "sess-2", "other_tool")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/escalations", nil)
	p.AdminHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var result []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(result) < 2 {
		t.Errorf("want >= 2 escalations, got %d", len(result))
	}
}

// TestAdminListEscalations_StatusFilter verifies that ?status=pending returns
// only pending escalations and ?status=approved returns only approved ones.
func TestAdminListEscalations_StatusFilter(t *testing.T) {
	p, st := newTestAdminProxy(t)
	insertPendingEscalation(t, st, "esc-pending-1", "sess-1", "some_tool")
	insertPendingEscalation(t, st, "esc-pending-2", "sess-2", "some_tool")
	// Approve one so we can verify the filter excludes it from pending results.
	if err := st.ApproveEscalation("esc-pending-2", "ok"); err != nil {
		t.Fatalf("approving test escalation: %v", err)
	}

	t.Run("pending filter returns only pending", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/escalations?status=pending", nil)
		p.AdminHandler().ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
		var result []map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if len(result) != 1 {
			t.Errorf("want 1 pending escalation, got %d", len(result))
		}
		if result[0]["status"] != "pending" {
			t.Errorf("want status=pending, got %v", result[0]["status"])
		}
	})

	t.Run("approved filter returns only approved", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/escalations?status=approved", nil)
		p.AdminHandler().ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
		var result []map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if len(result) != 1 {
			t.Errorf("want 1 approved escalation, got %d", len(result))
		}
	})

	t.Run("invalid status returns 400", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/escalations?status=unknown", nil)
		p.AdminHandler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("want 400, got %d", w.Code)
		}
	})
}

// TestAdminApprove_TransitionsToApproved verifies that a POST to
// /admin/escalations/{id}/approve transitions the escalation status to "approved"
// and that the DB record reflects the change.
func TestAdminApprove_TransitionsToApproved(t *testing.T) {
	p, st := newTestAdminProxy(t)
	insertPendingEscalation(t, st, "esc-approve-1", "sess-approve", "write_file")

	body := bytes.NewBufferString(`{"note":"reviewed and approved"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/escalations/esc-approve-1/approve", body)
	r.Header.Set("Content-Type", "application/json")
	p.AdminHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		EscalationID string `json:"escalation_id"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.EscalationID != "esc-approve-1" {
		t.Errorf("want escalation_id=esc-approve-1, got %q", resp.EscalationID)
	}
	if resp.Status != "approved" {
		t.Errorf("want status=approved, got %q", resp.Status)
	}

	// Verify the database record was updated.
	status, err := st.GetEscalationStatus("esc-approve-1")
	if err != nil {
		t.Fatalf("reading escalation status from DB: %v", err)
	}
	if status != "approved" {
		t.Errorf("DB status: want approved, got %q", status)
	}
}

// TestAdminReject_TransitionsToRejected verifies that a POST to
// /admin/escalations/{id}/reject transitions the escalation status to "rejected"
// and that the DB record reflects the change.
func TestAdminReject_TransitionsToRejected(t *testing.T) {
	p, st := newTestAdminProxy(t)
	insertPendingEscalation(t, st, "esc-reject-1", "sess-reject", "send_email")

	body := bytes.NewBufferString(`{"reason":"not authorised for this recipient"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/escalations/esc-reject-1/reject", body)
	r.Header.Set("Content-Type", "application/json")
	p.AdminHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		EscalationID string `json:"escalation_id"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Status != "rejected" {
		t.Errorf("want status=rejected, got %q", resp.Status)
	}

	// Verify the database record was updated.
	status, err := st.GetEscalationStatus("esc-reject-1")
	if err != nil {
		t.Fatalf("reading escalation status from DB: %v", err)
	}
	if status != "rejected" {
		t.Errorf("DB status: want rejected, got %q", status)
	}
}

// TestAdminApprove_NotFound verifies that approving a non-existent escalation
// returns 404.
func TestAdminApprove_NotFound(t *testing.T) {
	p, _ := newTestAdminProxy(t)

	body := bytes.NewBufferString(`{"note":"ok"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/escalations/does-not-exist/approve", body)
	r.Header.Set("Content-Type", "application/json")
	p.AdminHandler().ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAdminApprove_AlreadyResolved verifies that approving an already-approved
// escalation returns 409 Conflict.
func TestAdminApprove_AlreadyResolved(t *testing.T) {
	p, st := newTestAdminProxy(t)
	insertPendingEscalation(t, st, "esc-already-done", "sess-x", "some_tool")
	if err := st.ApproveEscalation("esc-already-done", "first approval"); err != nil {
		t.Fatalf("pre-approving escalation: %v", err)
	}

	body := bytes.NewBufferString(`{"note":"second attempt"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/escalations/esc-already-done/approve", body)
	r.Header.Set("Content-Type", "application/json")
	p.AdminHandler().ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAdminApprove_EmptyBody verifies that an approve request with no JSON body
// (content-length 0) succeeds — the "note" field is optional.
func TestAdminApprove_EmptyBody(t *testing.T) {
	p, st := newTestAdminProxy(t)
	insertPendingEscalation(t, st, "esc-no-body", "sess-nb", "some_tool")

	w := httptest.NewRecorder()
	// Empty body simulates a curl POST with no --data flag.
	r := httptest.NewRequest(http.MethodPost, "/admin/escalations/esc-no-body/approve", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/json")
	p.AdminHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAdminApprove_ThenCheckEscalationStatus is the end-to-end integration for
// the admin approval flow: approve via the admin HTTP endpoint, then verify the
// agent's check_escalation_status virtual tool call returns "approved".
func TestAdminApprove_ThenCheckEscalationStatus(t *testing.T) {
	p, st := newTestAdminProxy(t)

	const escID = "esc-e2e-001"
	const sessionID = "sess-e2e-001"
	insertPendingEscalation(t, st, escID, sessionID, "write_file")

	// Step 1: approve via the admin HTTP endpoint.
	approveBody := bytes.NewBufferString(`{"note":"looks good"}`)
	aw := httptest.NewRecorder()
	ar := httptest.NewRequest(http.MethodPost, "/admin/escalations/"+escID+"/approve", approveBody)
	ar.Header.Set("Content-Type", "application/json")
	p.AdminHandler().ServeHTTP(aw, ar)
	if aw.Code != http.StatusOK {
		t.Fatalf("approve: want 200, got %d: %s", aw.Code, aw.Body.String())
	}

	// Step 2: register a test agent so the proxy accepts the JWT.
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	// Step 3: send a check_escalation_status virtual tool call through the proxy
	// handler. The proxy handles this before session lookup, so no session needs
	// to exist in the DB.
	checkBody := `{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"check_escalation_status","arguments":{"escalation_id":"` + escID + `"}}}`
	cw := httptest.NewRecorder()
	cr := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(checkBody))
	cr.Header.Set("Authorization", "Bearer "+token)
	cr.Header.Set("Content-Type", "application/json")
	cr.Header.Set("X-TrueBearing-Session-ID", sessionID)
	p.Handler().ServeHTTP(cw, cr)

	if cw.Code != http.StatusOK {
		t.Fatalf("check_escalation_status: want 200, got %d: %s", cw.Code, cw.Body.String())
	}

	// The response is a JSON-RPC success with a text content item whose payload
	// is a JSON string containing "status":"approved".
	var rpcResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(cw.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decoding JSON-RPC response: %v", err)
	}
	if len(rpcResp.Result.Content) == 0 {
		t.Fatal("expected at least one content item in result")
	}

	var statusPayload struct {
		EscalationID string `json:"escalation_id"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal([]byte(rpcResp.Result.Content[0].Text), &statusPayload); err != nil {
		t.Fatalf("decoding status payload: %v", err)
	}
	if statusPayload.Status != "approved" {
		t.Errorf("check_escalation_status: want status=approved, got %q", statusPayload.Status)
	}
	if statusPayload.EscalationID != escID {
		t.Errorf("check_escalation_status: want escalation_id=%q, got %q", escID, statusPayload.EscalationID)
	}
}
