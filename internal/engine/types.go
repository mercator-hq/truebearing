package engine

import (
	"encoding/json"
	"time"
)

// Action is the enforcement outcome produced by the evaluation pipeline.
type Action string

const (
	// Allow permits the tool call to proceed to the upstream MCP server.
	Allow Action = "allow"

	// Deny blocks the tool call and returns a synthetic JSON-RPC error
	// response to the caller. The call is not forwarded upstream.
	Deny Action = "deny"

	// Escalate pauses the tool call by creating a pending human-review
	// escalation record. The caller receives a synthetic "escalated" response
	// and must poll check_escalation_status for resolution. The call is not
	// forwarded upstream until an operator approves the escalation.
	Escalate Action = "escalate"

	// ShadowDeny records a policy violation in the audit log but allows the
	// call to proceed to the upstream server. ShadowDeny is produced
	// exclusively by the pipeline when the effective enforcement mode is
	// shadow; evaluators never return ShadowDeny directly.
	ShadowDeny Action = "shadow_deny"
)

// DenyFeedback carries machine-readable context for a Deny decision.
// It is embedded in the JSON-RPC error response's data field so LLM agents
// can parse the denial reason and automatically retry with the correct
// prerequisites satisfied — the "self-repair" capability described in the
// competitive positioning doc.
//
// Invariant: Feedback is non-nil for every Deny decision produced by a policy
// evaluator. It is nil for Deny decisions created by the pipeline itself when
// an evaluator returns a non-nil error (fail-closed path), since those
// originate from internal errors, not actionable policy violations.
type DenyFeedback struct {
	// ReasonCode is a stable machine-readable string identifying the denial
	// category. One code per evaluator:
	//   may_use_denied            — MayUseEvaluator
	//   budget_exceeded           — BudgetEvaluator
	//   taint_blocked             — TaintEvaluator
	//   sequence_only_after       — SequenceEvaluator (only_after violation)
	//   sequence_never_after      — SequenceEvaluator (never_after violation)
	//   sequence_requires_prior_n — SequenceEvaluator (requires_prior_n violation)
	//   delegation_exceeded       — DelegationEvaluator
	//   rate_limit_exceeded       — RateLimitEvaluator
	//   content_blocked           — ContentEvaluator
	//   env_mismatch              — EnvEvaluator
	//   escalation_pending        — EscalationEvaluator
	ReasonCode string `json:"reason_code"`

	// UnsatisfiedPrerequisites lists the tool names that must be called before
	// the blocked tool can succeed. Non-empty only for sequence_only_after and
	// sequence_requires_prior_n denials. The agent should call each listed tool
	// (in any order) before retrying the blocked tool.
	UnsatisfiedPrerequisites []string `json:"unsatisfied_prerequisites,omitempty"`

	// Suggestion is a plain-English sentence the agent can inject into its own
	// context as a system message to guide a retry. It is phrased as an
	// actionable instruction rather than a description of the violation.
	Suggestion string `json:"suggestion"`
}

// Decision is the output of a single pipeline evaluation. It carries the
// enforcement outcome and the policy rule that produced it.
type Decision struct {
	// Action is the enforcement outcome for this call.
	Action Action

	// Reason is a human-readable explanation of the decision, included in the
	// audit record and returned to callers on deny or escalate.
	Reason string

	// RuleID identifies the specific policy rule that triggered a non-allow
	// decision (e.g. "may_use", "budget.max_tool_calls", "sequence.only_after").
	// Empty for Allow decisions.
	RuleID string

	// Feedback carries structured machine-readable context for a Deny decision.
	// It is non-nil for Deny decisions produced by policy evaluators and nil for
	// all other decision actions or for error-converted denials.
	Feedback *DenyFeedback
}

// ToolCall is the engine's internal representation of an intercepted MCP
// tools/call request. It is constructed by the proxy and passed unchanged
// through every evaluator in the pipeline.
//
// Design: the plan's ArgumentsMap field (map[string]interface{}) was omitted.
// CLAUDE.md §12 prohibits interface{} in the evaluation pipeline. Evaluators
// that need structured argument access use gjson directly on Arguments, which
// is faster and avoids a full unmarshal on every call.
type ToolCall struct {
	// SessionID is the value of the X-TrueBearing-Session-ID request header.
	SessionID string

	// AgentName is the "agent" claim from the validated JWT.
	AgentName string

	// ToolName is the "name" field from the MCP tools/call params.
	ToolName string

	// Arguments is the raw JSON of the tool call's "arguments" object.
	// Evaluators that need to inspect arguments should use gjson to extract
	// specific paths rather than unmarshal the full structure.
	Arguments json.RawMessage

	// AgentEnv is the "env" claim from the validated JWT, populated by the proxy
	// at request time. Empty when the agent was registered without --env. The
	// EnvEvaluator compares this against policy.Session.RequireEnv; a mismatch
	// produces a Deny with RuleID "env.mismatch".
	AgentEnv string

	// ParentAgent is the "parent_agent" claim from the validated JWT. Empty for
	// root agents (registered without --parent). The DelegationEvaluator uses
	// this to load the parent's allowed tool set from the agents table and verify
	// the child cannot call tools outside the parent's scope.
	ParentAgent string

	// RequestedAt is the wall-clock time the proxy received this request.
	RequestedAt time.Time
}
