package policy

// EnforcementMode controls whether policy violations are blocked or only observed.
type EnforcementMode string

const (
	// EnforcementShadow logs violations and allows the call through. Use during
	// onboarding and policy tuning so violations can be reviewed before blocking.
	EnforcementShadow EnforcementMode = "shadow"

	// EnforcementBlock denies calls that violate the policy. Use in production
	// after validating the policy with a week of shadow-mode observation.
	EnforcementBlock EnforcementMode = "block"
)

// Policy is the parsed, fingerprinted representation of a TrueBearing policy
// YAML file. It is produced exclusively by ParseFile and ParseBytes.
//
// The Fingerprint field is always set by the parser. Callers must not compute
// or override it.
type Policy struct {
	Version         string                `yaml:"version"          json:"version"`
	Agent           string                `yaml:"agent"            json:"agent"`
	EnforcementMode EnforcementMode       `yaml:"enforcement_mode" json:"enforcement_mode"`
	Session         SessionPolicy         `yaml:"session"          json:"session"`
	Budget          BudgetPolicy          `yaml:"budget"           json:"budget"`
	MayUse          []string              `yaml:"may_use"          json:"may_use"`
	Tools           map[string]ToolPolicy `yaml:"tools"            json:"tools"`
	// Escalation holds optional notification settings for escalation events.
	// omitempty ensures policies that do not set this block produce identical
	// fingerprints to policies authored before this field was added.
	Escalation *EscalationConfig `yaml:"escalation" json:"escalation,omitempty"`

	// Derived at parse time — excluded from JSON so the fingerprint hash does
	// not include itself, and SourcePath is excluded because it is a local
	// filesystem path that differs across machines.
	Fingerprint string `yaml:"-" json:"-"`
	SourcePath  string `yaml:"-" json:"-"`
}

// ShortFingerprint returns the first 8 hex characters of p.Fingerprint for
// display use (e.g., CLI output, audit records, health endpoint). The full
// 64-character fingerprint is in Policy.Fingerprint.
func (p *Policy) ShortFingerprint() string {
	if len(p.Fingerprint) < 8 {
		return p.Fingerprint
	}
	return p.Fingerprint[:8]
}

// SessionPolicy governs per-session lifetime and history limits.
type SessionPolicy struct {
	// MaxHistory is the hard cap on the number of events stored per session.
	// When reached, the session is considered exhausted and a new session must
	// be started. Events are never evicted. Default 0 means no limit is
	// configured (the linter warns via L007).
	MaxHistory int `yaml:"max_history" json:"max_history"`

	// MaxDurationSeconds is the optional wall-clock lifetime of a session.
	// Zero means no duration limit.
	MaxDurationSeconds int `yaml:"max_duration_seconds" json:"max_duration_seconds"`

	// RequireEnv, when non-empty, restricts this policy to agents whose JWT
	// carries a matching "env" claim. Agents registered without --env, or with
	// a different environment name, are denied by the EnvEvaluator before any
	// other pipeline stage runs. The linter warns (L016) when this field is set
	// so operators are reminded to register agents with the corresponding --env flag.
	RequireEnv string `yaml:"require_env" json:"require_env,omitempty"`
}

// BudgetPolicy sets hard ceilings on resource consumption per session.
// A zero-valued BudgetPolicy means no budget limits are configured (the
// linter warns via L006).
type BudgetPolicy struct {
	// MaxToolCalls is the maximum number of tool calls allowed in a session.
	MaxToolCalls int `yaml:"max_tool_calls" json:"max_tool_calls"`

	// MaxCostUSD is the maximum estimated cost in USD allowed in a session.
	MaxCostUSD float64 `yaml:"max_cost_usd" json:"max_cost_usd"`
}

// ToolPolicy defines the constraints applied to a single tool within a policy.
// A tool with an empty ToolPolicy is allowed with no restrictions beyond the
// global may_use check and budget.
type ToolPolicy struct {
	// EnforcementMode overrides the global policy enforcement_mode for this
	// specific tool. See the enforcement-mode hierarchy in mvp-plan.md §12.
	EnforcementMode EnforcementMode `yaml:"enforcement_mode" json:"enforcement_mode"`

	Sequence     SequencePolicy     `yaml:"sequence"      json:"sequence"`
	Taint        TaintPolicy        `yaml:"taint"         json:"taint"`
	EscalateWhen *EscalateRule      `yaml:"escalate_when"    json:"escalate_when"`
	NeverWhen    []ContentPredicate `yaml:"never_when"       json:"never_when"`
	// NeverWhenMatch controls how the predicates in NeverWhen are combined.
	// "any" (default when absent) fires when any single predicate matches (OR logic).
	// "all" fires only when every predicate matches simultaneously (AND logic).
	// An absent value with more than one predicate triggers lint rule L019.
	NeverWhenMatch ContentMatchMode `yaml:"never_when_match" json:"never_when_match,omitempty"`
	RateLimit      *RateLimitPolicy `yaml:"rate_limit"       json:"rate_limit,omitempty"`
}

