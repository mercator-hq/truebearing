package policy

import (
	"fmt"
	"regexp"
	"strings"
)

// Severity represents the urgency level of a lint diagnostic.
type Severity string

const (
	// SeverityError indicates a correctness problem that will cause unexpected
	// evaluation behaviour or runtime failures in the pipeline.
	SeverityError Severity = "ERROR"

	// SeverityWarning indicates a likely misconfiguration or omission that may
	// cause unintended evaluation behaviour.
	SeverityWarning Severity = "WARNING"

	// SeverityInfo provides informational context about the active configuration
	// without indicating a problem.
	SeverityInfo Severity = "INFO"
)

// LintResult is a single diagnostic produced by the policy linter.
type LintResult struct {
	// Code is the rule identifier (e.g., "L001"). See mvp-plan.md §6.4 for the
	// full rule table.
	Code string

	// Severity is the urgency level of this diagnostic.
	Severity Severity

	// Message is the human-readable explanation of the issue, suitable for
	// display directly to policy authors.
	Message string
}

// validContentOperators is the complete set of operator strings accepted in
// never_when content predicates. Any other value triggers L014.
var validContentOperators = map[string]bool{
	"is_external":      true,
	"contains_pattern": true,
	"equals":           true,
	"not_equals":       true,
}

// validEscalateOperators is the complete set of operator strings accepted in
// escalate_when rules. Any other value triggers L012.
var validEscalateOperators = map[string]bool{
	">":        true,
	"<":        true,
	">=":       true,
	"<=":       true,
	"==":       true,
	"!=":       true,
	"contains": true,
	"matches":  true,
}

// Lint runs all linter rules against the parsed policy p and returns the full
// list of diagnostics. An empty return slice means the policy passes all checks
// with no issues. Lint never panics and never modifies p.
func Lint(p *Policy) []LintResult {
	var results []LintResult
	results = append(results, lintL001(p)...)
	results = append(results, lintL002(p)...)
	results = append(results, lintL003(p)...)
	results = append(results, lintL004(p)...)
	results = append(results, lintL005(p)...)
	results = append(results, lintL006(p)...)
	results = append(results, lintL007(p)...)
	results = append(results, lintL008(p)...)
	results = append(results, lintL009(p)...)
	results = append(results, lintL010(p)...)
	results = append(results, lintL011(p)...)
	results = append(results, lintL012(p)...)
	results = append(results, lintL013(p)...)
	results = append(results, lintL014(p)...)
	results = append(results, lintL015(p)...)
	results = append(results, lintL016(p)...)
	results = append(results, lintL017(p)...)
	results = append(results, lintL018(p)...)
	results = append(results, lintL019(p)...)
	return results
}

// buildMayUseSet returns a set of tool names from p.MayUse for O(1) membership
// checks in linter rules.
func buildMayUseSet(p *Policy) map[string]bool {
	s := make(map[string]bool, len(p.MayUse))
	for _, name := range p.MayUse {
		s[name] = true
	}
	return s
}

// lintL001 reports an error if may_use is empty or missing. Without a may_use
// list the pipeline denies every tool call — the agent can do nothing.
func lintL001(p *Policy) []LintResult {
	if len(p.MayUse) == 0 {
		return []LintResult{{
			Code:     "L001",
			Severity: SeverityError,
			Message:  "may_use is empty or missing: every agent must declare the tools it is permitted to call",
		}}
	}
	return nil
}

// lintL002 reports an error for each tool in tools: that is not listed in
// may_use. Tools not in may_use are always denied before any other check runs,
// so sequence and escalation rules on such tools are unreachable dead code.
func lintL002(p *Policy) []LintResult {
	allowed := buildMayUseSet(p)
	var results []LintResult
	for name := range p.Tools {
		if !allowed[name] {
			results = append(results, LintResult{
				Code:     "L002",
				Severity: SeverityError,
				Message:  fmt.Sprintf("tool %q is defined in tools: but not listed in may_use", name),
			})
		}
	}
	return results
}

// lintL003 reports an error for each only_after predicate referencing a tool
// not in may_use. If the dependency tool can never be called (it is not in
// may_use), the only_after guard can never be satisfied and the protected tool
// will be permanently blocked.
func lintL003(p *Policy) []LintResult {
	allowed := buildMayUseSet(p)
	var results []LintResult
	for toolName, tp := range p.Tools {
		for _, dep := range tp.Sequence.OnlyAfter {
			if !allowed[dep] {
				results = append(results, LintResult{
					Code:     "L003",
					Severity: SeverityError,
					Message:  fmt.Sprintf("tool %q: only_after references %q which is not in may_use", toolName, dep),
				})
			}
		}
	}
	return results
}

