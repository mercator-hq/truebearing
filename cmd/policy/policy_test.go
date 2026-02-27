package policy

import (
	"bytes"
	"strings"
	"testing"

	internalpolicy "github.com/mercator-hq/truebearing/internal/policy"
)

// --- describeMode ---

func TestDescribeMode(t *testing.T) {
	cases := []struct {
		name string
		mode internalpolicy.EnforcementMode
		want string
	}{
		{
			name: "block",
			mode: internalpolicy.EnforcementBlock,
			want: "BLOCK (violations are denied)",
		},
		{
			name: "shadow",
			mode: internalpolicy.EnforcementShadow,
			want: "SHADOW (violations are logged but not blocked)",
		},
		{
			name: "empty defaults to shadow",
			mode: "",
			want: "SHADOW (default; violations are logged but not blocked)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeMode(tc.mode)
			if got != tc.want {
				t.Errorf("describeMode(%q) = %q; want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// --- describeBudget ---

func TestDescribeBudget(t *testing.T) {
	cases := []struct {
		name string
		b    internalpolicy.BudgetPolicy
		want string
	}{
		{
			name: "both configured",
			b:    internalpolicy.BudgetPolicy{MaxToolCalls: 50, MaxCostUSD: 5.0},
			want: "50 tool calls / $5.00 per session",
		},
		{
			name: "calls only",
			b:    internalpolicy.BudgetPolicy{MaxToolCalls: 100},
			want: "100 tool calls per session",
		},
		{
			name: "cost only",
			b:    internalpolicy.BudgetPolicy{MaxCostUSD: 2.50},
			want: "$2.50 per session",
		},
		{
			name: "neither configured",
			b:    internalpolicy.BudgetPolicy{},
			want: "(not configured)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeBudget(tc.b)
			if got != tc.want {
				t.Errorf("describeBudget() = %q; want %q", got, tc.want)
			}
		})
	}
}

// --- sameStringSet ---

func TestSameStringSet(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{name: "both empty", a: nil, b: nil, want: true},
		{name: "same order", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "different order", a: []string{"b", "a"}, b: []string{"a", "b"}, want: true},
		{name: "different elements", a: []string{"a", "b"}, b: []string{"a", "c"}, want: false},
		{name: "different lengths", a: []string{"a"}, b: []string{"a", "b"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sameStringSet(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("sameStringSet(%v, %v) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// --- samePriorN ---

func TestSamePriorN(t *testing.T) {
	cases := []struct {
		name string
		a, b *internalpolicy.PriorNRule
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "a nil", a: nil, b: &internalpolicy.PriorNRule{Tool: "t", Count: 1}, want: false},
		{name: "b nil", a: &internalpolicy.PriorNRule{Tool: "t", Count: 1}, b: nil, want: false},
		{
			name: "equal",
			a:    &internalpolicy.PriorNRule{Tool: "verify", Count: 2},
			b:    &internalpolicy.PriorNRule{Tool: "verify", Count: 2},
			want: true,
		},
		{
			name: "different count",
			a:    &internalpolicy.PriorNRule{Tool: "verify", Count: 1},
			b:    &internalpolicy.PriorNRule{Tool: "verify", Count: 2},
			want: false,
		},
		{
			name: "different tool",
			a:    &internalpolicy.PriorNRule{Tool: "verify", Count: 1},
			b:    &internalpolicy.PriorNRule{Tool: "approve", Count: 1},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := samePriorN(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("samePriorN() = %v; want %v", got, tc.want)
			}
		})
	}
}

// --- sameEscalateRule ---

func TestSameEscalateRule(t *testing.T) {
	cases := []struct {
		name string
		a, b *internalpolicy.EscalateRule
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "a nil", a: nil, b: &internalpolicy.EscalateRule{ArgumentPath: "$.x", Operator: ">", Value: 100}, want: false},
		{name: "b nil", a: &internalpolicy.EscalateRule{ArgumentPath: "$.x", Operator: ">", Value: 100}, b: nil, want: false},
		{
			name: "equal numeric value",
			a:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 10000},
			b:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 10000},
			want: true,
		},
		{
			name: "different operator",
			a:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 10000},
			b:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">=", Value: 10000},
			want: false,
		},
		{
			name: "different path",
			a:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 100},
			b:    &internalpolicy.EscalateRule{ArgumentPath: "$.total", Operator: ">", Value: 100},
			want: false,
		},
		{
			name: "different value",
			a:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 100},
			b:    &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 200},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sameEscalateRule(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("sameEscalateRule() = %v; want %v", got, tc.want)
			}
		})
	}
}

// --- printLintResults ---

