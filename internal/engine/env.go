package engine

import (
	"context"
	"fmt"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// EnvEvaluator is the first stage in the evaluation pipeline. It enforces the
// session-level require_env predicate: if the policy declares a required
// deployment environment (e.g. "production"), only agents whose JWT carries a
// matching "env" claim may execute tool calls in that session.
//
// This is a session-level constraint, not a per-tool constraint. A mismatch
// terminates the pipeline immediately before any tool-specific checks run,
// because the wrong-environment agent has no business executing any tool in
// this session regardless of which tool is being called.
//
// Fail-closed behaviour (CLAUDE.md §6 invariant 4):
//   - If require_env is set and call.AgentEnv is empty (agent registered
//     without --env), the call is denied. An agent without an env claim cannot
//     satisfy an environment requirement.
//   - The comparison is case-sensitive and exact. "Production" does not match
//     "production". Operators must use consistent casing when registering
//     agents and authoring policies.
//
// The evaluator is read-only per pipeline invariant 2: it never writes to
// the session, the policy, or the store.
type EnvEvaluator struct{}

// Evaluate checks the agent's environment claim against the policy's
// require_env field. Returns Allow when the field is unset (no restriction)
// or when the agent's env claim matches exactly. Returns Deny with
// RuleID "env.mismatch" on any mismatch.
func (e *EnvEvaluator) Evaluate(_ context.Context, call *ToolCall, _ *session.Session, pol *policy.Policy) (Decision, error) {
	required := pol.Session.RequireEnv
	if required == "" {
		// Fast path: no environment restriction configured. All agents pass.
		return Decision{Action: Allow}, nil
	}

	if call.AgentEnv == required {
		return Decision{Action: Allow}, nil
	}

	// Design: we include both the agent's env claim and the required value in
	// the deny reason so operators can diagnose mismatches without querying the
	// agents table. An empty call.AgentEnv indicates the agent was registered
	// without --env; the message distinguishes this from a wrong-env case.
	agentEnvDisplay := call.AgentEnv
	if agentEnvDisplay == "" {
		agentEnvDisplay = "(none — agent registered without --env)"
	}
	return Decision{
		Action: Deny,
		Reason: fmt.Sprintf(
			"require_env: policy requires environment %q but agent env is %s",
			required, agentEnvDisplay,
		),
		RuleID: "env.mismatch",
		Feedback: &DenyFeedback{
			ReasonCode: "env_mismatch",
			Suggestion: fmt.Sprintf("This policy requires an agent registered with --env %q. Re-register the agent with the correct environment flag and retry.", required),
		},
	}, nil
}
