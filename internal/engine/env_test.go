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

// envSess returns a minimal session for env evaluator tests.
func envSess() *session.Session {
	return &session.Session{ID: "env-sess", AgentName: "deploy-agent"}
}

// envPolicy builds a policy with the given require_env value.
func envPolicy(requireEnv string) *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"deploy_production"},
		Session: policy.SessionPolicy{
			RequireEnv: requireEnv,
		},
	}
}

// envCall builds a ToolCall with the given agent environment claim.
func envCall(agentEnv string) *engine.ToolCall {
	return &engine.ToolCall{
		SessionID: "env-sess",
		AgentName: "deploy-agent",
		ToolName:  "deploy_production",
		AgentEnv:  agentEnv,
	}
}

func TestEnvEvaluator(t *testing.T) {
	ctx := context.Background()
	eval := &engine.EnvEvaluator{}

	cases := []struct {
		name               string
		requireEnv         string
		agentEnv           string
		wantAction         engine.Action
		wantRuleID         string
		wantReasonContains []string
	}{
		// ── No restriction configured ────────────────────────────────────────
		{
			name:       "require_env empty - no restriction - allow",
			requireEnv: "",
			agentEnv:   "",
			wantAction: engine.Allow,
		},
		{
			name:       "require_env empty - agent has env claim - still allow",
			requireEnv: "",
			agentEnv:   "production",
			wantAction: engine.Allow,
		},

		// ── Happy path: env matches ──────────────────────────────────────────
		{
			name:       "require_env production, agent env production - allow",
			requireEnv: "production",
			agentEnv:   "production",
			wantAction: engine.Allow,
		},
		{
			name:       "require_env staging, agent env staging - allow",
			requireEnv: "staging",
			agentEnv:   "staging",
			wantAction: engine.Allow,
		},

		// ── Deny paths: env mismatch ─────────────────────────────────────────
		{
			name:               "require_env production, agent env staging - deny",
			requireEnv:         "production",
			agentEnv:           "staging",
			wantAction:         engine.Deny,
			wantRuleID:         "env.mismatch",
			wantReasonContains: []string{"production", "staging"},
		},
		{
			name:               "require_env production, agent env empty - deny",
			requireEnv:         "production",
			agentEnv:           "",
			wantAction:         engine.Deny,
			wantRuleID:         "env.mismatch",
			wantReasonContains: []string{"production", "(none"},
		},
		{
			name:               "require_env staging, agent env production - deny",
			requireEnv:         "staging",
			agentEnv:           "production",
			wantAction:         engine.Deny,
			wantRuleID:         "env.mismatch",
			wantReasonContains: []string{"staging", "production"},
		},

		// ── Boundary: case-sensitive match ───────────────────────────────────
		{
			name:               "require_env is case-sensitive - Production != production",
			requireEnv:         "production",
			agentEnv:           "Production",
			wantAction:         engine.Deny,
			wantRuleID:         "env.mismatch",
			wantReasonContains: []string{"production", "Production"},
		},
		{
			name:       "exact case match - allow",
			requireEnv: "Production",
			agentEnv:   "Production",
			wantAction: engine.Allow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := envPolicy(tc.requireEnv)
			call := envCall(tc.agentEnv)

			got, err := eval.Evaluate(ctx, call, envSess(), pol)
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

// TestEnvEvaluator_ShadowMode verifies that an environment mismatch produces
// shadow_deny (not deny) when the policy enforcement mode is shadow.
// EnvEvaluator always returns plain Deny; shadow conversion is a pipeline
// responsibility per invariant 5.
func TestEnvEvaluator_ShadowMode(t *testing.T) {
	st := store.NewTestDB(t)
	if err := st.CreateSession("shadow-env-sess", "deploy-agent", "fp"); err != nil {
		t.Fatalf("creating session: %v", err)
	}

	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementShadow,
		MayUse:          []string{"deploy_production"},
		Session: policy.SessionPolicy{
			RequireEnv: "production",
		},
	}

	pip := engine.New(
		&engine.EnvEvaluator{},
		&engine.MayUseEvaluator{},
	)
	call := &engine.ToolCall{
		SessionID: "shadow-env-sess",
		AgentName: "deploy-agent",
		ToolName:  "deploy_production",
		AgentEnv:  "staging", // wrong env
	}
	sess := &session.Session{ID: "shadow-env-sess", AgentName: "deploy-agent"}

	got := pip.Evaluate(context.Background(), call, sess, pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "env.mismatch" {
		t.Errorf("RuleID = %q, want env.mismatch", got.RuleID)
	}
}

// BenchmarkEnvEvaluator measures evaluation cost for the common case where
// require_env is set and the agent's env matches. Target: p99 < 2ms per
// CLAUDE.md §5. The env evaluator does no I/O so it is expected to be
// well under a microsecond on any modern hardware.
func BenchmarkEnvEvaluator(b *testing.B) {
	eval := &engine.EnvEvaluator{}
	pol := envPolicy("production")
	sess := &session.Session{ID: "bench-env-sess", AgentName: "bench-agent"}
	call := &engine.ToolCall{
		SessionID: "bench-env-sess",
		AgentName: "bench-agent",
		ToolName:  "deploy_production",
		AgentEnv:  "production",
	}
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