// lintL004 reports an error for each never_after predicate referencing a tool
// not in may_use. A never_after dependency that cannot be called is a
// permanently unsatisfiable guard (it is vacuously false, providing no
// protection).
func lintL004(p *Policy) []LintResult {
	allowed := buildMayUseSet(p)
	var results []LintResult
	for toolName, tp := range p.Tools {
		for _, dep := range tp.Sequence.NeverAfter {
			if !allowed[dep] {
				results = append(results, LintResult{
					Code:     "L004",
					Severity: SeverityError,
					Message:  fmt.Sprintf("tool %q: never_after references %q which is not in may_use", toolName, dep),
				})
			}
		}
	}
	return results
}

// lintL005 warns when enforcement_mode is not explicitly set. Without it the
// pipeline defaults to shadow mode, which operators may not intend.
func lintL005(p *Policy) []LintResult {
	if p.EnforcementMode == "" {
		return []LintResult{{
			Code:     "L005",
			Severity: SeverityWarning,
			Message:  "enforcement_mode is not set; defaulting to shadow (violations are logged but not blocked)",
		}}
	}
	return nil
}

// lintL006 warns when no budget block is defined. Without a budget, sessions
// have no tool call or cost ceilings and a runaway agent will not be stopped
// by the budget evaluator.
func lintL006(p *Policy) []LintResult {
	if p.Budget.MaxToolCalls == 0 && p.Budget.MaxCostUSD == 0 {
		return []LintResult{{
			Code:     "L006",
			Severity: SeverityWarning,
			Message:  "no budget block defined: sessions have no tool call or cost ceiling",
		}}
	}
	return nil
}

// lintL007 warns when session.max_history is not set. The pipeline applies a
// default cap at runtime, but operators should set this explicitly so they
// understand the session lifecycle and sequence-engine storage requirements.
func lintL007(p *Policy) []LintResult {
	if p.Session.MaxHistory == 0 {
		return []LintResult{{
			Code:     "L007",
			Severity: SeverityWarning,
			Message:  "session.max_history is not set; the pipeline will apply a default limit at runtime",
		}}
	}
	return nil
}

// lintL008 warns when a tool has an escalate_when rule but no escalation
// channel (webhook_url) is configured. Without a channel, escalation events are
// only written to stdout, which operators are unlikely to monitor in production.
func lintL008(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		if tp.EscalateWhen != nil && (p.Escalation == nil || p.Escalation.WebhookURL == "") {
			results = append(results, LintResult{
				Code:     "L008",
				Severity: SeverityWarning,
				Message: fmt.Sprintf(
					"tool %q has escalate_when but escalation.webhook_url is not configured; escalation events will only appear on stdout",
					toolName,
				),
			})
		}
	}
	return results
}

// lintL009 is an informational reminder when enforcement_mode is shadow. Shadow
// mode is the recommended onboarding default, but it must not remain in
// production policies indefinitely — violations are observed but not blocked.
func lintL009(p *Policy) []LintResult {
	if p.EnforcementMode == EnforcementShadow {
		return []LintResult{{
			Code:     "L009",
			Severity: SeverityInfo,
			Message:  "enforcement_mode is shadow: policy violations are logged but not blocked; change to block for production enforcement",
		}}
	}
	return nil
}

// lintL010 reports an error when requires_prior_n.count is zero or negative.
// A non-positive count makes the predicate trivially satisfied (count=0) or
// semantically undefined (count<0), neither of which is the operator's intent.
func lintL010(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		if tp.Sequence.RequiresPriorN != nil && tp.Sequence.RequiresPriorN.Count <= 0 {
			results = append(results, LintResult{
				Code:     "L010",
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"tool %q: requires_prior_n.count must be a positive integer, got %d",
					toolName, tp.Sequence.RequiresPriorN.Count,
				),
			})
		}
	}
	return results
}

