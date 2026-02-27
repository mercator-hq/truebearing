package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// stubEvaluator is a test double that returns a fixed Decision and error.
// If called is non-nil it records that Evaluate was invoked, which lets tests
// assert that the pipeline did (or did not) reach a particular stage.
type stubEvaluator struct {
	decision engine.Decision
	err      error
	called   *bool
}

func (s *stubEvaluator) Evaluate(_ context.Context, _ *engine.ToolCall, _ *session.Session, _ *policy.Policy) (engine.Decision, error) {
	if s.called != nil {
		*s.called = true
	}
	return s.decision, s.err
}

// blockPolicy returns a minimal policy with global enforcement_mode: block and
// no per-tool overrides, suitable for tests that expect real block behaviour.
func blockPolicy() *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		Tools:           map[string]policy.ToolPolicy{},
	}
}

// shadowPolicy returns a minimal policy with global enforcement_mode: shadow.
func shadowPolicy() *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementShadow,
		Tools:           map[string]policy.ToolPolicy{},
	}
}

// call returns a minimal ToolCall with the given tool name.
func call(toolName string) *engine.ToolCall {
	return &engine.ToolCall{
		SessionID: "sess-test",
		AgentName: "test-agent",
		ToolName:  toolName,
	}
}

// sess returns a minimal Session snapshot.
func sess() *session.Session {
	return &session.Session{ID: "sess-test", AgentName: "test-agent"}
}

func TestPipeline_Evaluate(t *testing.T) {
	allow := engine.Decision{Action: engine.Allow}
	deny := engine.Decision{Action: engine.Deny, Reason: "policy violation", RuleID: "test.rule"}
	escalate := engine.Decision{Action: engine.Escalate, Reason: "needs approval", RuleID: "test.escalate"}

	cases := []struct {
		name       string
		stages     func() []engine.Evaluator // factory so each test gets fresh stubs
		pol        *policy.Policy
		wantAction engine.Action
		wantRuleID string
	}{
		{
			name:       "zero evaluators returns allow",
			stages:     func() []engine.Evaluator { return nil },
			pol:        blockPolicy(),
			wantAction: engine.Allow,
		},
		{
			name: "all evaluators allow returns allow",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: allow},
					&stubEvaluator{decision: allow},
				}
			},
			pol:        blockPolicy(),
			wantAction: engine.Allow,
		},
		{
			name: "first deny terminates pipeline in block mode",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: deny},
					&stubEvaluator{decision: allow},
				}
			},
			pol:        blockPolicy(),
			wantAction: engine.Deny,
			wantRuleID: "test.rule",
		},
		{
			name: "second evaluator not called when first denies",
			stages: func() []engine.Evaluator {
				secondCalled := false
				return []engine.Evaluator{
					&stubEvaluator{decision: deny},
					&stubEvaluator{decision: allow, called: &secondCalled},
				}
			},
			pol:        blockPolicy(),
			wantAction: engine.Deny,
		},
		{
			name: "evaluator error produces deny",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{err: errors.New("db unavailable")},
				}
			},
			pol:        blockPolicy(),
			wantAction: engine.Deny,
			wantRuleID: "internal_error",
		},
		{
			name: "evaluator error in shadow mode still produces deny",
			// Design: errors mean fail-closed regardless of shadow mode.
			// An evaluator that cannot evaluate safely cannot allow the call.
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{err: errors.New("policy parse error")},
				}
			},
			pol:        shadowPolicy(),
			wantAction: engine.Deny,
			wantRuleID: "internal_error",
		},
		{
			name: "deny in global shadow mode becomes shadow_deny",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: deny},
				}
			},
			pol:        shadowPolicy(),
			wantAction: engine.ShadowDeny,
			wantRuleID: "test.rule",
		},
		{
			name: "escalate in global shadow mode becomes shadow_deny",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: escalate},
				}
			},
			pol:        shadowPolicy(),
			wantAction: engine.ShadowDeny,
		},
		{
			name: "deny in block mode stays deny",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: deny},
				}
			},
			pol:        blockPolicy(),
			wantAction: engine.Deny,
		},
		{
			name: "tool-level block overrides global shadow",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: deny},
				}
			},
			pol: &policy.Policy{
				EnforcementMode: policy.EnforcementShadow,
				Tools: map[string]policy.ToolPolicy{
					"guarded-tool": {EnforcementMode: policy.EnforcementBlock},
				},
			},
			wantAction: engine.Deny,
		},
		{
			name: "tool-level shadow overrides global block",
			stages: func() []engine.Evaluator {
				return []engine.Evaluator{
					&stubEvaluator{decision: deny},
				}
			},
			pol: &policy.Policy{
				EnforcementMode: policy.EnforcementBlock,
				Tools: map[string]policy.ToolPolicy{
					"guarded-tool": {EnforcementMode: policy.EnforcementShadow},
				},
			},
			wantAction: engine.ShadowDeny,
		},
		{
			name: "allow passes through all evaluators",
			stages: func() []engine.Evaluator {
				secondCalled := false
				return []engine.Evaluator{
					&stubEvaluator{decision: allow},
					&stubEvaluator{decision: allow, called: &secondCalled},
				}
			},
			pol:        blockPolicy(),
			wantAction: engine.Allow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := engine.New(tc.stages()...)
			got := p.Evaluate(context.Background(), call("guarded-tool"), sess(), tc.pol)

			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tc.wantAction)
			}
			if tc.wantRuleID != "" && got.RuleID != tc.wantRuleID {
				t.Errorf("RuleID = %q, want %q", got.RuleID, tc.wantRuleID)
			}
		})
	}
}

