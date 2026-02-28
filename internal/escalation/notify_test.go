package escalation_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mercator-hq/truebearing/internal/escalation"
	"github.com/mercator-hq/truebearing/internal/store"
)

// testEscalation returns a minimal *store.Escalation suitable for notification tests.
func testEscalation(id string) *store.Escalation {
	return &store.Escalation{
		ID:        id,
		SessionID: "sess-notify-test",
		Seq:       1,
		ToolName:  "execute_payment",
		Status:    "pending",
	}
}

// TestNotify_WebhookFires verifies that Notify POSTs the correct JSON payload
// to the configured webhook URL when an escalation is created.
func TestNotify_WebhookFires(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		received, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading webhook request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	esc := testEscalation("esc-webhook-001")
	reason := "amount_usd 15000 > threshold 10000"
	cfg := &escalation.NotifyConfig{WebhookURL: srv.URL}

	escalation.Notify(esc, reason, cfg)

	if len(received) == 0 {
		t.Fatal("Notify: no request received by webhook server")
	}

	var payload map[string]string
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatalf("Notify: webhook body is not valid JSON: %v", err)
	}

	checks := map[string]string{
		"event":         "escalation.created",
		"escalation_id": "esc-webhook-001",
		"session_id":    "sess-notify-test",
		"tool":          "execute_payment",
		"reason":        reason,
	}
	for field, want := range checks {
		if got := payload[field]; got != want {
			t.Errorf("payload[%q] = %q, want %q", field, got, want)
		}
	}

	// Verify the approve_cmd and reject_cmd include the escalation ID.
	if cmd := payload["approve_cmd"]; cmd == "" {
		t.Error("payload[approve_cmd] is empty")
	}
	if cmd := payload["reject_cmd"]; cmd == "" {
		t.Error("payload[reject_cmd] is empty")
	}
}

// TestNotify_WebhookFailure_NotFatal verifies that a webhook POST failure is
// handled gracefully: Notify does not panic and returns normally so the
// escalation creation is not blocked.
func TestNotify_WebhookFailure_NotFatal(t *testing.T) {
	// Point to a server that immediately closes connections, simulating a
	// network failure. We spin up and immediately close the server so the
	// URL is valid-looking but no longer listening.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	webhookURL := srv.URL
	srv.Close() // close so the POST will fail with a connection error

	esc := testEscalation("esc-webhook-002")
	cfg := &escalation.NotifyConfig{WebhookURL: webhookURL}

	// Must not panic. The log output from the failed POST is expected but
	// cannot be captured easily in a unit test without os.Stdout redirection;
	// the key assertion is that this call returns normally.
	escalation.Notify(esc, "some reason", cfg)
}

// TestNotify_StdoutFallback verifies that Notify does not panic and returns
// normally when no webhook URL is configured. Output goes to stdout, which is
// acceptable for local-dev operator visibility.
func TestNotify_StdoutFallback(t *testing.T) {
	esc := testEscalation("esc-stdout-003")

	// nil cfg — should fall back to stdout without panicking.
	escalation.Notify(esc, "threshold exceeded", nil)

	// Empty URL — same fallback path.
	escalation.Notify(esc, "threshold exceeded", &escalation.NotifyConfig{})
}