// lintL011 warns when at least one tool taints the session but no tool clears
// it. Once tainted, the session can never recover, so any taint-sensitive tool
// will eventually be permanently blocked for the rest of the session's lifetime.
func lintL011(p *Policy) []LintResult {
	hasApplies := false
	hasClears := false
	for _, tp := range p.Tools {
		if tp.Taint.Applies {
			hasApplies = true
		}
		if tp.Taint.Clears {
			hasClears = true
		}
	}
	if hasApplies && !hasClears {
		return []LintResult{{
			Code:     "L011",
			Severity: SeverityWarning,
			Message:  "a tool has taint.applies: true but no tool has taint.clears: true; once the session is tainted it can never be untainted",
		}}
	}
	return nil
}

// lintL012 reports an error when escalate_when.operator is not one of the
// supported comparison operators. An unrecognised operator causes the evaluation
// pipeline to fail closed (deny) on every call to that tool regardless of
// the actual argument values.
func lintL012(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		if tp.EscalateWhen != nil && !validEscalateOperators[tp.EscalateWhen.Operator] {
			results = append(results, LintResult{
				Code:     "L012",
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"tool %q: escalate_when.operator %q is not valid; supported operators: >, <, >=, <=, ==, !=, contains, matches",
					toolName, tp.EscalateWhen.Operator,
				),
			})
		}
	}
	return results
}

// lintL014 reports an error when a never_when predicate uses an operator that
// the ContentEvaluator does not recognise. An unrecognised operator causes the
// evaluation pipeline to fail closed (deny) on every call to that tool,
// blocking the agent permanently regardless of actual argument values.
func lintL014(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		for i, pred := range tp.NeverWhen {
			if !validContentOperators[pred.Operator] {
				results = append(results, LintResult{
					Code:     "L014",
					Severity: SeverityError,
					Message: fmt.Sprintf(
						"tool %q: never_when[%d] operator %q is not valid; supported operators: is_external, contains_pattern, equals, not_equals",
						toolName, i, pred.Operator,
					),
				})
			}
		}
	}
	return results
}

// lintL015 reports an error when a never_when predicate uses contains_pattern
// with a value that does not compile as a valid Go regexp. An invalid pattern
// causes the ContentEvaluator to return an error at runtime, which the pipeline
// converts to a Deny — permanently blocking the tool for every call.
//
// Leading and trailing / delimiters (Perl/JS notation) are stripped before
// compilation, matching the runtime behaviour of the ContentEvaluator.
func lintL015(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		for i, pred := range tp.NeverWhen {
			if pred.Operator != "contains_pattern" {
				continue
			}
			pattern := strings.TrimPrefix(pred.Value, "/")
			pattern = strings.TrimSuffix(pattern, "/")
			if _, err := regexp.Compile(pattern); err != nil {
				results = append(results, LintResult{
					Code:     "L015",
					Severity: SeverityError,
					Message: fmt.Sprintf(
						"tool %q: never_when[%d] contains_pattern value %q is not a valid Go regexp: %v",
						toolName, i, pred.Value, err,
					),
				})
			}
		}
	}
	return results
}

// lintL016 warns when session.require_env is set. This is not an error —
// require_env is a valid, intentional configuration — but operators who set
// it must remember to register their agents with the matching --env flag.
// Without it, all agent JWTs will lack the "env" claim and the EnvEvaluator
// will deny every tool call in the session. The linter surfaces this reminder
// at policy-authoring time rather than at runtime, when the denial would be
// harder to diagnose.
func lintL016(p *Policy) []LintResult {
	if p.Session.RequireEnv != "" {
		return []LintResult{{
			Code:     "L016",
			Severity: SeverityWarning,
			Message: fmt.Sprintf(
				"session.require_env is %q: register agents with --env %s; agents without a matching env claim will be denied by the EnvEvaluator",
				p.Session.RequireEnv, p.Session.RequireEnv,
			),
		}}
	}
	return nil
}

// lintL017 reports an error when rate_limit.window_seconds is zero or negative.
// A non-positive window makes the predicate semantically undefined: every call
// would be outside the window (window_seconds ≤ 0 means the since-threshold is
// at or after the current time), so the rate limit would never trigger regardless
// of how many calls were made. This is almost certainly a misconfiguration.
func lintL017(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		if tp.RateLimit != nil && tp.RateLimit.WindowSeconds <= 0 {
			results = append(results, LintResult{
				Code:     "L017",
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"tool %q: rate_limit.window_seconds must be a positive integer, got %d",
					toolName, tp.RateLimit.WindowSeconds,
				),
			})
		}
	}
	return results
}

