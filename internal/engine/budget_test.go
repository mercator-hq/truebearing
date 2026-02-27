package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// budgetPolicy returns a block-mode policy with the given budget limits.
// Pass 0 for a field to leave that limit unconfigured.
func budgetPolicy(maxCalls int, maxCostUSD float64) *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"read_data"},
		Tools:           map[string]policy.ToolPolicy{},
		Budget: policy.BudgetPolicy{
			MaxToolCalls: maxCalls,
			MaxCostUSD:   maxCostUSD,
		},
	}
}

// budgetSession returns a session with the given accumulated counters.
func budgetSession(callCount int, costUSD float64) *session.Session {
	return &session.Session{
		ID:               "sess-budget-test",
		AgentName:        "test-agent",
		ToolCallCount:    callCount,
		EstimatedCostUSD: costUSD,
	}
}

func TestBudgetEvaluator(t *testing.T) {
	ev := &engine.BudgetEvaluator{}
	ctx := context.Background()
	call := &engine.ToolCall{SessionID: "sess-budget-test", AgentName: "test-agent", ToolName: "read_data"}

	cases := []struct {
		name       string
		sess       *session.Session
		pol        *policy.Policy
		wantAction engine.Action
		wantRuleID string
		// wantReasonContains lists substrings that must all appear in Reason on
		// a deny decision. Empty means no Reason content is asserted.
		wantReasonContains []string
	}{
		{
			name:       "under call budget - allowed",
			sess:       budgetSession(10, 0),
			pol:        budgetPolicy(50, 0),
			wantAction: engine.Allow,
		},
		{
			name:       "one under call limit - allowed",
			sess:       budgetSession(49, 0),
			pol:        budgetPolicy(50, 0),
			wantAction: engine.Allow,
		},
		{
			name:       "at exact call limit - denied",
			sess:       budgetSession(50, 0),
			pol:        budgetPolicy(50, 0),
			wantAction: engine.Deny,
			wantRuleID: "budget.max_tool_calls",
		},
		{
			name:       "over call limit - denied",
			sess:       budgetSession(51, 0),
			pol:        budgetPolicy(50, 0),
			wantAction: engine.Deny,
			wantRuleID: "budget.max_tool_calls",
		},
		{
			name:       "under cost budget - allowed",
			sess:       budgetSession(0, 2.50),
			pol:        budgetPolicy(0, 5.00),
			wantAction: engine.Allow,
		},
		{
			name:       "one under cost limit - allowed",
			sess:       budgetSession(0, 4.9999),
			pol:        budgetPolicy(0, 5.00),
			wantAction: engine.Allow,
		},
		{
			name:       "at exact cost limit - denied",
			sess:       budgetSession(0, 5.00),
			pol:        budgetPolicy(0, 5.00),
			wantAction: engine.Deny,
			wantRuleID: "budget.max_cost_usd",
		},
		{
			name:       "over cost limit - denied",
			sess:       budgetSession(0, 6.00),
			pol:        budgetPolicy(0, 5.00),
			wantAction: engine.Deny,
			wantRuleID: "budget.max_cost_usd",
		},
		{
			name:       "no budget configured - allowed",
			sess:       budgetSession(999, 999.99),
			pol:        budgetPolicy(0, 0),
			wantAction: engine.Allow,
		},
		{
			name:       "both limits exceeded - tool call rule wins",
			sess:       budgetSession(51, 6.00),
			pol:        budgetPolicy(50, 5.00),
			wantAction: engine.Deny,
			wantRuleID: "budget.max_tool_calls",
			// Reason must name both violations so operators see the full picture.
			wantReasonContains: []string{"51", "50", "6.0", "5.0"},
		},
		{
			name: "only cost configured - calls uncapped",
			sess: budgetSession(1000, 4.00),
			pol:  budgetPolicy(0, 5.00),
			// MaxToolCalls is 0 so call count is not checked; cost is under limit.
			wantAction: engine.Allow,
		},
		{
			name: "only calls configured - cost uncapped",
			sess: budgetSession(10, 999.99),
			pol:  budgetPolicy(50, 0),
			// MaxCostUSD is 0 so cost is not checked; calls are under limit.
			wantAction: engine.Allow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ev.Evaluate(ctx, call, tc.sess, tc.pol)
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
		})
	}
}

// TestBudgetEvaluator_ShadowMode verifies that a budget violation produces
// shadow_deny (not deny) when routed through a pipeline whose effective
// enforcement mode is shadow. BudgetEvaluator itself always returns plain
// Deny; shadow conversion is a pipeline responsibility per invariant 5.
func TestBudgetEvaluator_ShadowMode(t *testing.T) {
	p := engine.New(&engine.BudgetEvaluator{})
	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementShadow,
		MayUse:          []string{"read_data"},
		Tools:           map[string]policy.ToolPolicy{},
		Budget: policy.BudgetPolicy{
			MaxToolCalls: 5,
		},
	}
	sess := budgetSession(5, 0) // at exact limit
	call := &engine.ToolCall{SessionID: "sess-shadow", AgentName: "test-agent", ToolName: "read_data"}

	got := p.Evaluate(context.Background(), call, sess, pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "budget.max_tool_calls" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "budget.max_tool_calls")
	}
}

// BenchmarkBudgetEvaluator measures the cost of a budget check on a session
// that is well within limits, representing the common hot path.
func BenchmarkBudgetEvaluator(b *testing.B) {
	ev := &engine.BudgetEvaluator{}
	ctx := context.Background()
	pol := budgetPolicy(1000, 10.00)
	sess := budgetSession(500, 5.00)
	call := &engine.ToolCall{SessionID: "bench-sess", AgentName: "bench-agent", ToolName: "read_data"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := ev.Evaluate(ctx, call, sess, pol)
		if err != nil {
			b.Fatal(err)
		}
		_ = d
	}
}