func TestPrintLintResults_Colors(t *testing.T) {
	results := []internalpolicy.LintResult{
		{Code: "L001", Severity: internalpolicy.SeverityError, Message: "test error"},
		{Code: "L005", Severity: internalpolicy.SeverityWarning, Message: "test warning"},
		{Code: "L009", Severity: internalpolicy.SeverityInfo, Message: "test info"},
	}
	var buf bytes.Buffer
	errCount := printLintResults(&buf, results)

	if errCount != 1 {
		t.Errorf("errCount = %d; want 1", errCount)
	}
	out := buf.String()
	if !strings.Contains(out, "L001") {
		t.Error("output missing L001")
	}
	if !strings.Contains(out, "L005") {
		t.Error("output missing L005")
	}
	if !strings.Contains(out, "L009") {
		t.Error("output missing L009")
	}
	// Verify ANSI color codes are present.
	if !strings.Contains(out, ansiRed) {
		t.Error("output missing red ANSI code for ERROR")
	}
	if !strings.Contains(out, ansiYellow) {
		t.Error("output missing yellow ANSI code for WARNING")
	}
	if !strings.Contains(out, ansiCyan) {
		t.Error("output missing cyan ANSI code for INFO")
	}
}

func TestPrintLintResults_Empty(t *testing.T) {
	var buf bytes.Buffer
	errCount := printLintResults(&buf, nil)
	if errCount != 0 {
		t.Errorf("errCount = %d; want 0 for empty results", errCount)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty results, got %q", buf.String())
	}
}

// --- printExplain ---

func TestPrintExplain_MinimalPolicy(t *testing.T) {
	p, err := internalpolicy.ParseFile("../../testdata/minimal.policy.yaml")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var buf bytes.Buffer
	printExplain(&buf, p)
	out := buf.String()

	if !strings.Contains(out, "Agent: test-agent") {
		t.Errorf("output missing agent name; got:\n%s", out)
	}
	if !strings.Contains(out, "SHADOW") {
		t.Errorf("output missing SHADOW mode; got:\n%s", out)
	}
	if !strings.Contains(out, "Allowed tools (3)") {
		t.Errorf("output missing allowed tools count; got:\n%s", out)
	}
	// Minimal policy has no sequence/taint/escalation sections.
	if strings.Contains(out, "Sequence guards:") {
		t.Errorf("minimal policy should not print Sequence guards section; got:\n%s", out)
	}
}

func TestPrintExplain_AllSections(t *testing.T) {
	p := &internalpolicy.Policy{
		Agent:           "test-bot",
		EnforcementMode: internalpolicy.EnforcementBlock,
		MayUse:          []string{"tool_a", "tool_b", "tool_c"},
		Budget:          internalpolicy.BudgetPolicy{MaxToolCalls: 10, MaxCostUSD: 1.0},
		Tools: map[string]internalpolicy.ToolPolicy{
			"tool_b": {
				Sequence: internalpolicy.SequencePolicy{
					OnlyAfter:      []string{"tool_a"},
					NeverAfter:     []string{"tool_c"},
					RequiresPriorN: &internalpolicy.PriorNRule{Tool: "tool_a", Count: 2},
				},
				Taint:        internalpolicy.TaintPolicy{Applies: true, Label: "sensitive"},
				EscalateWhen: &internalpolicy.EscalateRule{ArgumentPath: "$.amount", Operator: ">", Value: 500},
			},
			"tool_c": {
				Taint: internalpolicy.TaintPolicy{Clears: true},
			},
		},
	}
	// Set fingerprint manually since we bypassed ParseFile.
	if _, err := internalpolicy.Fingerprint(p); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	var buf bytes.Buffer
	printExplain(&buf, p)
	out := buf.String()

	checks := []string{
		"Agent: test-bot",
		"BLOCK (violations are denied)",
		"Allowed tools (3)",
		"10 tool calls / $1.00 per session",
		"Sequence guards:",
		"tool_b: may only run after [tool_a]",
		"tool_b: blocked if tool_c was called this session",
		"tool_b: requires tool_a called at least 2 time(s)",
		"Taint rules:",
		"tool_b: taints the session (label: sensitive)",
		"tool_c: clears the taint",
		"Escalation rules:",
		"tool_b: escalate to human if amount > 500",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Errorf("output missing %q\nfull output:\n%s", check, out)
		}
	}
}

// --- printDiff ---

func TestPrintDiff_NoChanges(t *testing.T) {
	p, err := internalpolicy.ParseFile("../../testdata/minimal.policy.yaml")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var buf bytes.Buffer
	printDiff(&buf, p, p, "old.yaml", "new.yaml")
	out := buf.String()
	if !strings.Contains(out, "(no changes detected)") {
		t.Errorf("expected no-change message; got:\n%s", out)
	}
}

