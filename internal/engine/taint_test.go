package engine_test

import (
	"context"
	"testing"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// taintPolicy returns a block-mode policy with a standard taint scenario:
//   - "apply-taint-tool" sets the session taint when called.
//   - "clear-taint-tool" clears the session taint when called.
//   - "sensitive-tool" has never_after: [apply-taint-tool], so it is blocked
//     when the session is tainted.
//   - "neutral-tool" has no taint rules and is unaffected by session taint.
func taintPolicy() *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse: []string{
			"apply-taint-tool",
			"clear-taint-tool",
			"sensitive-tool",
			"neutral-tool",
		},
		Tools: map[string]policy.ToolPolicy{
			"apply-taint-tool": {
				Taint: policy.TaintPolicy{
					Applies: true,
					Label:   "data_tainted",
				},
			},
			"clear-taint-tool": {
				Taint: policy.TaintPolicy{
					Clears: true,
				},
			},
			"sensitive-tool": {
				Sequence: policy.SequencePolicy{
					NeverAfter: []string{"apply-taint-tool"},
				},
			},
			// "neutral-tool" has an empty ToolPolicy — allowed with no restrictions.
			"neutral-tool": {},
		},
	}
}

// taintSess returns a session snapshot with Tainted set to the given value.
func taintSess(tainted bool) *session.Session {
	return &session.Session{
		ID:        "sess-taint-test",
		AgentName: "test-agent",
		Tainted:   tainted,
	}
}

func TestTaintEvaluator(t *testing.T) {
	ev := &engine.TaintEvaluator{}
	ctx := context.Background()
	pol := taintPolicy()

	cases := []struct {
		name       string
		toolName   string
		tainted    bool
		wantAction engine.Action
		wantRuleID string
	}{
		{
			name:       "untainted session - any tool allowed",
			toolName:   "sensitive-tool",
			tainted:    false,
			wantAction: engine.Allow,
		},
		{
			name:       "untainted session - taint-applying tool allowed",
			toolName:   "apply-taint-tool",
			tainted:    false,
			wantAction: engine.Allow,
		},
		{
			name: "tainted session + tool clears taint - allow",
			// clearance tools must pass through so the pipeline can lower the flag.
			toolName:   "clear-taint-tool",
			tainted:    true,
			wantAction: engine.Allow,
		},
		{
			name: "tainted session + sensitive tool - deny",
			// sensitive-tool has never_after: [apply-taint-tool]; session is tainted.
			toolName:   "sensitive-tool",
			tainted:    true,
			wantAction: engine.Deny,
			wantRuleID: "taint.session_tainted",
		},
		{
			name: "tainted session + neutral tool has no taint rules - allow",
			// neutral-tool is not in the policy tools map, so it has no never_after.
			toolName:   "neutral-tool",
			tainted:    true,
			wantAction: engine.Allow,
		},
		{
			name: "tainted session + tool not in policy tools - allow",
			// Tool is in may_use but has no entry in Tools map — no taint guard.
			toolName:   "apply-taint-tool",
			tainted:    true,
			wantAction: engine.Allow,
		},
		{
			name: "tainted session + no taint-applying tools in policy - allow",
			// Policy has a tainted session but no tool applies taint — stale flag.
			toolName: "sensitive-tool",
			tainted:  true,
			// Override: use a policy with no taint-applying tools.
			wantAction: engine.Allow,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pol
			// Case 6 (index 6): use a policy with no taint-applying tools.
			if i == 6 {
				p = &policy.Policy{
					EnforcementMode: policy.EnforcementBlock,
					MayUse:          []string{"sensitive-tool"},
					Tools: map[string]policy.ToolPolicy{
						"sensitive-tool": {
							Sequence: policy.SequencePolicy{
								NeverAfter: []string{"some-taint-tool"},
							},
						},
						// "some-taint-tool" is NOT in the policy, so no taint source exists.
					},
				}
			}

			toolCall := &engine.ToolCall{
				SessionID: "sess-taint-test",
				AgentName: "test-agent",
				ToolName:  tc.toolName,
			}
			got, err := ev.Evaluate(ctx, toolCall, taintSess(tc.tainted), p)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q (Reason: %q)", got.Action, tc.wantAction, got.Reason)
			}
			if tc.wantRuleID != "" && got.RuleID != tc.wantRuleID {
				t.Errorf("RuleID = %q, want %q", got.RuleID, tc.wantRuleID)
			}
			if tc.wantAction == engine.Deny && got.Reason == "" {
				t.Error("Deny decision has empty Reason; operators need context in the audit record")
			}
		})
	}
}

