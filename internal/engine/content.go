package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
)

// ContentEvaluator is the fifth stage in the evaluation pipeline, running
// between SequenceEvaluator and EscalationEvaluator. It enforces never_when
// content predicates declared in a tool's policy: argument-level conditions
// that deny the call based on the actual values present in the tool call's
// arguments JSON.
//
// Match modes (per ToolPolicy.NeverWhenMatch):
//   - "any" or absent: OR logic — the block fires if any single predicate matches.
//     This is the backward-compatible default.
//   - "all": AND logic — the block fires only when every predicate matches.
//
// Fail-closed behaviour (per CLAUDE.md §6 invariant 4):
//   - A predicate whose argument path is absent from the call arguments returns
//     an error (→ Deny). The policy author declared that this argument matters;
//     its absence is treated as a violation rather than a pass.
//   - An unrecognised operator returns an error (→ Deny). Operators are
//     validated by lint rule L014, so unrecognised operators at runtime
//     indicate a policy that bypassed linting.
//   - A contains_pattern value that fails regexp compilation returns an error
//     (→ Deny). The pattern is validated by lint rule L015.
//   - An unrecognised never_when_match value returns an error (→ Deny).
//     Valid values are validated by the linter at policy-authoring time.
//
// The evaluator is read-only per pipeline invariant 2: it never writes to
// the session, the policy, or the store.
type ContentEvaluator struct{}

// Evaluate checks the never_when predicates for the called tool using the
// match mode declared in never_when_match ("any" by default). If no predicates
// are configured for this tool, Allow is returned immediately.
func (e *ContentEvaluator) Evaluate(_ context.Context, call *ToolCall, _ *session.Session, pol *policy.Policy) (Decision, error) {
	tp, ok := pol.Tools[call.ToolName]
	if !ok || len(tp.NeverWhen) == 0 {
		// Fast path: no content predicates defined for this tool.
		return Decision{Action: Allow}, nil
	}

	switch tp.NeverWhenMatch {
	case "", policy.ContentMatchAny:
		return evalNeverWhenAny(call, tp)
	case policy.ContentMatchAll:
		return evalNeverWhenAll(call, tp)
	default:
		return Decision{}, fmt.Errorf(
			"unsupported never_when_match value %q for tool %q: supported values are %q and %q",
			tp.NeverWhenMatch, call.ToolName, policy.ContentMatchAny, policy.ContentMatchAll,
		)
	}
}

// evalNeverWhenAny implements OR logic: the block fires on the first predicate
// that matches. This is the default and is backward-compatible with policies
// that do not set never_when_match.
func evalNeverWhenAny(call *ToolCall, tp policy.ToolPolicy) (Decision, error) {
	for _, pred := range tp.NeverWhen {
		fired, err := evalContentPredicate(call, pred)
		if err != nil {
			return Decision{}, fmt.Errorf(
				"evaluating never_when predicate (argument=%q operator=%q) for tool %q: %w",
				pred.Argument, pred.Operator, call.ToolName, err,
			)
		}
		if fired {
			return Decision{
				Action: Deny,
				Reason: fmt.Sprintf(
					"never_when: argument %q %s %q",
					pred.Argument, pred.Operator, pred.Value,
				),
				RuleID: fmt.Sprintf("content.%s.%s", pred.Argument, pred.Operator),
				Feedback: &DenyFeedback{
					ReasonCode: "content_blocked",
					Suggestion: fmt.Sprintf(
						"Tool call was blocked by a content predicate: argument %q must not satisfy %s %q. Modify the argument value and retry.",
						pred.Argument, pred.Operator, pred.Value,
					),
				},
			}, nil
		}
	}
	return Decision{Action: Allow}, nil
}

// evalNeverWhenAll implements AND logic: the block fires only when every
// predicate in the never_when list matches simultaneously. A single
// non-matching predicate allows the call through.
func evalNeverWhenAll(call *ToolCall, tp policy.ToolPolicy) (Decision, error) {
	var reasons []string
	for _, pred := range tp.NeverWhen {
		fired, err := evalContentPredicate(call, pred)
		if err != nil {
			return Decision{}, fmt.Errorf(
				"evaluating never_when predicate (argument=%q operator=%q) for tool %q: %w",
				pred.Argument, pred.Operator, call.ToolName, err,
			)
		}
		if !fired {
			// AND short-circuit: one non-matching predicate means the block
			// does not fire regardless of what other predicates would return.
			return Decision{Action: Allow}, nil
		}
		reasons = append(reasons, fmt.Sprintf("argument %q %s %q", pred.Argument, pred.Operator, pred.Value))
	}

	// All predicates fired.
	return Decision{
		Action: Deny,
		Reason: fmt.Sprintf("never_when (all): all %d predicates matched: %s", len(reasons), strings.Join(reasons, "; ")),
		RuleID: "content.all_matched",
		Feedback: &DenyFeedback{
			ReasonCode: "content_blocked",
			Suggestion: fmt.Sprintf(
				"Tool call was blocked because all %d never_when conditions matched simultaneously. Modify the flagged argument values and retry.",
				len(reasons),
			),
		},
	}, nil
}

// evalContentPredicate evaluates a single ContentPredicate against the tool
// call's arguments. It returns true when the predicate fires (causing a Deny),
// or an error when evaluation is not possible (missing argument, bad operator,
// invalid regexp).
func evalContentPredicate(call *ToolCall, pred policy.ContentPredicate) (bool, error) {
	result := gjson.GetBytes(call.Arguments, pred.Argument)
	if !result.Exists() {
		// The argument declared in the predicate is absent from the call.
		// Fail closed: if we cannot inspect the argument we cannot confirm
		// the call is safe.
		return false, fmt.Errorf(
			"argument %q not found in tool call arguments (fail closed)",
			pred.Argument,
		)
	}

	argStr := result.String()

	switch pred.Operator {
	case "is_external":
		// is_external fires when argStr does NOT end with the configured
		// internal domain suffix. An empty Value makes HasSuffix vacuously
		// true, so the predicate is a no-op — operators should always set
		// Value for meaningful enforcement.
		return !strings.HasSuffix(argStr, pred.Value), nil

	case "contains_pattern":
		// Strip Perl/JS regexp delimiters (/pattern/) so the pitch YAML
		// style is accepted without a lint error. The linter (L015) strips
		// them identically before compiling, ensuring consistency.
		pattern := strings.TrimPrefix(pred.Value, "/")
		pattern = strings.TrimSuffix(pattern, "/")
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, fmt.Errorf("compiling contains_pattern regexp %q: %w", pred.Value, err)
		}
		return re.MatchString(argStr), nil

	case "equals":
		return argStr == pred.Value, nil

	case "not_equals":
		return argStr != pred.Value, nil

	default:
		return false, fmt.Errorf("unsupported never_when operator %q", pred.Operator)
	}
}
