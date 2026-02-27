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

	Sequence     SequencePolicy `yaml:"sequence"      json:"sequence"`
	Taint        TaintPolicy    `yaml:"taint"         json:"taint"`
	EscalateWhen *EscalateRule  `yaml:"escalate_when" json:"escalate_when"`
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
