package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// escalSess returns a minimal session for escalation evaluator tests.
func escalSess(id string) *session.Session {
	return &session.Session{ID: id, AgentName: "test-agent"}
}

// escalStore creates an isolated test store with a session pre-created.
func escalStore(t *testing.T, sessionID string) *store.Store {
	t.Helper()
	st := store.NewTestDB(t)
	if err := st.CreateSession(sessionID, "test-agent", "test-fp"); err != nil {
		t.Fatalf("creating test session %q: %v", sessionID, err)
	}
	return st
}

// escalPolicy builds a block-mode policy with the named tool configured with
// the given EscalateRule (may be nil for no escalation).
func escalPolicy(toolName string, rule *policy.EscalateRule) *policy.Policy {
	tp := policy.ToolPolicy{EscalateWhen: rule}
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{toolName},
		Tools:           map[string]policy.ToolPolicy{toolName: tp},
	}
}

// mustJSON returns a JSON encoding of the given map as json.RawMessage.
func mustJSON(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling test args: %v", err)
	}
	return b
}

// seedApprovedEscalation inserts a pre-approved escalation record into st
// so that EscalationEvaluator.Evaluate can detect it and return Allow.
func seedApprovedEscalation(t *testing.T, st *store.Store, sessionID, toolName string, argsJSON json.RawMessage) {
	t.Helper()
	e := &store.Escalation{
		ID:            "esc-test-" + sessionID,
		SessionID:     sessionID,
		Seq:           1,
		ToolName:      toolName,
		ArgumentsJSON: string(argsJSON),
		Status:        "approved",
	}
	if err := st.CreateEscalation(e); err != nil {
		t.Fatalf("seeding approved escalation: %v", err)
	}
}

func TestEscalationEvaluator(t *testing.T) {
	ctx := context.Background()
	const sid = "esc-test-session"

	cases := []struct {
		name               string
		toolName           string
		args               json.RawMessage
		rule               *policy.EscalateRule
		seedApproval       bool // whether to seed an approved escalation before evaluating
		wantAction         engine.Action
		wantRuleID         string
		wantReasonContains string
		wantError          bool // true if Evaluate should return a non-nil error
	}{
		{
			// No EscalateWhen rule on the tool policy → fast-path Allow.
			name:       "no escalation rule - allow",
			toolName:   "unrestricted-tool",
			args:       mustJSON(t, map[string]interface{}{"amount_usd": 500}),
			rule:       nil,
			wantAction: engine.Allow,
		},
		{
			// EscalateWhen rule defined but condition not satisfied → Allow.
			name:     "rule not triggered - value below threshold - allow",
			toolName: "high-value-tool",
			args:     mustJSON(t, map[string]interface{}{"amount_usd": 5000}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.amount_usd",
				Operator:     ">",
				Value:        10000,
			},
			wantAction: engine.Allow,
		},
		{
			// Value equals threshold: > operator is strictly greater, so no escalation.
			name:     "rule not triggered - value exactly at threshold for > - allow",
			toolName: "high-value-tool",
			args:     mustJSON(t, map[string]interface{}{"amount_usd": 10000}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.amount_usd",
				Operator:     ">",
				Value:        10000,
			},
			wantAction: engine.Allow,
		},
		{
			// Value one above threshold: > operator fires.
			name:     "rule triggered - value one above threshold - escalate",
			toolName: "high-value-tool",
			args:     mustJSON(t, map[string]interface{}{"amount_usd": 10001}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.amount_usd",
				Operator:     ">",
				Value:        10000,
			},
			wantAction:         engine.Escalate,
			wantRuleID:         "escalation",
			wantReasonContains: "escalate_when",
		},
		{
			// Rule triggered AND a prior approved escalation exists → Allow.
			name:     "rule triggered, prior approval exists - allow",
			toolName: "high-value-tool",
			args:     mustJSON(t, map[string]interface{}{"amount_usd": 15000}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.amount_usd",
				Operator:     ">",
				Value:        10000,
			},
			seedApproval: true,
			wantAction:   engine.Allow,
		},
		{
			// < operator: value below threshold triggers escalation.
			name:     "less-than operator - value below threshold - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"score": 30}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.score",
				Operator:     "<",
				Value:        50,
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// >= operator: value equal to threshold triggers escalation.
			name:     "gte operator - value at threshold - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"score": 100}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.score",
				Operator:     ">=",
				Value:        100,
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// <= operator: value below threshold triggers escalation.
			name:     "lte operator - value below threshold - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"priority": 1}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.priority",
				Operator:     "<=",
				Value:        5,
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// == operator: exact match triggers escalation.
			name:     "eq operator - exact match - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"category": 3}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.category",
				Operator:     "==",
				Value:        3,
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// != operator: value different from threshold triggers escalation.
			name:     "neq operator - value differs - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"status_code": 500}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.status_code",
				Operator:     "!=",
				Value:        200,
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// contains operator: substring present → escalate.
			name:     "contains operator - substring present - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"note": "urgent: approve now"}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.note",
				Operator:     "contains",
				Value:        "urgent",
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// contains operator: substring absent → Allow.
			name:     "contains operator - substring absent - allow",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"note": "routine transfer"}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.note",
				Operator:     "contains",
				Value:        "urgent",
			},
			wantAction: engine.Allow,
		},
		{
			// matches operator: regex matches → escalate.
			name:     "matches operator - regex matches - escalate",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"account": "ACC-12345"}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.account",
				Operator:     "matches",
				Value:        `^ACC-\d+$`,
			},
			wantAction: engine.Escalate,
			wantRuleID: "escalation",
		},
		{
			// matches operator: regex does not match → Allow.
			name:     "matches operator - regex does not match - allow",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"account": "OTHER-12345"}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.account",
				Operator:     "matches",
				Value:        `^ACC-\d+$`,
			},
			wantAction: engine.Allow,
		},
		{
			// Invalid JSONPath (path not found in arguments) → error → Deny via pipeline.
			name:     "argument path not found in arguments - error",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"other_field": 100}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.amount_usd",
				Operator:     ">",
				Value:        10000,
			},
			wantError: true,
		},
		{
			// Unsupported operator → error → Deny via pipeline.
			name:     "unsupported operator - error",
			toolName: "guarded-tool",
			args:     mustJSON(t, map[string]interface{}{"amount_usd": 15000}),
			rule: &policy.EscalateRule{
				ArgumentPath: "$.amount_usd",
				Operator:     "between", // not a valid operator
				Value:        10000,
			},
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := escalStore(t, sid)
			if tc.seedApproval {
				seedApprovedEscalation(t, st, sid, tc.toolName, tc.args)
			}

			pol := escalPolicy(tc.toolName, tc.rule)
			eval := &engine.EscalationEvaluator{Store: st}
			call := &engine.ToolCall{
				SessionID: sid,
				AgentName: "test-agent",
				ToolName:  tc.toolName,
				Arguments: tc.args,
			}

			got, err := eval.Evaluate(ctx, call, escalSess(sid), pol)

			if tc.wantError {
				if err == nil {
					t.Errorf("expected non-nil error; got nil (Action=%q Reason=%q)", got.Action, got.Reason)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q (Reason: %q)", got.Action, tc.wantAction, got.Reason)
			}
			if tc.wantRuleID != "" && got.RuleID != tc.wantRuleID {
				t.Errorf("RuleID = %q, want %q", got.RuleID, tc.wantRuleID)
			}
			if tc.wantReasonContains != "" && !containsStr(got.Reason, tc.wantReasonContains) {
				t.Errorf("Reason %q does not contain %q", got.Reason, tc.wantReasonContains)
			}
			if tc.wantAction == engine.Escalate && got.Reason == "" {
				t.Error("Escalate decision has empty Reason; operators need context in the audit record")
			}
		})
	}
}

