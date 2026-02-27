package engine

import (
	"context"
	"fmt"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// virtualEscalationTool is the name TrueBearing injects into every agent's
// tool schema to allow polling for escalation decisions. It is handled
// entirely by the proxy and must never reach the upstream MCP server. The
// may_use check permits it unconditionally so operators do not need to declare
// it in every policy.
const virtualEscalationTool = "check_escalation_status"

// MayUseEvaluator is the first stage in the evaluation pipeline. It verifies
// that the requested tool appears in the policy's may_use whitelist before any
// other check runs. A tool absent from may_use is denied immediately; no
// subsequent evaluators are consulted.
//
// The virtual tool check_escalation_status is always permitted regardless of
// the may_use list because it is synthesized by TrueBearing itself and managed
// entirely by the proxy — operators never need to declare it explicitly.
type MayUseEvaluator struct{}

// Evaluate returns Allow if call.ToolName is listed in pol.MayUse or is the
// check_escalation_status virtual tool. It returns Deny with RuleID "may_use"
// for any other tool name.
func (e *MayUseEvaluator) Evaluate(_ context.Context, call *ToolCall, _ *session.Session, pol *policy.Policy) (Decision, error) {
	// The virtual escalation-status tool is always permitted. It is synthesized
	// by TrueBearing and never forwarded upstream; the may_use list in operator
	// policies does not need to declare it.
	if call.ToolName == virtualEscalationTool {
		return Decision{Action: Allow}, nil
	}

	// Design: linear scan over may_use is intentional. The list is small in
	// practice (typically < 50 entries) and is already a parsed []string from
	// the policy struct — building a map per call would cost more than the scan.
	// If profiling reveals this as a bottleneck at very large may_use lists,
	// the policy parser can pre-build a set once at parse time (Task 2.1 area).
	for _, allowed := range pol.MayUse {
		if call.ToolName == allowed {
			return Decision{Action: Allow}, nil
		}
	}

	return Decision{
		Action: Deny,
		Reason: fmt.Sprintf("tool %q is not in the policy may_use list", call.ToolName),
		RuleID: "may_use",
	}, nil
}