func TestPrintDiff_ModeChange(t *testing.T) {
	old := &internalpolicy.Policy{
		Version:         "1",
		Agent:           "bot",
		EnforcementMode: internalpolicy.EnforcementShadow,
		MayUse:          []string{"tool_a"},
		Tools:           map[string]internalpolicy.ToolPolicy{},
	}
	if _, err := internalpolicy.Fingerprint(old); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	new := &internalpolicy.Policy{
		Version:         "1",
		Agent:           "bot",
		EnforcementMode: internalpolicy.EnforcementBlock,
		MayUse:          []string{"tool_a"},
		Tools:           map[string]internalpolicy.ToolPolicy{},
	}
	if _, err := internalpolicy.Fingerprint(new); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	var buf bytes.Buffer
	printDiff(&buf, old, new, "old.yaml", "new.yaml")
	out := buf.String()
	if !strings.Contains(out, "shadow → block") {
		t.Errorf("expected mode change line; got:\n%s", out)
	}
}

func TestPrintDiff_AddedRemovedTools(t *testing.T) {
	base := &internalpolicy.Policy{
		Version: "1",
		Agent:   "bot",
		MayUse:  []string{"tool_a", "tool_b"},
		Tools:   map[string]internalpolicy.ToolPolicy{},
	}
	if _, err := internalpolicy.Fingerprint(base); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	updated := &internalpolicy.Policy{
		Version: "1",
		Agent:   "bot",
		MayUse:  []string{"tool_a", "tool_c"},
		Tools:   map[string]internalpolicy.ToolPolicy{},
	}
	if _, err := internalpolicy.Fingerprint(updated); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	var buf bytes.Buffer
	printDiff(&buf, base, updated, "old.yaml", "new.yaml")
	out := buf.String()
	if !strings.Contains(out, "+ tool_c (added)") {
		t.Errorf("expected added tool line; got:\n%s", out)
	}
	if !strings.Contains(out, "- tool_b (removed)") {
		t.Errorf("expected removed tool line; got:\n%s", out)
	}
}

func TestPrintDiff_BudgetChange(t *testing.T) {
	old := &internalpolicy.Policy{
		Version: "1",
		Agent:   "bot",
		MayUse:  []string{"tool_a"},
		Budget:  internalpolicy.BudgetPolicy{MaxToolCalls: 50, MaxCostUSD: 5.0},
		Tools:   map[string]internalpolicy.ToolPolicy{},
	}
	if _, err := internalpolicy.Fingerprint(old); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	new := &internalpolicy.Policy{
		Version: "1",
		Agent:   "bot",
		MayUse:  []string{"tool_a"},
		Budget:  internalpolicy.BudgetPolicy{MaxToolCalls: 100, MaxCostUSD: 10.0},
		Tools:   map[string]internalpolicy.ToolPolicy{},
	}
	if _, err := internalpolicy.Fingerprint(new); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	var buf bytes.Buffer
	printDiff(&buf, old, new, "old.yaml", "new.yaml")
	out := buf.String()
	if !strings.Contains(out, "max_tool_calls: 50 → 100") {
		t.Errorf("expected tool calls change; got:\n%s", out)
	}
	if !strings.Contains(out, "max_cost_usd: 5.00 → 10.00") {
		t.Errorf("expected cost change; got:\n%s", out)
	}
}

func TestPrintDiff_ToolPredicateChange(t *testing.T) {
	old := &internalpolicy.Policy{
		Version: "1",
		Agent:   "bot",
		MayUse:  []string{"tool_a", "tool_b"},
		Tools: map[string]internalpolicy.ToolPolicy{
			"tool_b": {
				Sequence: internalpolicy.SequencePolicy{
					OnlyAfter: []string{"tool_a"},
				},
			},
		},
	}
	if _, err := internalpolicy.Fingerprint(old); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	new := &internalpolicy.Policy{
		Version: "1",
		Agent:   "bot",
		MayUse:  []string{"tool_a", "tool_b"},
		Tools: map[string]internalpolicy.ToolPolicy{
			"tool_b": {
				// only_after cleared — tool_b no longer has a prerequisite.
				Sequence: internalpolicy.SequencePolicy{
					OnlyAfter: []string{},
				},
			},
		},
	}
	if _, err := internalpolicy.Fingerprint(new); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	var buf bytes.Buffer
	printDiff(&buf, old, new, "old.yaml", "new.yaml")
	out := buf.String()
	if !strings.Contains(out, "only_after") {
		t.Errorf("expected only_after change; got:\n%s", out)
	}
}