// containsStr is a helper used by the table-driven test to check substring presence.
func containsStr(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestEscalationEvaluator_ToolNotInPolicyTools verifies that a tool call for a
// tool not present in the policy's Tools map (but in may_use) is allowed because
// there is no escalation rule to evaluate.
func TestEscalationEvaluator_ToolNotInPolicyTools(t *testing.T) {
	const sid = "esc-no-policy-tool"
	st := escalStore(t, sid)

	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"unlisted-tool"},
		Tools:           map[string]policy.ToolPolicy{},
	}
	eval := &engine.EscalationEvaluator{Store: st}
	call := &engine.ToolCall{
		SessionID: sid,
		ToolName:  "unlisted-tool",
		Arguments: mustJSON(t, map[string]interface{}{"amount_usd": 99999}),
	}
	got, err := eval.Evaluate(context.Background(), call, escalSess(sid), pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Action != engine.Allow {
		t.Errorf("Action = %q, want Allow", got.Action)
	}
}

// TestEscalationEvaluator_ApprovalHashIsolation verifies that an approved
// escalation for one argument payload does NOT unblock a call with different
// arguments — the hash must match exactly.
func TestEscalationEvaluator_ApprovalHashIsolation(t *testing.T) {
	const sid = "esc-hash-iso"
	st := escalStore(t, sid)

	approvedArgs := mustJSON(t, map[string]interface{}{"amount_usd": 15000})
	differentArgs := mustJSON(t, map[string]interface{}{"amount_usd": 50000})

	// Seed approval for 15000 args, but call will use 50000 args.
	seedApprovedEscalation(t, st, sid, "high-value-tool", approvedArgs)

	pol := escalPolicy("high-value-tool", &policy.EscalateRule{
		ArgumentPath: "$.amount_usd",
		Operator:     ">",
		Value:        10000,
	})
	eval := &engine.EscalationEvaluator{Store: st}
	call := &engine.ToolCall{
		SessionID: sid,
		ToolName:  "high-value-tool",
		Arguments: differentArgs, // different from the approved payload
	}
	got, err := eval.Evaluate(context.Background(), call, escalSess(sid), pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Action != engine.Escalate {
		t.Errorf("Action = %q, want Escalate — different args should not match prior approval", got.Action)
	}
}

// TestEscalationEvaluator_StoreError verifies that a store read failure is
// propagated as a non-nil error, which the pipeline converts to Deny (invariant 4).
func TestEscalationEvaluator_StoreError(t *testing.T) {
	const sid = "esc-store-err"
	st, err := store.Open("file:escalerr_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	if err := st.CreateSession(sid, "test-agent", "fp"); err != nil {
		_ = st.Close()
		t.Fatalf("creating session: %v", err)
	}
	// Close intentionally to force HasApprovedEscalation to fail on the next call.
	if err := st.Close(); err != nil {
		t.Fatalf("closing store: %v", err)
	}

	pol := escalPolicy("high-value-tool", &policy.EscalateRule{
		ArgumentPath: "$.amount_usd",
		Operator:     ">",
		Value:        10000,
	})
	eval := &engine.EscalationEvaluator{Store: st}
	call := &engine.ToolCall{
		SessionID: sid,
		ToolName:  "high-value-tool",
		Arguments: mustJSON(t, map[string]interface{}{"amount_usd": 15000}),
	}

	_, evalErr := eval.Evaluate(context.Background(), call, escalSess(sid), pol)
	if evalErr == nil {
		t.Error("expected error from closed store; got nil — evaluator must propagate store errors for pipeline to fail closed")
	}
}

// TestEscalationEvaluator_ShadowMode verifies that an escalation rule violation
// produces shadow_deny (not escalate) when routed through a pipeline whose
// effective enforcement mode is shadow. EscalationEvaluator always returns plain
// Escalate; shadow conversion is a pipeline responsibility per invariant 5.
func TestEscalationEvaluator_ShadowMode(t *testing.T) {
	const sid = "esc-shadow"
	st := escalStore(t, sid)

	pol := escalPolicy("high-value-tool", &policy.EscalateRule{
		ArgumentPath: "$.amount_usd",
		Operator:     ">",
		Value:        10000,
	})
	pol.EnforcementMode = policy.EnforcementShadow

	pip := engine.New(&engine.EscalationEvaluator{Store: st})
	call := &engine.ToolCall{
		SessionID: sid,
		AgentName: "test-agent",
		ToolName:  "high-value-tool",
		Arguments: mustJSON(t, map[string]interface{}{"amount_usd": 15000}),
	}
	got := pip.Evaluate(context.Background(), call, escalSess(sid), pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "escalation" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "escalation")
	}
}

