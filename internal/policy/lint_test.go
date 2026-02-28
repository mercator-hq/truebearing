package policy_test

import (
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/policy"
)

// hasCode reports whether any result in results carries the given lint code.
func hasCode(results []policy.LintResult, code string) bool {
	for _, r := range results {
		if r.Code == code {
			return true
		}
	}
	return false
}

// mustParseBytes is a test helper that calls policy.ParseBytes and fatally
// fails the test if parsing returns an error.
func mustParseBytes(t *testing.T, yaml string) *policy.Policy {
	t.Helper()
	p, err := policy.ParseBytes([]byte(yaml), "test")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	return p
}

// TestLint_L001 verifies that L001 fires when may_use is empty or absent and
// does not fire when may_use has at least one entry.
func TestLint_L001(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "empty may_use triggers L001",
			yaml: `
version: "1"
agent: data-agent
may_use: []
`,
			wantHit: true,
		},
		{
			name: "absent may_use triggers L001",
			yaml: `
version: "1"
agent: data-agent
`,
			wantHit: true,
		},
		{
			name: "non-empty may_use does not trigger L001",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L001")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L001\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L002 verifies that L002 fires for each tool in tools: that is not
// listed in may_use, and does not fire when all tools are in may_use.
func TestLint_L002(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "tool in tools: not in may_use triggers L002",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_b: {}
`,
			wantHit: true,
		},
		{
			name: "all tools in may_use does not trigger L002",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a: {}
  tool_b: {}
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L002")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L002\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L003 verifies that L003 fires when only_after references a tool not
// in may_use, and does not fire when all referenced tools are in may_use.
func TestLint_L003(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "only_after references unknown tool triggers L003",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      only_after:
        - tool_b
        - unknown_tool
`,
			wantHit: true,
		},
		{
			name: "only_after references only known tools does not trigger L003",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      only_after:
        - tool_b
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L003")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L003\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L004 verifies that L004 fires when never_after references a tool not
// in may_use, and does not fire when all referenced tools are known.
func TestLint_L004(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "never_after references unknown tool triggers L004",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      never_after:
        - ghost_tool
`,
			wantHit: true,
		},
		{
			name: "never_after references only known tools does not trigger L004",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      never_after:
        - tool_b
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L004")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L004\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L005 verifies that L005 fires when enforcement_mode is absent and
// does not fire when it is explicitly set.
func TestLint_L005(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "missing enforcement_mode triggers L005",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
`,
			wantHit: true,
		},
		{
			name: "explicit enforcement_mode block does not trigger L005",
			yaml: `
version: "1"
agent: data-agent
enforcement_mode: block
may_use:
  - tool_a
`,
			wantHit: false,
		},
		{
			name: "explicit enforcement_mode shadow does not trigger L005",
			yaml: `
version: "1"
agent: data-agent
enforcement_mode: shadow
may_use:
  - tool_a
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L005")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L005\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L006 verifies that L006 fires when no budget block is defined and
// does not fire when at least one budget limit is configured.
func TestLint_L006(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "absent budget block triggers L006",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
`,
			wantHit: true,
		},
		{
			name: "budget with max_tool_calls only does not trigger L006",
			yaml: `
version: "1"
agent: data-agent
budget:
  max_tool_calls: 50
may_use:
  - tool_a
`,
			wantHit: false,
		},
		{
			name: "budget with max_cost_usd only does not trigger L006",
			yaml: `
version: "1"
agent: data-agent
budget:
  max_cost_usd: 5.00
may_use:
  - tool_a
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L006")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L006\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L007 verifies that L007 fires when session.max_history is absent or
// zero, and does not fire when it is explicitly set to a positive value.
func TestLint_L007(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "absent session block triggers L007",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
`,
			wantHit: true,
		},
		{
			name: "session block without max_history triggers L007",
			yaml: `
version: "1"
agent: data-agent
session:
  max_duration_seconds: 3600
may_use:
  - tool_a
`,
			wantHit: true,
		},
		{
			name: "session.max_history set to positive value does not trigger L007",
			yaml: `
version: "1"
agent: data-agent
session:
  max_history: 1000
may_use:
  - tool_a
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L007")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L007\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L008 verifies that L008 fires when a tool has escalate_when but no
// webhook_url is configured, and does not fire when a channel is present.
func TestLint_L008(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "escalate_when without webhook_url triggers L008",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.amount"
      operator: ">"
      value: 1000
`,
			wantHit: true,
		},
		{
			name: "escalate_when with webhook_url configured does not trigger L008",
			yaml: `
version: "1"
agent: data-agent
escalation:
  webhook_url: "https://hooks.example.com/notify"
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.amount"
      operator: ">"
      value: 1000
`,
			wantHit: false,
		},
		{
			name: "no escalate_when does not trigger L008",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a: {}
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L008")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L008\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L009 verifies that L009 fires when enforcement_mode is shadow (as an
// informational reminder) and does not fire for block mode.
func TestLint_L009(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "enforcement_mode shadow triggers L009",
			yaml: `
version: "1"
agent: data-agent
enforcement_mode: shadow
may_use:
  - tool_a
`,
			wantHit: true,
		},
		{
			name: "enforcement_mode block does not trigger L009",
			yaml: `
version: "1"
agent: data-agent
enforcement_mode: block
may_use:
  - tool_a
`,
			wantHit: false,
		},
		{
			name: "absent enforcement_mode does not trigger L009 (L005 fires instead)",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L009")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L009\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L010 verifies that L010 fires when requires_prior_n.count is zero or
// negative, and does not fire when count is a positive integer.
func TestLint_L010(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "requires_prior_n.count zero triggers L010",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      requires_prior_n:
        tool: tool_b
        count: 0
`,
			wantHit: true,
		},
		{
			name: "requires_prior_n.count negative triggers L010",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      requires_prior_n:
        tool: tool_b
        count: -1
`,
			wantHit: true,
		},
		{
			name: "requires_prior_n.count positive does not trigger L010",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      requires_prior_n:
        tool: tool_b
        count: 2
`,
			wantHit: false,
		},
		{
			name: "no requires_prior_n does not trigger L010",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a: {}
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L010")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L010\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L011 verifies that L011 fires when a tool taints the session but no
// tool clears it, and does not fire when a clearing tool exists.
func TestLint_L011(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "taint.applies without any taint.clears triggers L011",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    taint:
      applies: true
  tool_b: {}
`,
			wantHit: true,
		},
		{
			name: "taint.applies with a taint.clears tool does not trigger L011",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    taint:
      applies: true
  tool_b:
    taint:
      clears: true
`,
			wantHit: false,
		},
		{
			name: "no taint at all does not trigger L011",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a: {}
`,
			wantHit: false,
		},
		{
			name: "taint.clears without taint.applies does not trigger L011",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    taint:
      clears: true
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L011")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L011\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L012 verifies that L012 fires for unrecognised escalate_when
// operators and does not fire for any of the valid operators.
func TestLint_L012(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHit bool
	}{
		{
			name: "invalid operator triggers L012",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.amount"
      operator: "between"
      value: 1000
`,
			wantHit: true,
		},
		{
			name: "operator greater-than does not trigger L012",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.amount"
      operator: ">"
      value: 1000
`,
			wantHit: false,
		},
		{
			name: "operator contains does not trigger L012",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.label"
      operator: "contains"
      value: "critical"
`,
			wantHit: false,
		},
		{
			name: "operator matches does not trigger L012",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.label"
      operator: "matches"
      value: "^PROD-"
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L012")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L012\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L013 verifies that L013 detects circular only_after dependencies
// (direct 2-node cycles, indirect 3-node cycles, and self-loops) and does not
// fire on acyclic dependency graphs.
func TestLint_L013(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		wantHit  bool
		wantPath string // substring that must appear in the error message when wantHit=true
	}{
		{
			name: "direct 2-node cycle triggers L013",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      only_after:
        - tool_b
  tool_b:
    sequence:
      only_after:
        - tool_a
`,
			wantHit:  true,
			wantPath: "only_after",
		},
		{
			name: "3-node cycle triggers L013",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
  - tool_c
tools:
  tool_a:
    sequence:
      only_after:
        - tool_b
  tool_b:
    sequence:
      only_after:
        - tool_c
  tool_c:
    sequence:
      only_after:
        - tool_a
`,
			wantHit:  true,
			wantPath: "only_after",
		},
		{
			name: "self-loop triggers L013",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    sequence:
      only_after:
        - tool_a
`,
			wantHit:  true,
			wantPath: "tool_a → only_after → tool_a",
		},
		{
			name: "acyclic DAG does not trigger L013",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
  - tool_c
tools:
  tool_a:
    sequence:
      only_after:
        - tool_b
  tool_b:
    sequence:
      only_after:
        - tool_c
  tool_c: {}
`,
			wantHit: false,
		},
		{
			name: "cycle among subset of tools (others are acyclic) triggers L013",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
  - tool_c
tools:
  tool_a: {}
  tool_b:
    sequence:
      only_after:
        - tool_c
  tool_c:
    sequence:
      only_after:
        - tool_b
`,
			wantHit:  true,
			wantPath: "only_after",
		},
		{
			name: "no only_after predicates at all does not trigger L013",
			yaml: `
version: "1"
agent: data-agent
may_use:
  - tool_a
  - tool_b
tools:
  tool_a:
    sequence:
      never_after:
        - tool_b
  tool_b: {}
`,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParseBytes(t, tc.yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L013")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L013\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
			if tc.wantHit && tc.wantPath != "" {
				// Verify the cycle path appears in the error message.
				found := false
				for _, r := range results {
					if r.Code == "L013" && strings.Contains(r.Message, tc.wantPath) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("L013 message does not contain expected path substring %q; got: %v", tc.wantPath, results)
				}
			}
		})
	}
}

// TestLint_L013_MessageFormat verifies the exact message structure for a simple
// 2-node cycle matches the format specified in mvp-plan.md §6.4.
func TestLint_L013_MessageFormat(t *testing.T) {
	const twoNodeCycle = `
version: "1"
agent: data-agent
may_use:
  - alpha
  - beta
tools:
  alpha:
    sequence:
      only_after:
        - beta
  beta:
    sequence:
      only_after:
        - alpha
`
	p := mustParseBytes(t, twoNodeCycle)
	results := policy.Lint(p)
	if !hasCode(results, "L013") {
		t.Fatal("expected L013 for 2-node cycle, got none")
	}
	for _, r := range results {
		if r.Code != "L013" {
			continue
		}
		if !strings.Contains(r.Message, "circular sequence dependency") {
			t.Errorf("L013 message missing \"circular sequence dependency\"; got: %q", r.Message)
		}
		if !strings.Contains(r.Message, "only_after") {
			t.Errorf("L013 message missing \"only_after\"; got: %q", r.Message)
		}
		if !strings.Contains(r.Message, "permanently block") {
			t.Errorf("L013 message missing \"permanently block\"; got: %q", r.Message)
		}
		if r.Severity != policy.SeverityError {
			t.Errorf("L013 severity = %q, want ERROR", r.Severity)
		}
	}
}

// TestLint_CleanPolicy verifies that a well-formed policy with all recommended
// fields set and no rule violations produces zero lint results.
func TestLint_CleanPolicy(t *testing.T) {
	const clean = `
version: "1"
agent: payments-agent
enforcement_mode: block

session:
  max_history: 1000

budget:
  max_tool_calls: 50
  max_cost_usd: 5.00

escalation:
  webhook_url: "https://hooks.example.com/alerts"

may_use:
  - read_invoice
  - verify_invoice
  - manager_approval
  - execute_payment
  - run_compliance_scan
  - check_escalation_status

tools:
  execute_payment:
    enforcement_mode: block
    sequence:
      only_after:
        - verify_invoice
        - manager_approval
      never_after:
        - run_compliance_scan
      requires_prior_n:
        tool: verify_invoice
        count: 1
    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 10000

  verify_invoice:
    taint:
      applies: true
      label: "invoice_verified"

  run_compliance_scan:
    taint:
      clears: true

  read_invoice: {}
  manager_approval: {}
  check_escalation_status: {}
`
	p := mustParseBytes(t, clean)
	results := policy.Lint(p)
	if len(results) != 0 {
		t.Errorf("expected zero lint results for clean policy, got %d: %v", len(results), results)
	}
}

// TestLint_SeverityValues verifies that the exported severity constants have
// the expected string values that downstream consumers (cmd/policy/lint.go)
// can use for output colouring.
func TestLint_SeverityValues(t *testing.T) {
	cases := []struct {
		sev  policy.Severity
		want string
	}{
		{policy.SeverityError, "ERROR"},
		{policy.SeverityWarning, "WARNING"},
		{policy.SeverityInfo, "INFO"},
	}
	for _, tc := range cases {
		if string(tc.sev) != tc.want {
			t.Errorf("Severity %v = %q, want %q", tc.sev, string(tc.sev), tc.want)
		}
	}
}

// TestLint_AllValidOperatorsPassL012 verifies that every supported escalation
// operator passes L012 — i.e., none of the valid operators are accidentally
// included in the validation reject-list.
func TestLint_AllValidOperatorsPassL012(t *testing.T) {
	validOps := []string{">", "<", ">=", "<=", "==", "!=", "contains", "matches"}
	for _, op := range validOps {
		t.Run("operator_"+op, func(t *testing.T) {
			yaml := `
version: "1"
agent: data-agent
may_use:
  - tool_a
tools:
  tool_a:
    escalate_when:
      argument_path: "$.v"
      operator: "` + op + `"
      value: 1
`
			p := mustParseBytes(t, yaml)
			results := policy.Lint(p)
			if hasCode(results, "L012") {
				t.Errorf("operator %q incorrectly triggers L012", op)
			}
		})
	}
}

// TestLint_L014 verifies that L014 fires when a never_when predicate uses an
// unrecognised operator and does not fire for all four supported operators.
func TestLint_L014(t *testing.T) {
	cases := []struct {
		name     string
		operator string
		wantHit  bool
	}{
		{
			name:     "unknown operator triggers L014",
			operator: "is_purple",
			wantHit:  true,
		},
		{
			name:     "empty operator triggers L014",
			operator: "",
			wantHit:  true,
		},
		{
			name:     "is_external does not trigger L014",
			operator: "is_external",
			wantHit:  false,
		},
		{
			name:     "contains_pattern does not trigger L014",
			operator: "contains_pattern",
			wantHit:  false,
		},
		{
			name:     "equals does not trigger L014",
			operator: "equals",
			wantHit:  false,
		},
		{
			name:     "not_equals does not trigger L014",
			operator: "not_equals",
			wantHit:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
version: "1"
agent: data-agent
enforcement_mode: block
may_use:
  - send-email
tools:
  send-email:
    never_when:
      - argument: recipient
        operator: "` + tc.operator + `"
        value: "@acme.com"
`
			p := mustParseBytes(t, yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L014")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L014\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L015 verifies that L015 fires when a contains_pattern predicate
// has an invalid regexp value and does not fire for valid patterns (including
// /delimiter/ notation).
func TestLint_L015(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantHit bool
	}{
		{
			name:    "invalid regexp triggers L015",
			value:   "[unclosed",
			wantHit: true,
		},
		{
			name:    "invalid regexp with delimiters triggers L015",
			value:   "/[unclosed/",
			wantHit: true,
		},
		{
			name:    "valid plain regexp does not trigger L015",
			value:   "secret|key|token",
			wantHit: false,
		},
		{
			name:    "valid /delimiter/ regexp does not trigger L015",
			value:   "/secret|key|token/",
			wantHit: false,
		},
		{
			name:    "empty value (matches everything) does not trigger L015",
			value:   "",
			wantHit: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
version: "1"
agent: data-agent
enforcement_mode: block
may_use:
  - send-email
tools:
  send-email:
    never_when:
      - argument: body
        operator: contains_pattern
        value: "` + tc.value + `"
`
			p := mustParseBytes(t, yaml)
			results := policy.Lint(p)
			got := hasCode(results, "L015")
			if got != tc.wantHit {
				t.Errorf("hasCode(results, \"L015\") = %v, want %v; results: %v", got, tc.wantHit, results)
			}
		})
	}
}

// TestLint_L015_NonPatternOperators verifies that L015 only fires for
// contains_pattern and is silent for other operators regardless of Value.
func TestLint_L015_NonPatternOperators(t *testing.T) {
	for _, op := range []string{"equals", "not_equals", "is_external"} {
		t.Run("operator="+op, func(t *testing.T) {
			yaml := `
version: "1"
agent: data-agent
enforcement_mode: block
may_use:
  - send-email
tools:
  send-email:
    never_when:
      - argument: recipient
        operator: "` + op + `"
        value: "[not-a-pattern-but-irrelevant]"
`
			p := mustParseBytes(t, yaml)
			results := policy.Lint(p)
			if hasCode(results, "L015") {
				t.Errorf("operator %q incorrectly triggers L015", op)
			}
		})
	}
}
