package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// mayUsePolicy returns a minimal block-mode policy with the given tool names
// in may_use. Used by MayUse evaluator tests.
func mayUsePolicy(tools ...string) *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          tools,
		Tools:           map[string]policy.ToolPolicy{},
	}
}

func TestMayUseEvaluator(t *testing.T) {
	ev := &engine.MayUseEvaluator{}
	ctx := context.Background()
	s := &session.Session{ID: "sess-test", AgentName: "test-agent"}

	cases := []struct {
		name       string
		toolName   string
		pol        *policy.Policy
		wantAction engine.Action
		wantRuleID string
	}{
		{
			name:       "tool in may_use is allowed",
			toolName:   "read_data",
			pol:        mayUsePolicy("read_data", "submit_data"),
			wantAction: engine.Allow,
		},
		{
			name:       "second tool in may_use is allowed",
			toolName:   "submit_data",
			pol:        mayUsePolicy("read_data", "submit_data"),
			wantAction: engine.Allow,
		},
		{
			name:       "tool not in may_use is denied",
			toolName:   "delete_record",
			pol:        mayUsePolicy("read_data", "submit_data"),
			wantAction: engine.Deny,
			wantRuleID: "may_use",
		},
		{
			name:       "empty may_use denies all tools",
			toolName:   "read_data",
			pol:        mayUsePolicy(),
			wantAction: engine.Deny,
			wantRuleID: "may_use",
		},
		{
			name:       "check_escalation_status allowed when absent from may_use",
			toolName:   "check_escalation_status",
			pol:        mayUsePolicy("read_data", "submit_data"),
			wantAction: engine.Allow,
		},
		{
			name:       "check_escalation_status allowed with empty may_use",
			toolName:   "check_escalation_status",
			pol:        mayUsePolicy(),
			wantAction: engine.Allow,
		},
		{
			name:       "deny reason names the rejected tool",
			toolName:   "dangerous_action",
			pol:        mayUsePolicy("safe_action"),
			wantAction: engine.Deny,
			wantRuleID: "may_use",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &engine.ToolCall{
				SessionID: "sess-test",
				AgentName: "test-agent",
				ToolName:  tc.toolName,
			}
			got, err := ev.Evaluate(ctx, c, s, tc.pol)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tc.wantAction)
			}
			if tc.wantRuleID != "" && got.RuleID != tc.wantRuleID {
				t.Errorf("RuleID = %q, want %q", got.RuleID, tc.wantRuleID)
			}
			// All deny decisions must name the tool in the reason so operators
			// can identify which tool was blocked without reading source code.
			if got.Action == engine.Deny {
				if !strings.Contains(got.Reason, tc.toolName) {
					t.Errorf("deny reason %q does not name tool %q", got.Reason, tc.toolName)
				}
			}
		})
	}
}

// TestMayUseEvaluator_ShadowMode verifies that a tool absent from may_use
// produces shadow_deny (not deny) when routed through a pipeline whose
// effective enforcement mode is shadow. The MayUse evaluator itself always
// returns Deny; shadow conversion is a pipeline responsibility.
func TestMayUseEvaluator_ShadowMode(t *testing.T) {
	p := engine.New(&engine.MayUseEvaluator{})
	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementShadow,
		MayUse:          []string{"allowed_tool"},
		Tools:           map[string]policy.ToolPolicy{},
	}
	c := &engine.ToolCall{
		SessionID: "sess-shadow",
		AgentName: "test-agent",
		ToolName:  "blocked_tool",
	}
	s := &session.Session{ID: "sess-shadow", AgentName: "test-agent"}

	got := p.Evaluate(context.Background(), c, s, pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "may_use" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "may_use")
	}
}

// BenchmarkMayUseEvaluator measures the cost of a may_use check against a
// list of 50 tools. The benchmarked call uses the last tool in the list to
// exercise the worst-case linear scan.
func BenchmarkMayUseEvaluator(b *testing.B) {
	tools := make([]string, 50)
	for i := range tools {
		tools[i] = fmt.Sprintf("tool_%02d", i)
	}
	pol := mayUsePolicy(tools...)

	ev := &engine.MayUseEvaluator{}
	ctx := context.Background()
	s := &session.Session{ID: "bench-sess", AgentName: "bench-agent"}
	// Use the last entry to exercise worst-case linear scan.
	c := &engine.ToolCall{ToolName: "tool_49"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := ev.Evaluate(ctx, c, s, pol)
		if err != nil {
			b.Fatal(err)
		}
		_ = d
	}
}
