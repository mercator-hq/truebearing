package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// seqSess returns a minimal session for sequence evaluator tests.
func seqSess(id string) *session.Session {
	return &session.Session{
		ID:        id,
		AgentName: "test-agent",
	}
}

// seqStore creates an isolated test store with the given session already created,
// satisfying the FK constraint on session_events.
func seqStore(t *testing.T, sessionID string) *store.Store {
	t.Helper()
	st := store.NewTestDB(t)
	if err := st.CreateSession(sessionID, "test-agent", "test-fp"); err != nil {
		t.Fatalf("creating test session %q: %v", sessionID, err)
	}
	return st
}

// seedEvent inserts a single event into st with the given tool name and decision.
func seedEvent(t *testing.T, st *store.Store, sessionID, toolName, decision string) {
	t.Helper()
	ev := &store.SessionEvent{
		SessionID: sessionID,
		ToolName:  toolName,
		Decision:  decision,
	}
	if err := st.AppendEvent(ev); err != nil {
		t.Fatalf("seeding event (tool=%q decision=%q): %v", toolName, decision, err)
	}
}

// seqPolicy builds a block-mode policy with a single guarded tool configured
// with the given sequence predicates. All referenced tools are added to may_use.
func seqPolicy(toolName string, onlyAfter, neverAfter []string, priorN *policy.PriorNRule) *policy.Policy {
	candidates := append([]string{toolName}, onlyAfter...)
	candidates = append(candidates, neverAfter...)
	if priorN != nil {
		candidates = append(candidates, priorN.Tool)
	}
	seen := make(map[string]bool)
	mayUse := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			mayUse = append(mayUse, c)
		}
	}
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          mayUse,
		Tools: map[string]policy.ToolPolicy{
			toolName: {
				Sequence: policy.SequencePolicy{
					OnlyAfter:      onlyAfter,
					NeverAfter:     neverAfter,
					RequiresPriorN: priorN,
				},
			},
		},
	}
}

