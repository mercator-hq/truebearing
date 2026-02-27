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

	// RequestedAt is the wall-clock time the proxy received this request.
	RequestedAt time.Time
}
