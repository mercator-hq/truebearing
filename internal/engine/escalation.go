package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// EscalationEvaluator is the fifth and final stage in the evaluation pipeline.
// It triggers a human-review pause when the called tool's arguments satisfy the
// escalate_when condition defined in the policy. If a human has previously
// approved this exact call (matched by session, tool name, and argument hash),
// the evaluator returns Allow so the agent can proceed without re-escalating.
//
// Fail-closed behaviour (per CLAUDE.md §6 invariant 4):
//   - A missing or unresolvable argument path returns an error (→ Deny).
//   - An unsupported operator returns an error (→ Deny).
//   - A store read failure returns an error (→ Deny).
//
// The evaluator is read-only per pipeline invariant 2: it never writes to the
// session, the policy, or the escalations table. The escalation record is created
// by the proxy handler after it receives the Escalate decision.
type EscalationEvaluator struct {
	// Store is the data access layer used to check for prior human approvals.
	// Must be non-nil before any call to Evaluate.
	Store QueryBackend
}

// Evaluate checks whether the called tool's arguments satisfy the escalate_when
// condition. Returns:
//   - Allow if the tool has no escalate_when rule.
//   - Allow if the rule condition is not satisfied.
//   - Allow if the rule is satisfied AND a prior human approval exists for this
//     session + tool + argument hash combination.
//   - Escalate if the rule is satisfied and no prior approval exists.
//   - error (→ Deny via pipeline) if the argument path is missing, the operator
//     is unsupported, or the store query fails.
func (e *EscalationEvaluator) Evaluate(_ context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) (Decision, error) {
	tp, ok := pol.Tools[call.ToolName]
	if !ok || tp.EscalateWhen == nil {
		// Fast path: no escalation rule for this tool.
		return Decision{Action: Allow}, nil
	}

	rule := tp.EscalateWhen

	// Normalise the argument path from JSONPath style ($.field.sub) to gjson
	// dot notation (field.sub). The policy DSL uses JSONPath's $. prefix for
	// readability; gjson does not use the $ sigil.
	path := strings.TrimPrefix(rule.ArgumentPath, "$.")
	path = strings.TrimPrefix(path, "$")
	if path == "" {
		return Decision{}, fmt.Errorf(
			"escalate_when.argument_path %q for tool %q resolves to an empty gjson path",
			rule.ArgumentPath, call.ToolName,
		)
	}

	result := gjson.GetBytes(call.Arguments, path)
	if !result.Exists() {
		// The argument path was not found in the call's arguments. Fail closed:
		// if we cannot evaluate the condition we cannot allow the call.
		return Decision{}, fmt.Errorf(
			"escalate_when.argument_path %q not found in arguments for tool %q (fail closed)",
			rule.ArgumentPath, call.ToolName,
		)
	}

	triggered, err := applyEscalationOperator(result, rule.Operator, rule.Value)
	if err != nil {
		return Decision{}, fmt.Errorf("evaluating escalate_when for tool %q: %w", call.ToolName, err)
	}

	if !triggered {
		return Decision{Action: Allow}, nil
	}

	// Escalation condition is satisfied. Check whether a human previously
	// approved this exact call. The argument hash ties the approval to the
	// specific payload so that approving a $15,000 wire does not implicitly
	// approve a $50,000 wire with different arguments.
	h := sha256.Sum256(call.Arguments)
	argumentsHash := hex.EncodeToString(h[:])

	approved, err := e.Store.HasApprovedEscalation(sess.ID, call.ToolName, argumentsHash)
	if err != nil {
		return Decision{}, fmt.Errorf(
			"checking approved escalation for session %q tool %q: %w",
			sess.ID, call.ToolName, err,
		)
	}

	if approved {
		return Decision{Action: Allow}, nil
	}

	return Decision{
		Action: Escalate,
		Reason: fmt.Sprintf(
			"escalate_when: argument at %q is %s %v",
			rule.ArgumentPath, rule.Operator, rule.Value,
		),
		RuleID: "escalation",
		Feedback: &DenyFeedback{
			ReasonCode: "escalation_pending",
			Suggestion: "This action requires human approval. Poll check_escalation_status with the returned escalation_id to retry once an operator approves.",
		},
	}, nil
}

// applyEscalationOperator evaluates `actualValue <operator> threshold` and reports
// whether the escalation condition is satisfied. Numeric operators (>, <, >=, <=, ==, !=)
// compare the floating-point representation of actualValue against threshold. String
// operators (contains, matches) operate on the string representation of actualValue.
// An unsupported operator returns an error so the pipeline fails closed.
func applyEscalationOperator(actualValue gjson.Result, operator string, threshold interface{}) (bool, error) {
	switch operator {
	case ">", "<", ">=", "<=", "==", "!=":
		return applyNumericOp(actualValue.Float(), operator, toFloat64(threshold))
	case "contains":
		return strings.Contains(actualValue.String(), fmt.Sprintf("%v", threshold)), nil
	case "matches":
		pattern := fmt.Sprintf("%v", threshold)
		matched, err := regexp.MatchString(pattern, actualValue.String())
		if err != nil {
			return false, fmt.Errorf("compiling regexp pattern %q: %w", pattern, err)
		}
		return matched, nil
	default:
		return false, fmt.Errorf("unsupported escalate_when operator %q", operator)
	}
}

// applyNumericOp compares actual against threshold using a numeric operator.
// The switch is exhaustive for the six valid numeric operators; the default
// branch is unreachable because the caller validates the operator set first.
func applyNumericOp(actual float64, operator string, threshold float64) (bool, error) {
	switch operator {
	case ">":
		return actual > threshold, nil
	case "<":
		return actual < threshold, nil
	case ">=":
		return actual >= threshold, nil
	case "<=":
		return actual <= threshold, nil
	case "==":
		return actual == threshold, nil
	case "!=":
		return actual != threshold, nil
	default:
		return false, fmt.Errorf("unreachable: unknown numeric operator %q", operator)
	}
}

// toFloat64 converts a YAML-parsed numeric threshold to float64 for comparison.
// yaml.v3 unmarshals integer literals as int and floating-point literals as
// float64. Both are valid threshold types in the policy DSL.
func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float32:
		return float64(n)
	default:
		// In practice this path is unreachable for well-formed policy YAML.
		// Return 0 so the comparison is deterministic; the condition will
		// almost certainly be false, which is the safer of the two outcomes.
		return 0
	}
}
