package engine

import (
	"context"
	"fmt"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// Pipeline is an ordered chain of Evaluators. The pipeline enforces the five
// invariants documented in doc.go. It is safe for concurrent calls to Evaluate
// after construction; the stages slice is never modified after New returns.
type Pipeline struct {
	stages []Evaluator
}

// New constructs a Pipeline from the given evaluators, which run in the order
// they appear in stages. An empty stages list is valid and produces an Allow
// decision on every call.
func New(stages ...Evaluator) *Pipeline {
	return &Pipeline{stages: stages}
}

// Evaluate runs each evaluator in order and returns the effective Decision for
// the given tool call. The five pipeline invariants (documented in doc.go) are
// enforced here:
//
//  1. The caller is responsible for writing exactly one audit record from the
//     returned Decision. Evaluate itself never writes audit records.
//  2. Evaluators receive sess and pol as read-only inputs; they must not
//     mutate either. Taint state mutations are applied by this method after the
//     Allow decision is reached, per invariant 2.
//  3. The first non-Allow decision terminates the pipeline immediately.
//  4. A non-nil error from any evaluator produces a Deny decision; the error
//     is embedded in Reason and is never returned as a Go error to the caller.
//  5. Shadow mode conversion is applied here. Evaluators always return Deny or
//     Escalate; this method converts them to ShadowDeny when the effective
//     enforcement mode for the tool is shadow.
func (p *Pipeline) Evaluate(ctx context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) Decision {
	for _, ev := range p.stages {
		d, err := ev.Evaluate(ctx, call, sess, pol)
		if err != nil {
			// Invariant 4: evaluation errors fail closed. The error text is
			// captured in Reason for the audit record; it is not propagated
			// to the caller as a Go error.
			return Decision{
				Action: Deny,
				Reason: fmt.Sprintf("evaluator error: %v", err),
				RuleID: "internal_error",
			}
		}
		if d.Action != Allow {
			// Invariant 3: first failure terminates the pipeline.
			// Invariant 5: apply shadow mode at this level, not in evaluators.
			if effectiveMode(pol, call.ToolName) == policy.EnforcementShadow {
				d.Action = ShadowDeny
			}
			return d
		}
	}

	// All evaluators allowed the call. Apply taint state mutations before
	// returning. Mutations are applied here (the pipeline orchestrator) rather
	// than inside any evaluator, satisfying invariant 2. The call that applies
	// taint is itself allowed; the taint takes effect for subsequent calls.
	applyTaintMutations(call, sess, pol)

	return Decision{Action: Allow}
}

// applyTaintMutations updates sess.Tainted based on the called tool's taint
// policy after a successful Allow decision. It is called exclusively by
// Pipeline.Evaluate and is the only site in the engine that mutates session
// state.
//
// Design: clears is applied before applies so that a tool with both flags set
// (which the linter does not currently flag as an error) results in a tainted
// session — "applies" wins as the more restrictive outcome.
func applyTaintMutations(call *ToolCall, sess *session.Session, pol *policy.Policy) {
	tp, ok := pol.Tools[call.ToolName]
	if !ok {
		return
	}
	if tp.Taint.Clears && sess.Tainted {
		sess.Tainted = false
	}
	if tp.Taint.Applies {
		sess.Tainted = true
	}
}

// effectiveMode resolves the enforcement mode for a specific tool, honouring
// the tool-level override hierarchy defined in mvp-plan.md §12.
//
// Hierarchy (tool-level wins when set):
//
//	global=shadow, tool=none   → shadow
//	global=shadow, tool=block  → block
//	global=block,  tool=none   → block
//	global=block,  tool=shadow → shadow
func effectiveMode(pol *policy.Policy, toolName string) policy.EnforcementMode {
	if tp, ok := pol.Tools[toolName]; ok && tp.EnforcementMode != "" {
		return tp.EnforcementMode
	}
	return pol.EnforcementMode
}