// RateLimitPolicy sets a per-tool call frequency ceiling within a rolling
// time window. A tool that exceeds max_calls within the past window_seconds
// is denied until older calls drop off the window boundary.
//
// Both fields must be positive integers. Lint rule L017 (window_seconds ≤ 0)
// and L018 (max_calls ≤ 0) enforce this at policy-authoring time.
type RateLimitPolicy struct {
	// MaxCalls is the maximum number of allowed calls within WindowSeconds.
	// The limit is exclusive: a tool called max_calls times is still allowed;
	// the (max_calls + 1)th call within the window is denied.
	MaxCalls int `yaml:"max_calls" json:"max_calls"`

	// WindowSeconds is the width of the rolling time window in seconds.
	// Calls recorded more than this many seconds ago do not count toward
	// the rate limit.
	WindowSeconds int `yaml:"window_seconds" json:"window_seconds"`
}

// ContentMatchMode controls how multiple never_when predicates within a single
// never_when block are combined.
type ContentMatchMode string

const (
	// ContentMatchAny fires the block when any single predicate matches (OR logic).
	// This is the backward-compatible default when never_when_match is absent.
	ContentMatchAny ContentMatchMode = "any"

	// ContentMatchAll fires the block only when every predicate matches simultaneously
	// (AND logic). Use this when the pitch YAML shows combined conditions — e.g.
	// "block only if recipient is external AND body contains a pattern".
	ContentMatchAll ContentMatchMode = "all"
)

// ContentPredicate defines a single content-based guard on a tool argument
// value. The predicate fires — causing a Deny — when the named argument
// satisfies the condition. Predicates are evaluated in order; the first match
// terminates evaluation and returns a Deny (in "any" mode) or all predicates
// must fire before the block triggers (in "all" mode).
//
// Supported operators:
//   - is_external: fires when the argument string does NOT end with Value.
//     Value is the internal domain suffix (e.g. "@acme.com"). An empty Value
//     makes this predicate a no-op; the lint rule L014 does not flag this but
//     operators should set Value for the predicate to be meaningful.
//   - contains_pattern: fires when the argument string matches the Go regexp
//     in Value. Surrounding / delimiters (Perl/JS notation) are stripped
//     before compilation so the pitch YAML style "/pattern/" is accepted.
//   - equals: fires when the argument string equals Value exactly.
//   - not_equals: fires when the argument string does not equal Value.
type ContentPredicate struct {
	// Argument is the key in the tool call's arguments JSON object to inspect.
	Argument string `yaml:"argument" json:"argument"`

	// Operator is the comparison to apply. See the type-level doc for supported
	// values. An unrecognised operator is reported by lint rule L014 and causes
	// the evaluation pipeline to fail closed (deny) at runtime.
	Operator string `yaml:"operator" json:"operator"`

	// Value is the comparand for the operator. Required for contains_pattern,
	// equals, and not_equals. For is_external, Value is the internal domain
	// suffix; omitting it makes the predicate a no-op.
	Value string `yaml:"value" json:"value"`
}

// SequencePolicy defines the ordering constraints for a tool call relative to
// prior calls in the same session.
type SequencePolicy struct {
	// OnlyAfter lists tools that must all appear in session history before
	// this tool may be called. Missing any one of them causes a deny.
	OnlyAfter []string `yaml:"only_after" json:"only_after"`

	// NeverAfter lists tools that, if any appear in session history, block
	// this tool from being called.
	NeverAfter []string `yaml:"never_after" json:"never_after"`

	// RequiresPriorN requires a specific tool to have been called at least
	// Count times before this tool may run.
	RequiresPriorN *PriorNRule `yaml:"requires_prior_n" json:"requires_prior_n"`
}

// PriorNRule specifies that Tool must appear in session history at least Count
// times before the containing tool may be called.
type PriorNRule struct {
	Tool  string `yaml:"tool"  json:"tool"`
	Count int    `yaml:"count" json:"count"`
}

// TaintPolicy defines how a tool interacts with the session taint flag. A
// tainted session blocks tools whose never_after lists include taint-applying
// tools.
type TaintPolicy struct {
	// Applies marks the session as tainted when this tool is called.
	Applies bool `yaml:"applies" json:"applies"`

	// Label is a human-readable name for the taint source, used in audit
	// records and explain output.
	Label string `yaml:"label" json:"label"`

	// Clears removes the taint flag from the session when this tool is called.
	Clears bool `yaml:"clears" json:"clears"`
}

// EscalationConfig holds notification settings for escalation events. It is
// parsed from the top-level escalation: block in the policy YAML. The
// operational sending logic lives in internal/escalation (Task 5.5a); this
// type exists here so the linter (L008) can check whether a channel is
// configured.
type EscalationConfig struct {
	// WebhookURL is the HTTP endpoint to POST escalation notifications to.
	// If empty, escalation events are written to stdout only.
	WebhookURL string `yaml:"webhook_url" json:"webhook_url"`
}

// EscalateRule triggers a human escalation when a tool is called with
// arguments that satisfy the condition.
type EscalateRule struct {
	// ArgumentPath is a JSONPath expression applied to the tool call's
	// arguments JSON to extract the value to compare.
	ArgumentPath string `yaml:"argument_path" json:"argument_path"`

	// Operator is the comparison to apply. Supported values:
	//   Numeric: >, <, >=, <=, ==, !=
	//   String:  contains, matches
	Operator string `yaml:"operator" json:"operator"`

	// Design: Value is interface{} because escalation thresholds may be
	// numeric (e.g. 10000 for a USD amount) or string (e.g. "critical" for
	// a severity category). The evaluator in internal/engine uses gjson and
	// typed comparisons; this field is never used via interface{} dispatch on
	// the hot evaluation path. interface{} is the only correct Go type for a
	// YAML field whose schema type is not known at compile time.
	Value interface{} `yaml:"value" json:"value"`
}