// TestEscalationEvaluator_InvalidRegex verifies that a malformed regex pattern
// in the matches operator returns an error rather than panicking.
func TestEscalationEvaluator_InvalidRegex(t *testing.T) {
	const sid = "esc-bad-regex"
	st := escalStore(t, sid)

	pol := escalPolicy("guarded-tool", &policy.EscalateRule{
		ArgumentPath: "$.account",
		Operator:     "matches",
		Value:        `[invalid-regex`,
	})
	eval := &engine.EscalationEvaluator{Store: st}
	call := &engine.ToolCall{
		SessionID: sid,
		ToolName:  "guarded-tool",
		Arguments: mustJSON(t, map[string]interface{}{"account": "ACC-123"}),
	}
	_, err := eval.Evaluate(context.Background(), call, escalSess(sid), pol)
	if err == nil {
		t.Error("expected error for invalid regex pattern; got nil")
	}
}

// BenchmarkEscalationEvaluator measures evaluation cost on three representative paths:
//   - No rule (fast path — most tools have no escalate_when)
//   - Rule not triggered (threshold check, no DB query)
//   - Rule triggered with prior approval (DB query involved)
//
// Target: p99 < 2ms per CLAUDE.md §5.
func BenchmarkEscalationEvaluator(b *testing.B) {
	const sid = "bench-esc-session"
	st, err := store.Open("file:escalbench?mode=memory&cache=shared")
	if err != nil {
		b.Fatalf("opening bench store: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })

	if err := st.CreateSession(sid, "bench-agent", "bench-fp"); err != nil {
		b.Fatalf("creating bench session: %v", err)
	}

	args := json.RawMessage(`{"amount_usd":5000}`)
	rule := &policy.EscalateRule{
		ArgumentPath: "$.amount_usd",
		Operator:     ">",
		Value:        10000,
	}
	pol := escalPolicy("high-value-tool", rule)
	eval := &engine.EscalationEvaluator{Store: st}
	sess := escalSess(sid)
	call := &engine.ToolCall{
		SessionID: sid,
		AgentName: "bench-agent",
		ToolName:  "high-value-tool",
		Arguments: args,
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Hot path: rule not triggered (amount 5000 is below 10000 threshold).
		// This is the most common production case — no DB query is issued.
		d, err := eval.Evaluate(ctx, call, sess, pol)
		if err != nil {
			b.Fatal(err)
		}
		if d.Action != engine.Allow {
			b.Fatalf("expected allow, got %q: %s", d.Action, d.Reason)
		}
	}
}