// lintL018 reports an error when rate_limit.max_calls is zero or negative.
// A non-positive max_calls makes every call to the tool an immediate deny,
// which is equivalent to removing the tool from may_use entirely. Operators
// who intend to disable a tool should remove it from may_use rather than
// setting max_calls to zero or a negative value.
func lintL018(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		if tp.RateLimit != nil && tp.RateLimit.MaxCalls <= 0 {
			results = append(results, LintResult{
				Code:     "L018",
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"tool %q: rate_limit.max_calls must be a positive integer, got %d",
					toolName, tp.RateLimit.MaxCalls,
				),
			})
		}
	}
	return results
}

// lintL019 warns when a never_when block has more than one predicate but no
// never_when_match field is set. The implied default is "any" (OR logic), but
// an operator who copies an AND-logic example from documentation will get
// unexpected OR behaviour without this warning. Explicitly setting
// never_when_match: any or never_when_match: all removes the ambiguity.
func lintL019(p *Policy) []LintResult {
	var results []LintResult
	for toolName, tp := range p.Tools {
		if len(tp.NeverWhen) > 1 && tp.NeverWhenMatch == "" {
			results = append(results, LintResult{
				Code:     "L019",
				Severity: SeverityWarning,
				Message: fmt.Sprintf(
					"tool %q: never_when has %d predicates but never_when_match is not set; defaulting to \"any\" (OR logic) — add never_when_match: any or never_when_match: all to make the intent explicit",
					toolName, len(tp.NeverWhen),
				),
			})
		}
	}
	return results
}

// lintL013 detects circular only_after dependencies that create permanent
// deadlocks. If tool A has only_after: [B] and tool B has only_after: [A],
// neither can ever be called first, so neither can ever be called at all.
//
// Design: we build a dependency graph where an edge T → D means "T has D in
// its only_after list (T requires D to have been called first)". A cycle in
// this directed graph means the agent will permanently block on those tools.
// We use three-colour DFS (white/gray/black) to detect cycles of any length —
// pairs, triples, and longer chains — and reconstruct the full cycle path so
// the operator can identify every tool involved.
func lintL013(p *Policy) []LintResult {
	// Build an adjacency list from only_after relationships. An edge T → D
	// means "T depends on D" (D must be called before T). Only tools in the
	// tools: block can have sequence predicates; tools only in may_use are
	// leaf nodes with no outgoing edges and cannot participate in a cycle.
	adj := make(map[string][]string, len(p.Tools))
	for toolName, tp := range p.Tools {
		for _, dep := range tp.Sequence.OnlyAfter {
			adj[toolName] = append(adj[toolName], dep)
		}
		// Ensure every tool with sequence constraints is a node in the graph
		// even when it has no outgoing edges, so the outer loop visits it.
		if _, ok := adj[toolName]; !ok {
			adj[toolName] = nil
		}
	}

	// Three-colour DFS: white = unvisited, gray = on the current DFS stack,
	// black = fully explored (no cycle reachable from this node).
	const (
		colorWhite = 0
		colorGray  = 1
		colorBlack = 2
	)
	color := make(map[string]int, len(adj))
	var cyclePath []string

	var dfs func(node string, path []string) bool
	dfs = func(node string, path []string) bool {
		color[node] = colorGray
		path = append(path, node)

		for _, neighbor := range adj[node] {
			switch color[neighbor] {
			case colorGray:
				// Back edge found — neighbor is on the current DFS stack.
				// Reconstruct the cycle starting from where neighbor first
				// appears in path, then append neighbor again to close the loop.
				cycleStart := 0
				for i, n := range path {
					if n == neighbor {
						cycleStart = i
						break
					}
				}
				// cyclePath = [neighbor, ..., node, neighbor] — the entry
				// node appears at both ends, making the cycle explicit.
				cyclePath = append(append([]string(nil), path[cycleStart:]...), neighbor)
				return true
			case colorWhite:
				if dfs(neighbor, path) {
					return true
				}
			}
		}
		color[node] = colorBlack
		return false
	}

	for node := range adj {
		if color[node] == colorWhite {
			if dfs(node, nil) {
				// Format: "A → only_after → B → only_after → A"
				msg := fmt.Sprintf(
					"circular sequence dependency: %s\nThis session can never satisfy both constraints simultaneously. The agent will permanently block.",
					strings.Join(cyclePath, " → only_after → "),
				)
				return []LintResult{{
					Code:     "L013",
					Severity: SeverityError,
					Message:  msg,
				}}
			}
		}
	}
	return nil
}