// TestPipeline_FirstFailureStopsExecution verifies that a stage after a
// failing stage is provably not called.
func TestPipeline_FirstFailureStopsExecution(t *testing.T) {
	secondCalled := false
	p := engine.New(
		&stubEvaluator{decision: engine.Decision{Action: engine.Deny, RuleID: "a"}},
		&stubEvaluator{decision: engine.Decision{Action: engine.Allow}, called: &secondCalled},
	)

	p.Evaluate(context.Background(), call("any-tool"), sess(), blockPolicy())

	if secondCalled {
		t.Error("second evaluator was called after first returned Deny; pipeline must stop on first failure")
	}
}

// TestPipeline_ErrorReasonContainsOriginalError verifies that the Deny reason
// produced on evaluator error embeds the original error text for auditability.
func TestPipeline_ErrorReasonContainsOriginalError(t *testing.T) {
	p := engine.New(&stubEvaluator{err: errors.New("sqlite: database is locked")})
	got := p.Evaluate(context.Background(), call("any-tool"), sess(), blockPolicy())

	if got.Action != engine.Deny {
		t.Fatalf("Action = %q, want Deny", got.Action)
	}
	const wantSubstr = "sqlite: database is locked"
	if !strings.Contains(got.Reason, wantSubstr) {
		t.Errorf("Reason %q does not contain %q", got.Reason, wantSubstr)
	}
}

// TestPipeline_ShadowDenyPreservesRuleID verifies that shadow conversion does
// not erase the RuleID, which operators need for audit analysis.
func TestPipeline_ShadowDenyPreservesRuleID(t *testing.T) {
	p := engine.New(&stubEvaluator{
		decision: engine.Decision{Action: engine.Deny, RuleID: "sequence.only_after", Reason: "must run after approve"},
	})
	got := p.Evaluate(context.Background(), call("any-tool"), sess(), shadowPolicy())

	if got.Action != engine.ShadowDeny {
		t.Fatalf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "sequence.only_after" {
		t.Errorf("RuleID = %q after shadow conversion, want %q", got.RuleID, "sequence.only_after")
	}
}

// policyWithTaint returns a block-mode policy where "taint-source" applies
// taint and "taint-clearer" clears it. Used by taint mutation tests.
func policyWithTaint() *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"taint-source", "taint-clearer", "plain-tool"},
		Tools: map[string]policy.ToolPolicy{
			"taint-source":  {Taint: policy.TaintPolicy{Applies: true}},
			"taint-clearer": {Taint: policy.TaintPolicy{Clears: true}},
			"plain-tool":    {},
		},
	}
}

