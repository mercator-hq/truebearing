package engine

import (
	"context"
	"fmt"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// TaintEvaluator is the third stage in the evaluation pipeline. It enforces
// session taint rules: when the session taint flag is set, tools whose
// never_after list references any taint-applying tool are blocked.
//
// Taint is a session-level boolean flag. When a tool with taint.applies == true
// is called and allowed, Pipeline.Evaluate sets session.Tainted = true after
// the decision. When a tool with taint.clears == true is called and allowed,
// Pipeline.Evaluate sets session.Tainted = false. This evaluator is read-only
// per pipeline invariant 2 — it inspects but never mutates session or policy.
type TaintEvaluator struct{}

// Evaluate returns Allow when the session taint state does not block this tool,
// or Deny when the session is tainted and the tool is sensitive to that taint.
//
// Decision logic:
//  1. If session.Tainted == false, return Allow immediately — no active taint.
//  2. If the tool has taint.clears == true, return Allow — clearance tools must
//     pass through so the pipeline orchestrator can clear the taint flag.
//  3. Build the set of taint-applying tools defined in this policy.
//  4. If the tool's never_after list contains any taint-applying tool, return
//     Deny with RuleID "taint.session_tainted" — the active taint blocks it.
//  5. Otherwise return Allow — this tool is not sensitive to the current taint.
func (e *TaintEvaluator) Evaluate(_ context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) (Decision, error) {
	// Fast-path: no taint active in this session.
	if !sess.Tainted {
		return Decision{Action: Allow}, nil
	}

	// Clearance tools are permitted through while tainted so the pipeline
	// orchestrator can lower the taint flag after the allow decision.
	if tp, ok := pol.Tools[call.ToolName]; ok && tp.Taint.Clears {
		return Decision{Action: Allow}, nil
	}

	// Build the set of tools that apply taint in this policy. If there are
	// none, the session taint flag is stale (e.g. left over from a prior policy
	// version). In that case allow rather than block on undefined sources.
	taintSources := taintApplyingTools(pol)
	if len(taintSources) == 0 {
		return Decision{Action: Allow}, nil
	}

	// Check whether the current tool's never_after list references any
	// taint-applying tool. If so, the active session taint blocks this call.
	//
	// Design: this check mirrors what the sequence evaluator does for
	// never_after, but operates on the in-memory session.Tainted flag rather
	// than querying session_events history. The two mechanisms are complementary:
	// the taint flag is mutable (clearable), while session_events is immutable.
	// A tool cleared by taint.clears can unblock the next call via this
	// evaluator even though the event record of the taint-applying tool remains
	// in history and would still trigger the sequence never_after check.
	if tp, ok := pol.Tools[call.ToolName]; ok {
		for _, blocked := range tp.Sequence.NeverAfter {
			if taintSources[blocked] {
				return Decision{
					Action: Deny,
					Reason: fmt.Sprintf(
						"session is tainted: tool %q is blocked because %q (a taint-applying tool) appears in its never_after guard and the session taint flag is set",
						call.ToolName, blocked,
					),
					RuleID: "taint.session_tainted",
					Feedback: &DenyFeedback{
						ReasonCode: "taint_blocked",
						Suggestion: fmt.Sprintf("Session is tainted by a previous call to %q. Call the designated taint-clearing tool first, then retry %q.", blocked, call.ToolName),
					},
				}, nil
			}
		}
	}

	return Decision{Action: Allow}, nil
}

// taintApplyingTools returns the set of tool names that have taint.applies == true
// in pol. Callers use this to determine which tools contribute to the session
// taint that may block other tools via their never_after lists.
func taintApplyingTools(pol *policy.Policy) map[string]bool {
	result := make(map[string]bool)
	for name, tp := range pol.Tools {
		if tp.Taint.Applies {
			result[name] = true
		}
	}
	return result
}
