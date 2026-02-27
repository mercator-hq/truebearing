package engine

import (
	"context"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// Evaluator is the interface implemented by each stage in the evaluation
// pipeline. Evaluators are pure: they read call, sess, and pol but must
// never write to any of them. State mutations (taint changes, counter
// increments) are applied by the pipeline orchestrator after the decision
// is emitted, never inside an evaluator.
//
// An evaluator that cannot determine a safe outcome must return a non-nil
// error. The pipeline converts errors to Deny decisions — fail closed per
// CLAUDE.md §8 invariant 1.
type Evaluator interface {
	// Evaluate assesses whether the given tool call should be permitted,
	// given the current session state and policy. Returning Allow passes
	// control to the next evaluator in the pipeline. Any other action
	// terminates the pipeline immediately.
	//
	// Evaluators must not be aware of shadow mode. The pipeline applies
	// shadow conversion after receiving a non-Allow decision.
	Evaluate(ctx context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) (Decision, error)
}
