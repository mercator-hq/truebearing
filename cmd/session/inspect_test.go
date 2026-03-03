package session

import (
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/store"
)

// TestWriteMermaidOutput verifies that a known sequence of session events
// produces deterministic, correctly-structured Mermaid sequenceDiagram output.
// Each test case represents a distinct event pattern that operators would see
// in production: happy-path allow chains, taint-then-block, escalation flows,
// and shadow-deny observations.
func TestWriteMermaidOutput(t *testing.T) {
	cases := []struct {
		name        string
		agentName   string
		events      []store.SessionEvent
		escalations []store.Escalation
		wantLines   []string
	}{
		{
			name:      "empty session produces header only",
			agentName: "payments-agent",
			events:    []store.SessionEvent{},
			wantLines: []string{"sequenceDiagram"},
		},
		{
			name:      "single allowed event",
			agentName: "payments-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "read_account", Decision: "allow"},
			},
			wantLines: []string{
				"sequenceDiagram",
				"    payments-agent->>Proxy: read_account (ALLOWED)",
			},
		},
		{
			name:      "denied event shows reason code from PolicyRule",
			agentName: "payments-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "execute_wire", Decision: "deny", PolicyRule: "sequence.only_after"},
			},
			wantLines: []string{
				"    payments-agent->>Proxy: execute_wire (DENIED)",
				"    Note over Proxy: reason: sequence.only_after",
			},
		},
		{
			name:      "denied event with empty PolicyRule uses fallback reason code",
			agentName: "payments-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "execute_wire", Decision: "deny", PolicyRule: ""},
			},
			wantLines: []string{
				"    Note over Proxy: reason: policy_violation",
			},
		},
		{
			name:      "taint-apply then taint-block annotates causing event",
			agentName: "payments-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "read_external_email", Decision: "allow"},
				{Seq: 2, ToolName: "execute_wire_transfer", Decision: "deny", PolicyRule: "taint.session_tainted"},
			},
			wantLines: []string{
				"    payments-agent->>Proxy: read_external_email (ALLOWED)",
				"    Note over Proxy: session tainted",
				"    payments-agent->>Proxy: execute_wire_transfer (DENIED)",
				"    Note over Proxy: reason: taint.session_tainted",
			},
		},
		{
			name:      "escalated event shows PENDING when no escalation record exists",
			agentName: "payments-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "approve_claim", Decision: "escalate"},
			},
			escalations: []store.Escalation{},
			wantLines: []string{
				"    payments-agent->>Proxy: approve_claim (ESCALATED)",
				"    Note over Proxy: ESCALATED → PENDING",
			},
		},
		{
			name:      "escalated event shows APPROVED when escalation record is approved",
			agentName: "claims-agent",
			events: []store.SessionEvent{
				{Seq: 3, ToolName: "approve_claim", Decision: "escalate"},
			},
			escalations: []store.Escalation{
				{ID: "esc-001", SessionID: "sess-001", Seq: 3, ToolName: "approve_claim", Status: "approved"},
			},
			wantLines: []string{
				"    claims-agent->>Proxy: approve_claim (ESCALATED)",
				"    Note over Proxy: ESCALATED → APPROVED",
			},
		},
		{
			name:      "escalated event shows REJECTED when escalation is rejected",
			agentName: "claims-agent",
			events: []store.SessionEvent{
				{Seq: 5, ToolName: "submit_to_fda", Decision: "escalate"},
			},
			escalations: []store.Escalation{
				{ID: "esc-002", SessionID: "sess-002", Seq: 5, ToolName: "submit_to_fda", Status: "rejected"},
			},
			wantLines: []string{
				"    Note over Proxy: ESCALATED → REJECTED",
			},
		},
		{
			name:      "shadow_deny shows SHADOW DENIED label and reason annotation",
			agentName: "payments-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "execute_wire", Decision: "shadow_deny", PolicyRule: "budget.calls_exceeded"},
			},
			wantLines: []string{
				"    payments-agent->>Proxy: execute_wire (SHADOW DENIED)",
				"    Note over Proxy: reason: budget.calls_exceeded",
			},
		},
		{
			name:      "agent name with spaces is sanitised to underscores",
			agentName: "my billing agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "read_invoice", Decision: "allow"},
			},
			wantLines: []string{
				"    my_billing_agent->>Proxy: read_invoice (ALLOWED)",
			},
		},
		{
			name:      "empty agent name falls back to Agent",
			agentName: "",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "ping", Decision: "allow"},
			},
			wantLines: []string{
				"    Agent->>Proxy: ping (ALLOWED)",
			},
		},
		{
			name:      "mixed session: allow, taint-block, escalate approved",
			agentName: "legal-agent",
			events: []store.SessionEvent{
				{Seq: 1, ToolName: "read_privileged_document", Decision: "allow"},
				{Seq: 2, ToolName: "transmit_to_external", Decision: "deny", PolicyRule: "taint.session_tainted"},
				{Seq: 3, ToolName: "file_with_court", Decision: "escalate"},
			},
			escalations: []store.Escalation{
				{Seq: 3, ToolName: "file_with_court", Status: "approved"},
			},
			wantLines: []string{
				"sequenceDiagram",
				"    legal-agent->>Proxy: read_privileged_document (ALLOWED)",
				"    Note over Proxy: session tainted",
				"    legal-agent->>Proxy: transmit_to_external (DENIED)",
				"    Note over Proxy: reason: taint.session_tainted",
				"    legal-agent->>Proxy: file_with_court (ESCALATED)",
				"    Note over Proxy: ESCALATED → APPROVED",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			if err := writeMermaidOutput(tc.agentName, tc.events, tc.escalations, &sb); err != nil {
				t.Fatalf("writeMermaidOutput: %v", err)
			}
			output := sb.String()
			for _, want := range tc.wantLines {
				if !strings.Contains(output, want) {
					t.Errorf("output missing expected line:\n  want: %q\n  got:\n%s", want, output)
				}
			}
		})
	}
}

// TestDetectTaintCausingEvents verifies the taint-inference heuristic independently
// of the full rendering path.
func TestDetectTaintCausingEvents(t *testing.T) {
	cases := []struct {
		name       string
		events     []store.SessionEvent
		wantSeqs   []uint64
		noWantSeqs []uint64
	}{
		{
			name: "no taint events: empty result",
			events: []store.SessionEvent{
				{Seq: 1, Decision: "allow"},
				{Seq: 2, Decision: "allow"},
			},
			wantSeqs: []uint64{},
		},
		{
			name: "single taint cycle: last allowed before first taint-block",
			events: []store.SessionEvent{
				{Seq: 1, Decision: "allow"},
				{Seq: 2, Decision: "allow"},
				{Seq: 3, Decision: "deny", PolicyRule: "taint.session_tainted"},
			},
			wantSeqs: []uint64{2},
		},
		{
			name: "no preceding allowed event: nothing marked",
			events: []store.SessionEvent{
				{Seq: 1, Decision: "deny", PolicyRule: "taint.session_tainted"},
			},
			noWantSeqs: []uint64{1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := detectTaintCausingEvents(tc.events)
			for _, seq := range tc.wantSeqs {
				if !result[seq] {
					t.Errorf("seq %d should be marked as taint-causing, but is not; result=%v", seq, result)
				}
			}
			for _, seq := range tc.noWantSeqs {
				if result[seq] {
					t.Errorf("seq %d should NOT be marked as taint-causing, but is; result=%v", seq, result)
				}
			}
		})
	}
}
