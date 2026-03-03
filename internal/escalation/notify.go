package escalation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// NotifyConfig holds the configuration for escalation notification delivery.
// If WebhookURL is empty, notifications are written to stdout as structured JSON.
type NotifyConfig struct {
	// WebhookURL is the HTTP endpoint to POST escalation-created events to.
	// If empty, the notification is written to stdout instead.
	WebhookURL string

	// AdminPort is the port on which the TrueBearing admin HTTP server is
	// listening (default 7774). When non-zero, the notification payload
	// includes approve_url and reject_url fields so webhook recipients can
	// approve or reject the escalation with a single curl command rather than
	// requiring CLI access to the proxy machine.
	AdminPort int
}

// notifyPayload is the JSON body sent to the webhook URL or written to stdout
// when an escalation is created.
type notifyPayload struct {
	Event        string `json:"event"`
	EscalationID string `json:"escalation_id"`
	SessionID    string `json:"session_id"`
	Tool         string `json:"tool"`
	Reason       string `json:"reason"`
	ApproveCmd   string `json:"approve_cmd"`
	RejectCmd    string `json:"reject_cmd"`
	// ApproveURL and RejectURL are populated when the admin HTTP server is
	// running (NotifyConfig.AdminPort != 0). They let webhook recipients
	// approve or reject with a single curl POST rather than requiring CLI
	// access to the proxy machine.
	ApproveURL string `json:"approve_url,omitempty"`
	RejectURL  string `json:"reject_url,omitempty"`
}

// Notify fires an escalation-created notification for the given escalation record.
//
// If cfg is non-nil and cfg.WebhookURL is set, the payload is POSTed to that URL
// using a 5-second timeout. A delivery failure is logged but does not block the
// caller — the escalation record is already persisted in the database before
// Notify is called.
//
// If cfg is nil or cfg.WebhookURL is empty, the payload is written to stdout as
// a single JSON line so local-dev operators can observe escalations in the proxy
// log without configuring a webhook.
//
// Design: Notify is always called after the escalation record has been persisted.
// This means a failed notification never rolls back the escalation. Operators can
// always discover pending escalations via "truebearing escalation list" regardless
// of notification delivery status.
func Notify(esc *store.Escalation, reason string, cfg *NotifyConfig) {
	p := notifyPayload{
		Event:        "escalation.created",
		EscalationID: esc.ID,
		SessionID:    esc.SessionID,
		Tool:         esc.ToolName,
		Reason:       reason,
		ApproveCmd:   fmt.Sprintf("truebearing escalation approve %s", esc.ID),
		RejectCmd:    fmt.Sprintf("truebearing escalation reject %s --reason \"...\"", esc.ID),
	}
	// Populate HTTP approval URLs when the admin server is running so
	// webhook recipients can act without CLI access to the proxy machine.
	if cfg != nil && cfg.AdminPort != 0 {
		p.ApproveURL = fmt.Sprintf("http://localhost:%d/admin/escalations/%s/approve", cfg.AdminPort, esc.ID)
		p.RejectURL = fmt.Sprintf("http://localhost:%d/admin/escalations/%s/reject", cfg.AdminPort, esc.ID)
	}
	body, err := json.Marshal(p)
	if err != nil {
		// JSON marshalling of a struct with only string fields cannot fail in
		// practice; log defensively and return.
		log.Printf("escalation: marshalling notification payload for %q: %v", esc.ID, err)
		return
	}

	if cfg != nil && cfg.WebhookURL != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(cfg.WebhookURL, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("escalation: webhook POST to %q for escalation %q failed: %v", cfg.WebhookURL, esc.ID, err)
			return
		}
		// Drain and close the response body to allow connection reuse. We do
		// not inspect the status code — notification delivery is best-effort.
		_ = resp.Body.Close()
		return
	}

	// Stdout fallback: write a single JSON line to the proxy's stdout so operators
	// running the proxy in a terminal can see escalations without a webhook.
	fmt.Printf("%s\n", body)
}