// TestPipeline_TaintMutation_AppliesOnAllow verifies that calling a tool with
// taint.applies == true sets sess.Tainted = true after an Allow decision.
func TestPipeline_TaintMutation_AppliesOnAllow(t *testing.T) {
	p := engine.New(&stubEvaluator{decision: engine.Decision{Action: engine.Allow}})
	s := &session.Session{ID: "sess-mut", Tainted: false}

	got := p.Evaluate(context.Background(), call("taint-source"), s, policyWithTaint())
	if got.Action != engine.Allow {
		t.Fatalf("Action = %q, want Allow", got.Action)
	}
	if !s.Tainted {
		t.Error("sess.Tainted = false after allowed call with taint.applies; want true")
	}
}

// TestPipeline_TaintMutation_ClearsOnAllow verifies that calling a tool with
// taint.clears == true sets sess.Tainted = false after an Allow decision.
func TestPipeline_TaintMutation_ClearsOnAllow(t *testing.T) {
	p := engine.New(&stubEvaluator{decision: engine.Decision{Action: engine.Allow}})
	s := &session.Session{ID: "sess-mut", Tainted: true}

	got := p.Evaluate(context.Background(), call("taint-clearer"), s, policyWithTaint())
	if got.Action != engine.Allow {
		t.Fatalf("Action = %q, want Allow", got.Action)
	}
	if s.Tainted {
		t.Error("sess.Tainted = true after allowed call with taint.clears; want false")
	}
}

// TestPipeline_TaintMutation_NoMutationOnDeny verifies that a Deny decision
// does not trigger taint mutations. The session taint state is unchanged when
// the call is blocked.
func TestPipeline_TaintMutation_NoMutationOnDeny(t *testing.T) {
	denyEv := &stubEvaluator{decision: engine.Decision{Action: engine.Deny, RuleID: "some.rule"}}
	p := engine.New(denyEv)
	s := &session.Session{ID: "sess-mut", Tainted: false}

	got := p.Evaluate(context.Background(), call("taint-source"), s, policyWithTaint())
	if got.Action != engine.Deny {
		t.Fatalf("Action = %q, want Deny", got.Action)
	}
	if s.Tainted {
		t.Error("sess.Tainted = true after denied call; taint mutations must not fire on non-Allow decisions")
	}
}

// TestPipeline_TaintMutation_NoMutationOnShadowDeny verifies that a ShadowDeny
// decision does not trigger taint mutations. Shadow mode allows the call through
// to upstream but the engine's decision is still a violation.
func TestPipeline_TaintMutation_NoMutationOnShadowDeny(t *testing.T) {
	denyEv := &stubEvaluator{decision: engine.Decision{Action: engine.Deny, RuleID: "some.rule"}}
	p := engine.New(denyEv)
	s := &session.Session{ID: "sess-mut", Tainted: false}

	pol := policyWithTaint()
	pol.EnforcementMode = policy.EnforcementShadow

	got := p.Evaluate(context.Background(), call("taint-source"), s, pol)
	if got.Action != engine.ShadowDeny {
		t.Fatalf("Action = %q, want ShadowDeny", got.Action)
	}
	if s.Tainted {
		t.Error("sess.Tainted = true after shadow_deny decision; taint mutations must not fire on non-Allow decisions")
	}
}

// TestPipeline_TaintMutation_PlainToolNoMutation verifies that a tool with no
// taint policy leaves sess.Tainted unchanged whether it starts true or false.
func TestPipeline_TaintMutation_PlainToolNoMutation(t *testing.T) {
	p := engine.New(&stubEvaluator{decision: engine.Decision{Action: engine.Allow}})

	for _, initial := range []bool{false, true} {
		s := &session.Session{ID: "sess-mut", Tainted: initial}
		got := p.Evaluate(context.Background(), call("plain-tool"), s, policyWithTaint())
		if got.Action != engine.Allow {
			t.Fatalf("initial=%v: Action = %q, want Allow", initial, got.Action)
		}
		if s.Tainted != initial {
			t.Errorf("initial=%v: sess.Tainted changed to %v; plain-tool has no taint policy", initial, s.Tainted)
		}
	}
}
