package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// SequenceEvaluator is the fourth stage in the evaluation pipeline. It enforces
// sequence predicates declared in a tool's policy: only_after, never_after, and
// requires_prior_n. All three predicates are evaluated against the session's
// allowed-event history; denied and escalated calls are excluded because they
// were never executed upstream and must not contribute to sequence state.
//
// The evaluator requires a non-nil QueryBackend to query session_events. It is
// read-only per pipeline invariant 2: it queries history but never writes to
// the session or policy.
type SequenceEvaluator struct {
	// Store is the data access layer used to fetch the session's event history.
	// Must be non-nil before any call to Evaluate.
	Store QueryBackend
}

// Evaluate checks all three sequence predicates for the called tool and returns
// Allow if all predicates pass, or Deny with a reason listing every violated
// predicate.
//
// Design: we collect ALL sequence violations before returning rather than
// short-circuiting on the first failure. Operators debugging a policy need to
// see every violated predicate in a single request, not discover them one at a
// time over multiple attempts. See also mvp-plan.md §8.5.
//
// Design: ORDER BY seq is performed in SQL via GetSessionEvents rather than in
// Go. The composite primary key index on (session_id, seq) makes this sort
// effectively free — SQLite reads the B-tree in key order. Sorting in Go would
// add unnecessary O(n log n) work in this hot path with large session histories.
func (e *SequenceEvaluator) Evaluate(_ context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) (Decision, error) {
	tp, ok := pol.Tools[call.ToolName]
	if !ok {
		// Tool has no ToolPolicy entry; no sequence predicates to check.
		return Decision{Action: Allow}, nil
	}

	seq := tp.Sequence

	// Fast path: no sequence predicates defined for this tool.
	if len(seq.OnlyAfter) == 0 && len(seq.NeverAfter) == 0 && seq.RequiresPriorN == nil {
		return Decision{Action: Allow}, nil
	}

	// Fetch the session's full event history, ordered by seq ASC. Only events
	// with decision "allow" or "shadow_deny" count toward sequence state. Denied
	// calls were blocked before reaching the upstream server and must not
	// satisfy sequence predicates.
	events, err := e.Store.GetSessionEvents(sess.ID)
	if err != nil {
		return Decision{}, fmt.Errorf("fetching session events for sequence check on session %q: %w", sess.ID, err)
	}

	// Build per-tool frequency counts from allowed events in a single pass.
	// A single traversal covers all three predicates without additional loops.
	type freq struct {
		seen  bool
		count int
	}
	history := make(map[string]freq, len(events))
	for _, ev := range events {
		if ev.Decision != string(Allow) && ev.Decision != string(ShadowDeny) {
			continue
		}
		f := history[ev.ToolName]
		f.seen = true
		f.count++
		history[ev.ToolName] = f
	}

	var violations []string

	// only_after: every listed tool must appear at least once in history.
	for _, required := range seq.OnlyAfter {
		if !history[required].seen {
			violations = append(violations, fmt.Sprintf(
				"sequence.only_after: %q has not been called in this session (required before %q)",
				required, call.ToolName,
			))
		}
	}

	// never_after: none of the listed tools may appear anywhere in history.
	for _, forbidden := range seq.NeverAfter {
		if history[forbidden].seen {
			violations = append(violations, fmt.Sprintf(
				"sequence.never_after: %q was already called in this session and permanently blocks %q",
				forbidden, call.ToolName,
			))
		}
	}

	// requires_prior_n: the named tool must appear at least N times in history.
	if seq.RequiresPriorN != nil {
		count := history[seq.RequiresPriorN.Tool].count
		if count < seq.RequiresPriorN.Count {
			violations = append(violations, fmt.Sprintf(
				"sequence.requires_prior_n: %q must be called at least %d time(s) before %q (%d call(s) recorded)",
				seq.RequiresPriorN.Tool, seq.RequiresPriorN.Count, call.ToolName, count,
			))
		}
	}

	if len(violations) == 0 {
		return Decision{Action: Allow}, nil
	}

	return Decision{
		Action: Deny,
		Reason: strings.Join(violations, "; "),
		RuleID: "sequence",
	}, nil
}