func TestSequenceEvaluator(t *testing.T) {
	ctx := context.Background()
	const sid = "test-session"

	type histEntry struct {
		tool     string
		decision string
	}

	cases := []struct {
		name               string
		history            []histEntry
		toolName           string
		pol                *policy.Policy
		wantAction         engine.Action
		wantRuleID         string
		wantReasonContains []string
	}{
		{
			name:     "no sequence rules - allow",
			toolName: "unrestricted-tool",
			pol: &policy.Policy{
				EnforcementMode: policy.EnforcementBlock,
				MayUse:          []string{"unrestricted-tool"},
				Tools: map[string]policy.ToolPolicy{
					"unrestricted-tool": {},
				},
			},
			wantAction: engine.Allow,
		},
		{
			name:     "tool not in Tools map - allow",
			toolName: "unlisted-tool",
			pol: &policy.Policy{
				EnforcementMode: policy.EnforcementBlock,
				MayUse:          []string{"unlisted-tool"},
				Tools:           map[string]policy.ToolPolicy{},
			},
			wantAction: engine.Allow,
		},
		{
			name: "only_after satisfied - allow",
			history: []histEntry{
				{tool: "prereq-tool", decision: "allow"},
			},
			toolName:   "target-tool",
			pol:        seqPolicy("target-tool", []string{"prereq-tool"}, nil, nil),
			wantAction: engine.Allow,
		},
		{
			name:       "only_after not satisfied - empty history - deny",
			toolName:   "target-tool",
			pol:        seqPolicy("target-tool", []string{"prereq-tool"}, nil, nil),
			wantAction: engine.Deny,
			wantRuleID: "sequence",
			wantReasonContains: []string{
				"sequence.only_after",
				"prereq-tool",
			},
		},
		{
			name: "only_after multiple tools all satisfied - allow",
			history: []histEntry{
				{tool: "step-a", decision: "allow"},
				{tool: "step-b", decision: "allow"},
			},
			toolName:   "final-tool",
			pol:        seqPolicy("final-tool", []string{"step-a", "step-b"}, nil, nil),
			wantAction: engine.Allow,
		},
		{
			name: "only_after multiple tools one missing - deny",
			history: []histEntry{
				{tool: "step-a", decision: "allow"},
				// step-b absent
			},
			toolName:           "final-tool",
			pol:                seqPolicy("final-tool", []string{"step-a", "step-b"}, nil, nil),
			wantAction:         engine.Deny,
			wantRuleID:         "sequence",
			wantReasonContains: []string{"step-b"},
		},
		{
			name:     "never_after not violated - allow",
			toolName: "target-tool",
			pol:      seqPolicy("target-tool", nil, []string{"dangerous-tool"}, nil),
			// history is empty; dangerous-tool was never called
			wantAction: engine.Allow,
		},
		{
			name: "never_after violated - deny",
			history: []histEntry{
				{tool: "dangerous-tool", decision: "allow"},
			},
			toolName:   "target-tool",
			pol:        seqPolicy("target-tool", nil, []string{"dangerous-tool"}, nil),
			wantAction: engine.Deny,
			wantRuleID: "sequence",
			wantReasonContains: []string{
				"sequence.never_after",
				"dangerous-tool",
			},
		},
		{
			name: "requires_prior_n satisfied - allow",
			history: []histEntry{
				{tool: "verify-tool", decision: "allow"},
				{tool: "verify-tool", decision: "allow"},
				{tool: "verify-tool", decision: "allow"},
			},
			toolName:   "submit-tool",
			pol:        seqPolicy("submit-tool", nil, nil, &policy.PriorNRule{Tool: "verify-tool", Count: 3}),
			wantAction: engine.Allow,
		},
		{
			name: "requires_prior_n at exact N - allow",
			history: []histEntry{
				{tool: "verify-tool", decision: "allow"},
				{tool: "verify-tool", decision: "allow"},
			},
			toolName:   "submit-tool",
			pol:        seqPolicy("submit-tool", nil, nil, &policy.PriorNRule{Tool: "verify-tool", Count: 2}),
			wantAction: engine.Allow,
		},
		{
			name: "requires_prior_n at N-1 - deny",
			history: []histEntry{
				{tool: "verify-tool", decision: "allow"},
				// need 2, only 1 present
			},
			toolName:   "submit-tool",
			pol:        seqPolicy("submit-tool", nil, nil, &policy.PriorNRule{Tool: "verify-tool", Count: 2}),
			wantAction: engine.Deny,
			wantRuleID: "sequence",
			wantReasonContains: []string{
				"sequence.requires_prior_n",
				"verify-tool",
				"2",
			},
		},
		{
			name:       "requires_prior_n at 0 occurrences - deny",
			toolName:   "submit-tool",
			pol:        seqPolicy("submit-tool", nil, nil, &policy.PriorNRule{Tool: "verify-tool", Count: 1}),
			wantAction: engine.Deny,
			wantRuleID: "sequence",
			wantReasonContains: []string{
				"sequence.requires_prior_n",
				"verify-tool",
			},
		},
		{
			name: "all three predicates violated - deny with all three violations",
			history: []histEntry{
				{tool: "forbidden-tool", decision: "allow"}, // triggers never_after
				// prereq-tool absent (triggers only_after)
				// verify-tool count is 0 (triggers requires_prior_n)
			},
			toolName: "target-tool",
			pol: &policy.Policy{
				EnforcementMode: policy.EnforcementBlock,
				MayUse:          []string{"target-tool", "prereq-tool", "forbidden-tool", "verify-tool"},
				Tools: map[string]policy.ToolPolicy{
					"target-tool": {
						Sequence: policy.SequencePolicy{
							OnlyAfter:      []string{"prereq-tool"},
							NeverAfter:     []string{"forbidden-tool"},
							RequiresPriorN: &policy.PriorNRule{Tool: "verify-tool", Count: 1},
						},
					},
				},
			},
			wantAction: engine.Deny,
			wantRuleID: "sequence",
			wantReasonContains: []string{
				"sequence.only_after",
				"sequence.never_after",
				"sequence.requires_prior_n",
			},
		},
		{
			name: "denied events not counted for only_after - deny",
			history: []histEntry{
				// prereq-tool appears only as a denied call and must not
				// satisfy the only_after predicate.
				{tool: "prereq-tool", decision: "deny"},
			},
			toolName:           "target-tool",
			pol:                seqPolicy("target-tool", []string{"prereq-tool"}, nil, nil),
			wantAction:         engine.Deny,
			wantRuleID:         "sequence",
			wantReasonContains: []string{"sequence.only_after", "prereq-tool"},
		},
		{
			name: "denied events not counted for never_after - allow",
			history: []histEntry{
				// dangerous-tool appears only as a denied call; it was never
				// executed upstream and must not trigger the never_after block.
				{tool: "dangerous-tool", decision: "deny"},
			},
			toolName:   "target-tool",
			pol:        seqPolicy("target-tool", nil, []string{"dangerous-tool"}, nil),
			wantAction: engine.Allow,
		},
		{
			name: "shadow_deny events count for only_after - allow",
			history: []histEntry{
				// prereq-tool was shadow-denied but forwarded upstream; it must
				// count toward sequence history.
				{tool: "prereq-tool", decision: "shadow_deny"},
			},
			toolName:   "target-tool",
			pol:        seqPolicy("target-tool", []string{"prereq-tool"}, nil, nil),
			wantAction: engine.Allow,
		},
		{
			name: "shadow_deny events count for never_after - deny",
			history: []histEntry{
				// dangerous-tool was shadow-denied but forwarded upstream; it
				// counts as executed and must trigger the never_after block.
				{tool: "dangerous-tool", decision: "shadow_deny"},
			},
			toolName:   "target-tool",
			pol:        seqPolicy("target-tool", nil, []string{"dangerous-tool"}, nil),
			wantAction: engine.Deny,
			wantRuleID: "sequence",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := seqStore(t, sid)
			for _, h := range tc.history {
				seedEvent(t, st, sid, h.tool, h.decision)
			}

			eval := &engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}}
			call := &engine.ToolCall{
				SessionID: sid,
				AgentName: "test-agent",
				ToolName:  tc.toolName,
			}
			got, err := eval.Evaluate(ctx, call, seqSess(sid), tc.pol)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q (Reason: %q)", got.Action, tc.wantAction, got.Reason)
			}
			if tc.wantRuleID != "" && got.RuleID != tc.wantRuleID {
				t.Errorf("RuleID = %q, want %q", got.RuleID, tc.wantRuleID)
			}
			for _, sub := range tc.wantReasonContains {
				if !strings.Contains(got.Reason, sub) {
					t.Errorf("Reason %q does not contain %q", got.Reason, sub)
				}
			}
			if tc.wantAction == engine.Deny && got.Reason == "" {
				t.Error("Deny decision has empty Reason; operators need context in the audit record")
			}
		})
	}
}

