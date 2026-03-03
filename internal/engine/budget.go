package engine

import (
	"context"
	"fmt"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// BudgetEvaluator is the second stage in the evaluation pipeline. It compares
// the session's accumulated tool call count and estimated cost against the
// limits configured in the policy's budget block. A zero-valued BudgetPolicy
// (both fields are zero) means no limits are configured; the evaluator allows
// immediately without inspection.
//
// Budget checks run after MayUse so that denied tools (which cost nothing) do
// not consume budget headroom. State mutations (incrementing ToolCallCount and
// EstimatedCostUSD) are applied by the pipeline orchestrator after the
// decision; the evaluator is read-only per pipeline invariant 2.
type BudgetEvaluator struct{}

// Evaluate returns Allow when the session is within budget, or Deny when
// either the tool call count or the estimated cost limit has been reached.
//
// Decision logic:
//  1. If Budget is zero-valued (both MaxToolCalls == 0 and MaxCostUSD == 0.0),
//     return Allow immediately — no limits are configured.
//  2. Check the call count limit first (it is the cheaper comparison).
//  3. Check the cost limit second.
//  4. If both limits are exceeded, the call count denial fires (first wins)
//     and the reason message names both violations so operators can see the
//     full picture in a single audit record.
func (e *BudgetEvaluator) Evaluate(_ context.Context, _ *ToolCall, sess *session.Session, pol *policy.Policy) (Decision, error) {
	b := pol.Budget

	// Fast-path: no budget block configured in the policy. The linter warns
	// about this via L006 but it is not an error — allow immediately.
	if b.MaxToolCalls == 0 && b.MaxCostUSD == 0 {
		return Decision{Action: Allow}, nil
	}

	callsExceeded := b.MaxToolCalls > 0 && sess.ToolCallCount >= b.MaxToolCalls
	costExceeded := b.MaxCostUSD > 0 && sess.EstimatedCostUSD >= b.MaxCostUSD

	// Design: when both limits are exceeded simultaneously, we report the tool
	// call violation first (matching pipeline evaluation order) and embed both
	// values in the reason message. This gives operators the full picture in a
	// single audit record rather than hiding the cost violation behind the call
	// count denial.
	if callsExceeded && costExceeded {
		return Decision{
			Action: Deny,
			Reason: fmt.Sprintf(
				"session budget exceeded: tool call limit (%d/%d calls) and cost limit ($%.4f/$%.4f USD) both reached",
				sess.ToolCallCount, b.MaxToolCalls,
				sess.EstimatedCostUSD, b.MaxCostUSD,
			),
			RuleID: "budget.max_tool_calls",
			Feedback: &DenyFeedback{
				ReasonCode: "budget_exceeded",
				Suggestion: "Session budget limits have been reached. Start a new session or contact the operator to increase the configured limits.",
			},
		}, nil
	}

	if callsExceeded {
		return Decision{
			Action: Deny,
			Reason: fmt.Sprintf(
				"session tool call limit reached: %d of %d calls used",
				sess.ToolCallCount, b.MaxToolCalls,
			),
			RuleID: "budget.max_tool_calls",
			Feedback: &DenyFeedback{
				ReasonCode: "budget_exceeded",
				Suggestion: "Session tool call budget has been reached. Start a new session or contact the operator to increase the configured limit.",
			},
		}, nil
	}

	if costExceeded {
		return Decision{
			Action: Deny,
			Reason: fmt.Sprintf(
				"session cost limit reached: $%.4f of $%.4f USD used",
				sess.EstimatedCostUSD, b.MaxCostUSD,
			),
			RuleID: "budget.max_cost_usd",
			Feedback: &DenyFeedback{
				ReasonCode: "budget_exceeded",
				Suggestion: "Session cost budget has been reached. Start a new session or contact the operator to increase the configured limit.",
			},
		}, nil
	}

	return Decision{Action: Allow}, nil
}
