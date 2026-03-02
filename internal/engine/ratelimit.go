package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// RateLimitEvaluator is a stage in the evaluation pipeline. It enforces per-tool
// call frequency limits within a rolling time window. A tool that has been called
// at least max_calls times within the past window_seconds is denied until enough
// older calls advance past the window boundary.
//
// The evaluator is read-only per pipeline invariant 2: it queries session_events
// but never writes to the session, the policy, or the store.
//
// Design: the time reference for the window is call.RequestedAt, not time.Now().
// This allows simulate and replay pipelines to use the original trace timestamp
// as the reference point, producing rate-limit decisions consistent with the
// original session timeline rather than the current wall-clock time.
type RateLimitEvaluator struct {
	// Store is the data access layer used to count recent events for the tool.
	// Must be non-nil before any call to Evaluate.
	Store *store.Store
}

// Evaluate checks whether the current call would exceed the tool's configured
// rate limit. Returns Allow when no rate_limit is configured, or when the call
// count within the rolling window is below max_calls. Returns Deny with
// RuleID "rate_limit.<tool_name>" when the limit is reached or exceeded.
func (e *RateLimitEvaluator) Evaluate(_ context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) (Decision, error) {
	tp, ok := pol.Tools[call.ToolName]
	if !ok || tp.RateLimit == nil {
		// Fast path: no rate_limit configured for this tool.
		return Decision{Action: Allow}, nil
	}

	rl := tp.RateLimit

	// Use call.RequestedAt as the reference time so that simulate and replay
	// pipelines (which set RequestedAt from the original trace timestamp) produce
	// rate-limit decisions relative to the original call time rather than the
	// current wall clock. Fall back to time.Now() when RequestedAt is zero —
	// this should not happen in the proxy or test paths but guards against
	// a misconfigured caller.
	ref := call.RequestedAt
	if ref.IsZero() {
		ref = time.Now()
	}

	since := ref.Add(-time.Duration(rl.WindowSeconds) * time.Second)

	count, err := e.Store.CountSessionEventsSince(sess.ID, call.ToolName, since)
	if err != nil {
		return Decision{}, fmt.Errorf("checking rate limit for tool %q in session %q: %w", call.ToolName, sess.ID, err)
	}

	if count >= rl.MaxCalls {
		return Decision{
			Action: Deny,
			Reason: fmt.Sprintf(
				"rate_limit: %q has been called %d time(s) in the past %d second(s); limit is %d call(s) per window",
				call.ToolName, count, rl.WindowSeconds, rl.MaxCalls,
			),
			RuleID: fmt.Sprintf("rate_limit.%s", call.ToolName),
		}, nil
	}

	return Decision{Action: Allow}, nil
}
