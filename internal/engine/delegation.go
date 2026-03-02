package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// DelegationEvaluator enforces that child agents cannot exceed the tool
// permissions of their parent. When an agent is registered with --parent,
// its JWT carries a parent_agent claim. On every tool call, this evaluator
// loads the parent agent's current allowed_tools from the agents table and
// verifies that the requested tool is within the parent's scope.
//
// Root agents (no parent_agent claim in the JWT) always pass this evaluator,
// because there is no delegation constraint to enforce.
//
// Fail-closed behaviour (CLAUDE.md §6 invariant 4):
//   - If the parent agent is not found in the agents table, the call is denied.
//   - If the agents table query fails, an error is returned (→ Deny via pipeline).
//   - If the parent's allowed_tools JSON is malformed, an error is returned.
//
// The evaluator is read-only per pipeline invariant 2: it never writes to the
// session, the policy, or the store.
type DelegationEvaluator struct {
	// Store is the data access layer used to load the parent agent's current
	// allowed tool list. Must be non-nil before any call to Evaluate.
	Store QueryBackend
}

// Evaluate returns Allow for root agents (call.ParentAgent == ""). For child
// agents, it loads the parent's current allowed_tools from the agents table
// and returns Allow if call.ToolName is within the parent's scope. Returns
// Deny with RuleID "delegation.exceeds_parent" when the child attempts to
// call a tool not present in the parent's allowed set.
//
// Design: the parent's tools are loaded from the agents table on every call
// rather than using the ParentAllowed list embedded in the child's JWT. This
// ensures that if the parent agent is re-registered with a narrower tool set,
// the change takes effect immediately for all child agents without requiring
// child credential renewal.
func (e *DelegationEvaluator) Evaluate(_ context.Context, call *ToolCall, _ *session.Session, _ *policy.Policy) (Decision, error) {
	if call.ParentAgent == "" {
		// Fast path: root agent has no delegation constraint to enforce.
		return Decision{Action: Allow}, nil
	}

	parentTools, err := e.Store.GetAgentAllowedTools(call.ParentAgent)
	if err != nil {
		if errors.Is(err, ErrParentAgentNotFound) {
			return Decision{}, fmt.Errorf(
				"parent agent %q not found in agents table; child agent %q cannot be authorised",
				call.ParentAgent, call.AgentName,
			)
		}
		return Decision{}, fmt.Errorf(
			"loading parent agent %q for delegation check on behalf of %q: %w",
			call.ParentAgent, call.AgentName, err,
		)
	}

	// Design: linear scan over parentTools is intentional. The list is small in
	// practice (typically < 50 entries) and building a map per call costs more
	// than the scan for typical tool sets. The MayUseEvaluator uses the same
	// pattern. If profiling reveals this as a bottleneck, a pre-built set keyed
	// by agent name can be cached at evaluator construction time.
	for _, t := range parentTools {
		if t == call.ToolName {
			return Decision{Action: Allow}, nil
		}
	}

	return Decision{
		Action: Deny,
		Reason: fmt.Sprintf(
			"delegation.exceeds_parent: tool %q is not in parent agent %q's allowed tool set",
			call.ToolName, call.ParentAgent,
		),
		RuleID: "delegation.exceeds_parent",
	}, nil
}
