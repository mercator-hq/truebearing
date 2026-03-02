//go:build js || wasip1

// Package main is the WASM entry point for the TrueBearing policy engine.
// It exposes a stateless evaluate function that accepts all required context
// as JSON, runs the full evaluation pipeline in-process, and returns a
// decision JSON blob — no SQLite, no HTTP, no sidecar.
//
// Two build variants are provided:
//   - main_js.go   (GOOS=js GOARCH=wasm)   — Node.js via wasm_exec.js
//   - main_wasi.go (GOOS=wasip1 GOARCH=wasm) — WASI stdin/stdout JSON loop
//
// Invariant: evaluate is pure. It does not persist any state between calls.
// The caller is responsible for maintaining session state (events, taint,
// counters) and passing the complete state on each call.
package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// wasmSessionState carries all per-session state needed for a single
// evaluation. The caller must serialize the current session state to this
// type before each call. RecordedAt values in events are unix nanoseconds.
type wasmSessionState struct {
	Session             session.Session                  `json:"session"`
	Events              []engine.SessionEventEntry       `json:"events"`
	ApprovedEscalations []engine.ApprovedEscalationEntry `json:"approved_escalations,omitempty"`
	// ParentTools is the list of tool names that the calling agent's parent
	// is allowed to call. Only required for child agents (those with a
	// non-empty ParentAgent field in the tool call). Leave nil or empty for
	// root agents.
	ParentTools []string `json:"parent_tools,omitempty"`
}

// wasmCallInput is the serialised form of engine.ToolCall used as WASM input.
type wasmCallInput struct {
	SessionID   string          `json:"session_id"`
	AgentName   string          `json:"agent_name"`
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	AgentEnv    string          `json:"agent_env,omitempty"`
	ParentAgent string          `json:"parent_agent,omitempty"`
	RequestedAt time.Time       `json:"requested_at"`
}

// wasmDecision is the serialised form of engine.Decision returned by evaluate.
type wasmDecision struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
	RuleID string `json:"rule_id"`
}

// evaluateCore is the shared, target-agnostic implementation invoked by both
// the js/wasm and wasip1 entry points. All inputs and outputs are JSON bytes
// to keep the calling convention identical across WASM hosts.
//
// Error conditions (malformed JSON, missing required fields) produce a Deny
// decision with a descriptive reason string. The function never panics and
// never returns a non-nil Go error — all failures are expressed as deny
// decisions so the WASM host always receives a usable response.
func evaluateCore(policyJSON, sessionJSON, callJSON []byte) []byte {
	// Parse policy. The policy arrives as JSON because the WASM caller
	// typically has the policy stored as JSON (via json.Marshal of policy.Policy)
	// or can convert from YAML before calling evaluate.
	var pol policy.Policy
	if err := json.Unmarshal(policyJSON, &pol); err != nil {
		return encodeDeny("invalid policy JSON: " + err.Error())
	}

	// Parse session state (session snapshot + event history + escalation approvals).
	var ss wasmSessionState
	if err := json.Unmarshal(sessionJSON, &ss); err != nil {
		return encodeDeny("invalid session JSON: " + err.Error())
	}

	// Parse the tool call.
	var ci wasmCallInput
	if err := json.Unmarshal(callJSON, &ci); err != nil {
		return encodeDeny("invalid call JSON: " + err.Error())
	}

	// Construct an in-memory backend from the pre-loaded session state.
	// No SQLite, no network: the backend is entirely self-contained.
	backend := engine.NewMemBackend(ss.Events, ss.ApprovedEscalations, ss.ParentTools)

	// Build the canonical pipeline in the same order used by the live proxy.
	// EnvEvaluator and MayUseEvaluator require no backend; the other four
	// evaluators use the MemBackend for read-only history queries.
	pip := engine.New(
		&engine.EnvEvaluator{},
		&engine.MayUseEvaluator{},
		&engine.DelegationEvaluator{Store: backend},
		&engine.BudgetEvaluator{},
		&engine.TaintEvaluator{},
		&engine.SequenceEvaluator{Store: backend},
		&engine.ContentEvaluator{},
		&engine.RateLimitEvaluator{Store: backend},
		&engine.EscalationEvaluator{Store: backend},
	)

	call := &engine.ToolCall{
		SessionID:   ci.SessionID,
		AgentName:   ci.AgentName,
		ToolName:    ci.ToolName,
		Arguments:   ci.Arguments,
		AgentEnv:    ci.AgentEnv,
		ParentAgent: ci.ParentAgent,
		RequestedAt: ci.RequestedAt,
	}

	decision := pip.Evaluate(context.Background(), call, &ss.Session, &pol)

	result, _ := json.Marshal(wasmDecision{
		Action: string(decision.Action),
		Reason: decision.Reason,
		RuleID: decision.RuleID,
	})
	return result
}

// encodeDeny returns a JSON-encoded Deny decision with the given reason.
// Used for input validation failures before the pipeline can run.
func encodeDeny(reason string) []byte {
	result, _ := json.Marshal(wasmDecision{
		Action: string(engine.Deny),
		Reason: reason,
		RuleID: "wasm_input_error",
	})
	return result
}