// TestSequenceEvaluator_AllViolationsReported verifies that all predicate
// violations are collected into a single denial, not just the first one found.
// This is the key design property of the sequence evaluator: operators must see
// the full picture in one request.
func TestSequenceEvaluator_AllViolationsReported(t *testing.T) {
	const sid = "sess-all-violations"
	st := seqStore(t, sid)
	seedEvent(t, st, sid, "forbidden-a", "allow")
	seedEvent(t, st, sid, "forbidden-b", "allow")
	// prereq-a and prereq-b are absent from history

	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"target", "prereq-a", "prereq-b", "forbidden-a", "forbidden-b"},
		Tools: map[string]policy.ToolPolicy{
			"target": {
				Sequence: policy.SequencePolicy{
					OnlyAfter:  []string{"prereq-a", "prereq-b"},
					NeverAfter: []string{"forbidden-a", "forbidden-b"},
				},
			},
		},
	}

	eval := &engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}}
	call := &engine.ToolCall{SessionID: sid, ToolName: "target"}
	got, err := eval.Evaluate(context.Background(), call, seqSess(sid), pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Action != engine.Deny {
		t.Fatalf("Action = %q, want Deny", got.Action)
	}
	// All four violations (2× only_after + 2× never_after) must appear in the
	// reason so operators see the full picture from a single audit record.
	for _, sub := range []string{"prereq-a", "prereq-b", "forbidden-a", "forbidden-b"} {
		if !strings.Contains(got.Reason, sub) {
			t.Errorf("Reason %q does not mention %q", got.Reason, sub)
		}
	}
}

