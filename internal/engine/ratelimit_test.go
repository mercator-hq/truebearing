package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// rlStore creates a test store with a single session pre-created for the given ID.
func rlStore(t *testing.T, sessionID string) *store.Store {
	t.Helper()
	st := store.NewTestDB(t)
	if err := st.CreateSession(sessionID, "test-agent", "fp"); err != nil {
		t.Fatalf("creating session: %v", err)
	}
	return st
}

// rlPolicy builds a policy with rate_limit on the named tool.
func rlPolicy(toolName string, maxCalls, windowSeconds int) *policy.Policy {
	return &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{toolName, "other-tool"},
		Tools: map[string]policy.ToolPolicy{
			toolName: {
				RateLimit: &policy.RateLimitPolicy{
					MaxCalls:      maxCalls,
					WindowSeconds: windowSeconds,
				},
			},
		},
	}
}

// rlSess returns a minimal session for rate-limit evaluator tests.
func rlSess(sessionID string) *session.Session {
	return &session.Session{ID: sessionID, AgentName: "test-agent"}
}

// rlCall builds a ToolCall with the given RequestedAt for the given session.
func rlCall(sessionID, toolName string, requestedAt time.Time) *engine.ToolCall {
	return &engine.ToolCall{
		SessionID:   sessionID,
		AgentName:   "test-agent",
		ToolName:    toolName,
		RequestedAt: requestedAt,
	}
}

// appendAllowedAt appends an "allow" event for toolName at the given timestamp.
func appendAllowedAt(t *testing.T, st *store.Store, sessionID, toolName string, at time.Time) {
	t.Helper()
	ev := &store.SessionEvent{
		SessionID:  sessionID,
		ToolName:   toolName,
		Decision:   "allow",
		RecordedAt: at.UnixNano(),
	}
	if err := st.AppendEvent(ev); err != nil {
		t.Fatalf("appending event: %v", err)
	}
}