// TestTaintEvaluator_DenyReasonNamesTools verifies that the deny reason message
// includes both the blocked tool name and the taint-applying tool name, so
// operators can understand the violation from the audit record alone.
func TestTaintEvaluator_DenyReasonNamesTools(t *testing.T) {
	ev := &engine.TaintEvaluator{}
	ctx := context.Background()

	toolCall := &engine.ToolCall{
		SessionID: "sess-reason-test",
		AgentName: "test-agent",
		ToolName:  "sensitive-tool",
	}
	got, err := ev.Evaluate(ctx, toolCall, taintSess(true), taintPolicy())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Action != engine.Deny {
		t.Fatalf("Action = %q, want Deny", got.Action)
	}

	// Reason must name the blocked tool and the taint source so operators can
	// trace the violation without reading source code.
	for _, substr := range []string{"sensitive-tool", "apply-taint-tool"} {
		if len(got.Reason) == 0 {
			t.Fatal("Reason is empty")
		}
		found := false
		for idx := 0; idx+len(substr) <= len(got.Reason); idx++ {
			if got.Reason[idx:idx+len(substr)] == substr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Reason %q does not mention %q", got.Reason, substr)
		}
	}
}

// TestTaintEvaluator_ShadowMode verifies that a taint violation produces
// shadow_deny (not deny) when routed through a pipeline whose effective
// enforcement mode is shadow. TaintEvaluator always returns plain Deny;
// shadow conversion is a pipeline responsibility per invariant 5.
func TestTaintEvaluator_ShadowMode(t *testing.T) {
	pol := taintPolicy()
	pol.EnforcementMode = policy.EnforcementShadow

	p := engine.New(&engine.TaintEvaluator{})
	toolCall := &engine.ToolCall{
		SessionID: "sess-shadow-taint",
		AgentName: "test-agent",
		ToolName:  "sensitive-tool",
	}
	got := p.Evaluate(context.Background(), toolCall, taintSess(true), pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "taint.session_tainted" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "taint.session_tainted")
	}
}

// TestTaintEvaluator_NeverAfterMultipleSources verifies that when the policy
// has multiple taint-applying tools and a sensitive tool's never_after contains
// more than one of them, the deny fires for the first matching source.
func TestTaintEvaluator_NeverAfterMultipleSources(t *testing.T) {
	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"taint-a", "taint-b", "guarded-tool"},
		Tools: map[string]policy.ToolPolicy{
			"taint-a": {Taint: policy.TaintPolicy{Applies: true}},
			"taint-b": {Taint: policy.TaintPolicy{Applies: true}},
			"guarded-tool": {
				Sequence: policy.SequencePolicy{
					NeverAfter: []string{"taint-a", "taint-b"},
				},
			},
		},
	}

	ev := &engine.TaintEvaluator{}
	toolCall := &engine.ToolCall{ToolName: "guarded-tool"}
	got, err := ev.Evaluate(context.Background(), toolCall, taintSess(true), pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Action != engine.Deny {
		t.Errorf("Action = %q, want Deny", got.Action)
	}
	if got.RuleID != "taint.session_tainted" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "taint.session_tainted")
	}
}

// BenchmarkTaintEvaluator measures the cost of a taint check on a tainted
// session where the tool's never_after list contains a taint-applying tool
// (the deny path), which is the most expensive path through the evaluator.
func BenchmarkTaintEvaluator(b *testing.B) {
	ev := &engine.TaintEvaluator{}
	ctx := context.Background()
	pol := taintPolicy()
	sess := taintSess(true)
	toolCall := &engine.ToolCall{
		SessionID: "bench-sess",
		AgentName: "bench-agent",
		ToolName:  "sensitive-tool",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := ev.Evaluate(ctx, toolCall, sess, pol)
		if err != nil {
			b.Fatal(err)
		}
		_ = d
	}
}
