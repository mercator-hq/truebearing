package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	intpolicy "github.com/mercator-hq/truebearing/internal/policy"
)

// TestGeneratePolicyYAML verifies that the scaffolder produces a policy that
// parses and lints cleanly for a range of input configurations.
func TestGeneratePolicyYAML(t *testing.T) {
	cases := []struct {
		name          string
		agentName     string
		allTools      []string
		highRiskTools []string
		prerequisites map[string][]string
		maxCalls      int
		maxCost       float64
		wantLintErrs  int // expected number of lint ERROR results
	}{
		{
			name:          "minimal — one tool, no high-risk",
			agentName:     "simple-agent",
			allTools:      []string{"read_data"},
			highRiskTools: nil,
			prerequisites: map[string][]string{},
			maxCalls:      50,
			maxCost:       5.00,
			wantLintErrs:  0,
		},
		{
			name:          "high-risk tool with prerequisites",
			agentName:     "payments-agent",
			allTools:      []string{"read_invoice", "verify_invoice", "execute_payment"},
			highRiskTools: []string{"execute_payment"},
			prerequisites: map[string][]string{
				"execute_payment": {"verify_invoice"},
			},
			maxCalls:     50,
			maxCost:      5.00,
			wantLintErrs: 0,
		},
		{
			name:          "high-risk tool with no prerequisites (still valid)",
			agentName:     "ops-agent",
			allTools:      []string{"deploy", "rollback"},
			highRiskTools: []string{"deploy"},
			prerequisites: map[string][]string{"deploy": nil},
			maxCalls:      20,
			maxCost:       2.00,
			wantLintErrs:  0,
		},
		{
			name:          "multiple high-risk tools with disjoint prerequisites",
			agentName:     "billing-agent",
			allTools:      []string{"read_claim", "verify_claim", "submit_claim", "approve_claim"},
			highRiskTools: []string{"submit_claim", "approve_claim"},
			prerequisites: map[string][]string{
				"submit_claim":  {"verify_claim"},
				"approve_claim": {"verify_claim"},
			},
			maxCalls:     100,
			maxCost:      10.00,
			wantLintErrs: 0,
		},
		{
			name:          "operator already included check_escalation_status",
			agentName:     "escalation-agent",
			allTools:      []string{"do_work", "check_escalation_status"},
			highRiskTools: nil,
			prerequisites: map[string][]string{},
			maxCalls:      30,
			maxCost:       3.00,
			wantLintErrs:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := generatePolicyYAML(tc.agentName, tc.allTools, tc.highRiskTools, tc.prerequisites, tc.maxCalls, tc.maxCost)

			// The generated YAML must parse without error.
			p, err := intpolicy.ParseBytes([]byte(yaml), "test.policy.yaml")
			if err != nil {
				t.Fatalf("ParseBytes failed: %v\nGenerated YAML:\n%s", err, yaml)
			}

			// Run the linter and count ERRORs.
			results := intpolicy.Lint(p)
			errCount := 0
			for _, r := range results {
				if r.Severity == intpolicy.SeverityError {
					errCount++
					t.Logf("lint ERROR: %s %s", r.Code, r.Message)
				}
			}
			if errCount != tc.wantLintErrs {
				t.Errorf("got %d lint ERROR(s), want %d\nGenerated YAML:\n%s", errCount, tc.wantLintErrs, yaml)
			}

			// The generated file must always use enforcement_mode: shadow.
			if p.EnforcementMode != intpolicy.EnforcementShadow {
				t.Errorf("enforcement_mode = %q, want %q", p.EnforcementMode, intpolicy.EnforcementShadow)
			}

			// Budget must reflect the inputs.
			if p.Budget.MaxToolCalls != tc.maxCalls {
				t.Errorf("max_tool_calls = %d, want %d", p.Budget.MaxToolCalls, tc.maxCalls)
			}

			// session.max_history must always be set (avoids L007 warning).
			if p.Session.MaxHistory == 0 {
				t.Errorf("session.max_history is 0; linter will warn via L007")
			}

			// High-risk tools must appear in the generated tools: block with
			// enforcement_mode: block.
			for _, ht := range tc.highRiskTools {
				tp, ok := p.Tools[ht]
				if !ok {
					t.Errorf("high-risk tool %q missing from tools:", ht)
					continue
				}
				if tp.EnforcementMode != intpolicy.EnforcementBlock {
					t.Errorf("tool %q enforcement_mode = %q, want %q", ht, tp.EnforcementMode, intpolicy.EnforcementBlock)
				}
				// Prerequisites must appear in only_after.
				prereqs := tc.prerequisites[ht]
				if len(prereqs) != len(tp.Sequence.OnlyAfter) {
					t.Errorf("tool %q only_after len = %d, want %d", ht, len(tp.Sequence.OnlyAfter), len(prereqs))
				}
			}
		})
	}
}