func TestRateLimitEvaluator(t *testing.T) {
	const (
		sid  = "sess-ratelimit"
		tool = "search_web"
	)

	now := time.Now()
	ctx := context.Background()

	cases := []struct {
		name          string
		maxCalls      int
		windowSeconds int
		// history is (toolName, offset from now) for each event to pre-seed.
		history []struct {
			tool string
			age  time.Duration
		}
		// callTool overrides the tool being called (default: tool)
		callTool           string
		requestedAt        time.Time // zero → use now
		wantAction         engine.Action
		wantRuleID         string
		wantReasonContains []string
	}{
		// ── Happy path: no previous calls ─────────────────────────────────────
		{
			name:          "no history - under limit - allow",
			maxCalls:      3,
			windowSeconds: 60,
			history:       nil,
			wantAction:    engine.Allow,
		},

		// ── Happy path: under the limit ────────────────────────────────────────
		{
			name:          "two calls in window with limit 3 - allow",
			maxCalls:      3,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{tool, 10 * time.Second},
				{tool, 20 * time.Second},
			},
			wantAction: engine.Allow,
		},

		// ── Boundary: exactly at the limit (count == maxCalls - 1) ─────────────
		{
			name:          "exactly maxCalls-1 calls - still allow",
			maxCalls:      5,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{tool, 5 * time.Second},
				{tool, 10 * time.Second},
				{tool, 15 * time.Second},
				{tool, 20 * time.Second},
			},
			wantAction: engine.Allow,
		},

		// ── Deny: at the limit (count == maxCalls) ─────────────────────────────
		{
			name:          "exactly maxCalls calls in window - deny",
			maxCalls:      3,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{tool, 10 * time.Second},
				{tool, 20 * time.Second},
				{tool, 30 * time.Second},
			},
			wantAction: engine.Deny,
			wantRuleID: "rate_limit.search_web",
			wantReasonContains: []string{
				"search_web",
				"3",  // count
				"60", // window_seconds
			},
		},

		// ── Deny: over the limit (count > maxCalls) ───────────────────────────
		{
			name:          "more than maxCalls calls in window - deny",
			maxCalls:      2,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{tool, 5 * time.Second},
				{tool, 10 * time.Second},
				{tool, 15 * time.Second},
			},
			wantAction: engine.Deny,
			wantRuleID: "rate_limit.search_web",
		},

		// ── Events outside the window do not count ────────────────────────────
		{
			name:          "calls older than window excluded - allow",
			maxCalls:      2,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{tool, 90 * time.Second},  // outside 60s window
				{tool, 120 * time.Second}, // outside 60s window
				{tool, 10 * time.Second},  // inside window (1 call)
			},
			wantAction: engine.Allow,
		},
		{
			name:          "all calls outside window excluded - allow",
			maxCalls:      1,
			windowSeconds: 30,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{tool, 60 * time.Second},
				{tool, 90 * time.Second},
			},
			wantAction: engine.Allow,
		},

		// ── Other tools' events excluded ──────────────────────────────────────
		{
			name:          "other tool calls do not count toward limit - allow",
			maxCalls:      2,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{"other-tool", 10 * time.Second},
				{"other-tool", 20 * time.Second},
				{"other-tool", 30 * time.Second},
				{tool, 10 * time.Second}, // only 1 for the rate-limited tool
			},
			wantAction: engine.Allow,
		},
		{
			name:          "other tool at limit does not deny rate-limited tool - allow",
			maxCalls:      2,
			windowSeconds: 60,
			history: []struct {
				tool string
				age  time.Duration
			}{
				{"other-tool", 5 * time.Second},
				{"other-tool", 10 * time.Second},
			},
			callTool:   tool,
			wantAction: engine.Allow,
		},

		// ── Denied events excluded from rate-limit count ───────────────────────
		{
			name:          "denied events do not count toward rate limit - allow",
			maxCalls:      2,
			windowSeconds: 60,
			// Only allowed events count; denied ones are not reached upstream.
			history:    nil, // we seed via appendDeniedAt helper in the subtest body
			wantAction: engine.Allow,
		},

		// ── No rate_limit policy on tool → always allow ────────────────────────
		{
			name:          "tool with no rate_limit config - allow",
			maxCalls:      0, // signals: use a policy with no rate_limit
			windowSeconds: 0,
			history:       nil,
			wantAction:    engine.Allow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each sub-test gets its own session ID so events don't bleed across tests.
			localSID := fmt.Sprintf("%s-%s", sid, strings.ReplaceAll(tc.name, " ", "-"))
			localSt := rlStore(t, localSID)
			eval := &engine.RateLimitEvaluator{Store: localSt}

			// Build the policy.
			var pol *policy.Policy
			if tc.maxCalls <= 0 && tc.windowSeconds <= 0 {
				// No rate_limit — tool present in may_use but no ToolPolicy entry.
				pol = &policy.Policy{
					EnforcementMode: policy.EnforcementBlock,
					MayUse:          []string{tool},
				}
			} else {
				pol = rlPolicy(tool, tc.maxCalls, tc.windowSeconds)
			}

			callTool := tc.callTool
			if callTool == "" {
				callTool = tool
			}
			requestedAt := tc.requestedAt
			if requestedAt.IsZero() {
				requestedAt = now
			}

			// Special case: seed denied events to verify they are excluded.
			if tc.name == "denied events do not count toward rate limit - allow" {
				for i := 0; i < 5; i++ {
					ev := &store.SessionEvent{
						SessionID:  localSID,
						ToolName:   tool,
						Decision:   "deny",
						RecordedAt: now.Add(-time.Duration(i+1) * 5 * time.Second).UnixNano(),
					}
					if err := localSt.AppendEvent(ev); err != nil {
						t.Fatalf("seeding denied event: %v", err)
					}
				}
			} else {
				for _, h := range tc.history {
					appendAllowedAt(t, localSt, localSID, h.tool, now.Add(-h.age))
				}
			}

			call := rlCall(localSID, callTool, requestedAt)
			got, err := eval.Evaluate(ctx, call, rlSess(localSID), pol)
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

// TestRateLimitEvaluator_ShadowMode verifies that a rate-limit violation produces
// shadow_deny (not deny) when routed through a pipeline with shadow enforcement.
// RateLimitEvaluator always returns plain Deny; shadow conversion is a pipeline
// responsibility per invariant 5.
func TestRateLimitEvaluator_ShadowMode(t *testing.T) {
	const sid = "sess-rl-shadow"
	now := time.Now()
	st := rlStore(t, sid)

	// Seed 3 calls within the window to trigger the rate limit.
	for i := 0; i < 3; i++ {
		appendAllowedAt(t, st, sid, "search_web", now.Add(-time.Duration(i+1)*5*time.Second))
	}

	pol := rlPolicy("search_web", 3, 60)
	pol.EnforcementMode = policy.EnforcementShadow

	pip := engine.New(&engine.RateLimitEvaluator{Store: st})
	call := rlCall(sid, "search_web", now)
	sess := rlSess(sid)

	got := pip.Evaluate(context.Background(), call, sess, pol)
	if got.Action != engine.ShadowDeny {
		t.Errorf("Action = %q, want shadow_deny", got.Action)
	}
	if got.RuleID != "rate_limit.search_web" {
		t.Errorf("RuleID = %q, want rate_limit.search_web", got.RuleID)
	}
}

// TestRateLimitEvaluator_StoreError verifies that a store error is propagated
// so the pipeline converts it to a Deny (fail closed per CLAUDE.md §6 invariant 4).
func TestRateLimitEvaluator_StoreError(t *testing.T) {
	const sid = "sess-rl-err"
	// Use a fresh in-memory store and close it immediately to force a DB error.
	st, err := store.Open("file:rl_err_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	if err := st.CreateSession(sid, "test-agent", "fp"); err != nil {
		_ = st.Close()
		t.Fatalf("creating session: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("closing store: %v", err)
	}

	eval := &engine.RateLimitEvaluator{Store: st}
	pol := rlPolicy("search_web", 3, 60)
	call := rlCall(sid, "search_web", time.Now())

	_, evalErr := eval.Evaluate(context.Background(), call, rlSess(sid), pol)
	if evalErr == nil {
		t.Error("expected error from closed store; got nil — evaluator must propagate store errors for pipeline to fail closed")
	}
}

// TestRateLimitEvaluator_RequestedAtFallback verifies that a zero RequestedAt
// falls back to time.Now() without panicking or returning an error.
func TestRateLimitEvaluator_RequestedAtFallback(t *testing.T) {
	const sid = "sess-rl-fallback"
	st := rlStore(t, sid)
	eval := &engine.RateLimitEvaluator{Store: st}
	pol := rlPolicy("search_web", 3, 60)

	// Zero RequestedAt — should fall back to time.Now() and allow (no history).
	call := &engine.ToolCall{
		SessionID: sid,
		AgentName: "test-agent",
		ToolName:  "search_web",
		// RequestedAt intentionally left zero.
	}

	got, err := eval.Evaluate(context.Background(), call, rlSess(sid), pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Action != engine.Allow {
		t.Errorf("Action = %q, want allow (no history); Reason: %q", got.Action, got.Reason)
	}
}

// BenchmarkRateLimitEvaluator measures the evaluation cost against a session
// with 1000 historical events — the worst-case history depth operators are
// likely to encounter in production. Target: p99 < 2ms per CLAUDE.md §5.
//
// 995 events are for "other-tool" (excluded by the tool_name filter in SQL).
// 5 events are for "search_web" within the window (under the limit of 10).
// This represents the allow hot-path that dominates in production.
func BenchmarkRateLimitEvaluator(b *testing.B) {
	const sid = "bench-rl-session"
	st, err := store.Open("file:rlbench?mode=memory&cache=shared")
	if err != nil {
		b.Fatalf("opening bench store: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })

	if err := st.CreateSession(sid, "bench-agent", "bench-fp"); err != nil {
		b.Fatalf("creating bench session: %v", err)
	}

	now := time.Now()

	// Seed 995 events for "other-tool" and 5 for "search_web" within the
	// 60-second window. This exercises the common case where the rate-limited
	// tool is called infrequently while the session is otherwise active.
	for i := 0; i < 995; i++ {
		ev := &store.SessionEvent{
			SessionID:  sid,
			ToolName:   "other-tool",
			Decision:   "allow",
			RecordedAt: now.Add(-time.Duration(i+1) * time.Second).UnixNano(),
		}
		if err := st.AppendEvent(ev); err != nil {
			b.Fatalf("seeding other-tool event %d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		ev := &store.SessionEvent{
			SessionID:  sid,
			ToolName:   "search_web",
			Decision:   "allow",
			RecordedAt: now.Add(-time.Duration(i+1) * 5 * time.Second).UnixNano(),
		}
		if err := st.AppendEvent(ev); err != nil {
			b.Fatalf("seeding search_web event %d: %v", i, err)
		}
	}

	pol := &policy.Policy{
		EnforcementMode: policy.EnforcementBlock,
		MayUse:          []string{"search_web", "other-tool"},
		Tools: map[string]policy.ToolPolicy{
			"search_web": {
				RateLimit: &policy.RateLimitPolicy{
					MaxCalls:      10,
					WindowSeconds: 60,
				},
			},
		},
	}
	eval := &engine.RateLimitEvaluator{Store: st}
	sess := &session.Session{ID: sid, AgentName: "bench-agent"}
	call := &engine.ToolCall{
		SessionID:   sid,
		AgentName:   "bench-agent",
		ToolName:    "search_web",
		RequestedAt: now,
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
