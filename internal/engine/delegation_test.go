package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// registerParent is a test helper that inserts a parent agent into the store
// with the given allowed tools.
func registerParent(t *testing.T, st *store.Store, name string, tools []string) {
	t.Helper()
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("registerParent: marshalling tools: %v", err)
	}
	if err := st.UpsertAgent(&store.Agent{
		Name:             name,
		PublicKeyPEM:     "fake-pem-for-tests",
		PolicyFile:       "test.policy.yaml",
		AllowedToolsJSON: string(toolsJSON),
		RegisteredAt:     time.Now().UnixNano(),
		JWTPreview:       "fake-jwt-preview",
	}); err != nil {
		t.Fatalf("registerParent: upserting agent %q: %v", name, err)
	}
}

// delegCall builds a ToolCall with the given agent, parent, and tool names.
func delegCall(agentName, parentAgent, toolName string) *engine.ToolCall {
	return &engine.ToolCall{
		SessionID:   "deleg-sess",
		AgentName:   agentName,
		ToolName:    toolName,
		ParentAgent: parentAgent,
	}
}

// delegPolicy returns a minimal policy for delegation evaluator tests.
func delegPolicy() *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"read_invoice", "verify_invoice", "execute_payment"},
	}
}

func TestDelegationEvaluator(t *testing.T) {
	ctx := context.Background()
	st := store.NewTestDB(t)

	// Register a parent agent that is only allowed to call two of the three tools.
	registerParent(t, st, "payments-agent", []string{"read_invoice", "verify_invoice"})

	eval := &engine.DelegationEvaluator{Store: st}
	pol := delegPolicy()
	sess := &session.Session{ID: "deleg-sess", AgentName: "child-agent"}

	cases := []struct {
		name               string
		agentName          string
		parentAgent        string
		toolName           string
		wantAction         engine.Action
		wantRuleID         string
		wantReasonContains []string
	}{
		// ── Root agent (no parent): always allowed by this evaluator ──────
		{
			name:        "root agent no parent - allow",
			agentName:   "root-agent",
			parentAgent: "",
			toolName:    "execute_payment",
			wantAction:  engine.Allow,
		},
		{
			name:        "root agent no parent - any tool - allow",
			agentName:   "root-agent",
			parentAgent: "",
			toolName:    "read_invoice",
			wantAction:  engine.Allow,
		},

		// ── Child within parent's tool set: allow ─────────────────────────
		{
			name:        "child calls tool in parent scope - allow",
			agentName:   "child-agent",
			parentAgent: "payments-agent",
			toolName:    "read_invoice",
			wantAction:  engine.Allow,
		},
		{
			name:        "child calls second tool in parent scope - allow",
			agentName:   "child-agent",
			parentAgent: "payments-agent",
			toolName:    "verify_invoice",
			wantAction:  engine.Allow,
		},

		// ── Child exceeds parent's tool set: deny ─────────────────────────
		{
			name:               "child calls tool not in parent scope - deny",
			agentName:          "child-agent",
			parentAgent:        "payments-agent",
			toolName:           "execute_payment",
			wantAction:         engine.Deny,
			wantRuleID:         "delegation.exceeds_parent",
			wantReasonContains: []string{"execute_payment", "payments-agent"},
		},

		// ── Boundary: parent allows empty tool set ────────────────────────
		// (after registerParent below with an empty list)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := delegCall(tc.agentName, tc.parentAgent, tc.toolName)
			got, err := eval.Evaluate(ctx, call, sess, pol)
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

	// ── Boundary: parent has empty allowed tool set ────────────────────
	t.Run("parent with empty tool set - any child tool denied", func(t *testing.T) {
		registerParent(t, st, "empty-parent", []string{})
		call := delegCall("child-agent", "empty-parent", "read_invoice")
		got, err := eval.Evaluate(ctx, call, sess, pol)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Action != engine.Deny {
			t.Errorf("Action = %q, want deny", got.Action)
		}
		if got.RuleID != "delegation.exceeds_parent" {
			t.Errorf("RuleID = %q, want delegation.exceeds_parent", got.RuleID)
		}
	})

	// ── Error path: parent not found in store ─────────────────────────
	t.Run("parent not registered - error causes deny via pipeline", func(t *testing.T) {
		call := delegCall("child-agent", "nonexistent-parent", "read_invoice")
		_, err := eval.Evaluate(ctx, call, sess, pol)
		if err == nil {
			t.Error("expected error when parent agent is not found; got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent-parent") {
			t.Errorf("error %q does not mention the missing parent agent name", err.Error())
		}
	})
}

// TestDelegationEvaluator_ShadowMode verifies that a delegation violation
// produces shadow_deny (not deny) when the policy enforcement mode is shadow.
// DelegationEvaluator always returns plain Deny; shadow conversion is the
// pipeline's responsibility per invariant 5.
func TestDelegationEvaluator_ShadowMode(t *testing.T) {
	st := store.NewTestDB(t)
	registerParent(t, st, "parent-agent", []string{"safe_tool"})

	if err := st.CreateSession("shadow-deleg-sess", "child-agent", "fp"); err != nil {
		t.Fatalf("creating session: %v", err)
	}

	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementShadow,
		MayUse:          []string{"safe_tool", "restricted_tool"},
	}

	pip := engine.New(
		&engine.MayUseEvaluator{},
		&engine.DelegationEvaluator{Store: st},
	)
	call := &engine.ToolCall{
		SessionID:   "shadow-deleg-sess",
		AgentName:   "child-agent",
		ToolName:    "restricted_tool", // in may_use but not in parent's scope
		ParentAgent: "parent-agent",
	}
	sess := &session.Session{ID: "shadow-deleg-sess", AgentName: "child-agent"}

	got := pip.Evaluate(context.Background(), call, sess, pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "delegation.exceeds_parent" {
		t.Errorf("RuleID = %q, want delegation.exceeds_parent", got.RuleID)
	}
}

// BenchmarkDelegationEvaluator measures evaluation cost for both the root-agent
// fast path and the full child-agent parent-lookup path. Target p99 < 2ms per
// CLAUDE.md §5. The store lookup path involves a SQLite read; the fast path
// (no parent) should be sub-microsecond.
func BenchmarkDelegationEvaluator(b *testing.B) {
	// store.NewTestDB only accepts *testing.T, so we open an in-memory database
	// directly for benchmarks. The schema is applied by store.Open.
	st, err := store.Open("file:bench_delegation?mode=memory&cache=shared")
	if err != nil {
		b.Fatalf("opening benchmark store: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	tools := make([]string, 50)
	for i := range tools {
		tools[i] = "tool_" + strings.Repeat("a", i+1)
	}
	registerParentForBench(b, st, "bench-parent", tools)

	eval := &engine.DelegationEvaluator{Store: st}
	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          tools,
	}
	sess := &session.Session{ID: "bench-deleg-sess", AgentName: "bench-child"}

	b.Run("root_agent_fast_path", func(b *testing.B) {
		call := &engine.ToolCall{
			SessionID:   "bench-deleg-sess",
			AgentName:   "bench-root",
			ToolName:    tools[len(tools)-1],
			ParentAgent: "",
		}
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			d, err := eval.Evaluate(ctx, call, sess, pol)
			if err != nil {
				b.Fatal(err)
			}
			if d.Action != engine.Allow {
				b.Fatalf("expected allow, got %q", d.Action)
			}
		}
	})

	b.Run("child_within_parent_scope", func(b *testing.B) {
		// Call the last tool in the list to exercise the full linear scan.
		call := &engine.ToolCall{
			SessionID:   "bench-deleg-sess",
			AgentName:   "bench-child",
			ToolName:    tools[len(tools)-1],
			ParentAgent: "bench-parent",
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
	})
}

// registerParentForBench is like registerParent but accepts testing.TB so it
// works for both tests and benchmarks.
func registerParentForBench(tb testing.TB, st *store.Store, name string, tools []string) {
	tb.Helper()
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		tb.Fatalf("registerParentForBench: marshalling tools: %v", err)
	}
	if err := st.UpsertAgent(&store.Agent{
		Name:             name,
		PublicKeyPEM:     "fake-pem-for-tests",
		PolicyFile:       "test.policy.yaml",
		AllowedToolsJSON: string(toolsJSON),
		RegisteredAt:     time.Now().UnixNano(),
		JWTPreview:       "fake-jwt-preview",
	}); err != nil {
		tb.Fatalf("registerParentForBench: upserting agent %q: %v", name, err)
	}
}