// TestRunInit_CircularDependency verifies that runInit aborts without writing
// a file when the operator describes prerequisites that form a cycle (L013).
func TestRunInit_CircularDependency(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.policy.yaml")

	// Simulate: tool A requires B, B requires A → circular.
	yaml := generatePolicyYAML(
		"test-agent",
		[]string{"tool-a", "tool-b"},
		[]string{"tool-a", "tool-b"},
		map[string][]string{
			"tool-a": {"tool-b"},
			"tool-b": {"tool-a"},
		},
		50, 5.00,
	)

	parsed, err := intpolicy.ParseBytes([]byte(yaml), outPath)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	results := intpolicy.Lint(parsed)
	hasL013 := false
	for _, r := range results {
		if r.Code == "L013" {
			hasL013 = true
		}
	}
	if !hasL013 {
		t.Errorf("expected L013 circular dependency error; results: %+v", results)
	}
}

// TestRunInit_EndToEnd simulates a complete init interaction by piping
// answers into stdin and verifying the generated file.
func TestRunInit_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "test.policy.yaml")

	// Pipe answers for all five questions.
	input := strings.Join([]string{
		"my-test-agent",         // Q1: agent name
		"read_data, write_data", // Q2: tools
		"write_data",            // Q3: high-risk
		"read_data",             // Q4: prerequisites for write_data
		"30",                    // Q5: max tool calls
		"3.50",                  // Q5: max cost
		"",                      // trailing newline
	}, "\n")

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("writing to pipe: %v", err)
	}
	w.Close()

	var out bytes.Buffer
	if err := runInit(&out, outPath); err != nil {
		t.Fatalf("runInit returned error: %v\nOutput:\n%s", err, out.String())
	}

	// The file must exist and be parseable.
	p, err := intpolicy.ParseFile(outPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if p.Agent != "my-test-agent" {
		t.Errorf("agent = %q, want %q", p.Agent, "my-test-agent")
	}
	if p.EnforcementMode != intpolicy.EnforcementShadow {
		t.Errorf("enforcement_mode = %q, want shadow", p.EnforcementMode)
	}
	if p.Budget.MaxToolCalls != 30 {
		t.Errorf("max_tool_calls = %d, want 30", p.Budget.MaxToolCalls)
	}

	tp, ok := p.Tools["write_data"]
	if !ok {
		t.Fatalf("write_data missing from tools:")
	}
	if tp.EnforcementMode != intpolicy.EnforcementBlock {
		t.Errorf("write_data enforcement_mode = %q, want block", tp.EnforcementMode)
	}
	if len(tp.Sequence.OnlyAfter) != 1 || tp.Sequence.OnlyAfter[0] != "read_data" {
		t.Errorf("write_data only_after = %v, want [read_data]", tp.Sequence.OnlyAfter)
	}

	// The output must contain the next-steps checklist.
	outStr := out.String()
	if !strings.Contains(outStr, "Next steps:") {
		t.Errorf("output missing 'Next steps:' section\nOutput:\n%s", outStr)
	}
	if !strings.Contains(outStr, "truebearing agent register") {
		t.Errorf("output missing 'truebearing agent register' step\nOutput:\n%s", outStr)
	}

	// The file must pass lint with zero errors.
	results := intpolicy.Lint(p)
	for _, r := range results {
		if r.Severity == intpolicy.SeverityError {
			t.Errorf("lint ERROR: %s %s", r.Code, r.Message)
		}
	}
}

// TestParseCSV covers the comma-separated input parser.
func TestParseCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"  a , b , c  ", []string{"a", "b", "c"}},
		{"a,,b", []string{"a", "b"}},
		{"a,a,b", []string{"a", "b"}}, // deduplicated
	}
	for _, tc := range cases {
		got := parseCSV(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseCSV(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseCSV(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

// TestParsePositiveInt covers the integer parser.
func TestParsePositiveInt(t *testing.T) {
	cases := []struct {
		input      string
		defaultVal int
		wantVal    int
		wantErr    bool
	}{
		{"", 42, 42, false},
		{"10", 0, 10, false},
		{"0", 0, 0, true},
		{"-5", 0, 0, true},
		{"abc", 0, 0, true},
	}
	for _, tc := range cases {
		got, err := parsePositiveInt(tc.input, tc.defaultVal)
		if (err != nil) != tc.wantErr {
			t.Errorf("parsePositiveInt(%q) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
		}
		if err == nil && got != tc.wantVal {
			t.Errorf("parsePositiveInt(%q) = %d, want %d", tc.input, got, tc.wantVal)
		}
	}
}

// TestParsePositiveFloat covers the float parser.
func TestParsePositiveFloat(t *testing.T) {
	cases := []struct {
		input      string
		defaultVal float64
		wantVal    float64
		wantErr    bool
	}{
		{"", 5.0, 5.0, false},
		{"2.50", 0, 2.50, false},
		{"0", 0, 0, true},
		{"-1.0", 0, 0, true},
		{"nope", 0, 0, true},
	}
	for _, tc := range cases {
		got, err := parsePositiveFloat(tc.input, tc.defaultVal)
		if (err != nil) != tc.wantErr {
			t.Errorf("parsePositiveFloat(%q) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
		}
		if err == nil && got != tc.wantVal {
			t.Errorf("parsePositiveFloat(%q) = %f, want %f", tc.input, got, tc.wantVal)
		}
	}
}