// TestSequenceEvaluator_StoreError verifies that a store read failure is
// propagated as a non-nil error from Evaluate. The pipeline converts this to a
// Deny decision per invariant 4 — fail closed on any evaluation error.
func TestSequenceEvaluator_StoreError(t *testing.T) {
	const sid = "sess-store-err"
	// Open directly (not via NewTestDB) to avoid a double-close from t.Cleanup.
	st, err := store.Open("file:seqerr_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	if err := st.CreateSession(sid, "test-agent", "fp"); err != nil {
		_ = st.Close()
		t.Fatalf("creating session: %v", err)
	}
	// Close intentionally to force GetSessionEvents to return an error on the
	// next call. The evaluator must propagate this error rather than allowing.
	if err := st.Close(); err != nil {
		t.Fatalf("closing store: %v", err)
	}

	pol := seqPolicy("target-tool", []string{"prereq-tool"}, nil, nil)
	eval := &engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}}
	call := &engine.ToolCall{SessionID: sid, ToolName: "target-tool"}

	_, evalErr := eval.Evaluate(context.Background(), call, seqSess(sid), pol)
	if evalErr == nil {
		t.Error("expected error from closed store; got nil — evaluator must propagate store errors for pipeline to fail closed")
	}
}

// TestSequenceEvaluator_ShadowMode verifies that a sequence violation produces
// shadow_deny (not deny) when routed through a pipeline whose effective
// enforcement mode is shadow. SequenceEvaluator always returns plain Deny;
// shadow conversion is a pipeline responsibility per invariant 5.
func TestSequenceEvaluator_ShadowMode(t *testing.T) {
	const sid = "sess-shadow-seq"
	st := seqStore(t, sid)
	// No history: only_after will fail.
	pol := seqPolicy("target-tool", []string{"prereq-tool"}, nil, nil)
	pol.EnforcementMode = policy.EnforcementShadow

	pip := engine.New(&engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}})
	call := &engine.ToolCall{SessionID: sid, AgentName: "test-agent", ToolName: "target-tool"}
	got := pip.Evaluate(context.Background(), call, seqSess(sid), pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "sequence" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "sequence")
	}
}

// BenchmarkSequenceEvaluator measures the evaluation cost against a session
// with 1000 historical events — the worst-case history depth operators are
// likely to encounter in production. Target: p99 < 2ms per CLAUDE.md §5.
func BenchmarkSequenceEvaluator(b *testing.B) {
	const sid = "bench-session"
	st, err := store.Open("file:seqbench?mode=memory&cache=shared")
	if err != nil {
		b.Fatalf("opening bench store: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })

	if err := st.CreateSession(sid, "bench-agent", "bench-fp"); err != nil {
		b.Fatalf("creating bench session: %v", err)
	}

	// Seed 1000 allowed events: 500 occurrences of "prereq-tool" and 500 of
	// "other-tool". All predicates will be satisfied, representing the allow
	// hot path that dominates in production.
	for i := 0; i < 1000; i++ {
		toolName := "other-tool"
		if i < 500 {
			toolName = "prereq-tool"
		}
		ev := &store.SessionEvent{
			SessionID: sid,
			ToolName:  toolName,
			Decision:  "allow",
		}
		if err := st.AppendEvent(ev); err != nil {
			b.Fatalf("seeding bench event %d: %v", i, err)
		}
	}

	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"target-tool", "prereq-tool", "other-tool"},
		Tools: map[string]policy.ToolPolicy{
			"target-tool": {
				Sequence: policy.SequencePolicy{
					OnlyAfter:  []string{"prereq-tool"},
					NeverAfter: []string{"absent-tool"}, // absent from history; never_after passes
					RequiresPriorN: &policy.PriorNRule{
						Tool:  "prereq-tool",
						Count: 5,
					},
				},
			},
		},
	}

	eval := &engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}}
	sess := &session.Session{ID: sid, AgentName: "bench-agent"}
	call := &engine.ToolCall{SessionID: sid, AgentName: "bench-agent", ToolName: "target-tool"}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := eval.Evaluate(ctx, call, sess, pol)
		if err != nil {
			b.Fatal(err)
		}
		if d.Action != engine.Allow {
			b.Fatalf("expected allow, got %q: %s", d.Action, d.Reason)
		}
	}
}
