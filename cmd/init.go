package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/cmd/initpacks"
	intpolicy "github.com/mercator-hq/truebearing/internal/policy"
)

// ANSI codes used only for the lint result summary printed by init.
// Mirrors the constants in cmd/policy/lint.go; duplication is intentional
// because the two commands are independent and sharing them would require
// exporting from cmd/policy, which is not warranted for two constants.
const (
	initAnsiReset  = "\033[0m"
	initAnsiRed    = "\033[31m"
	initAnsiYellow = "\033[33m"
	initAnsiCyan   = "\033[36m"
)

// verticalDescriptions maps a vertical identifier to the one-line description shown
// in the interactive selection menu.
var verticalDescriptions = map[string]string{
	"finance":       "payment processing, invoicing, financial transactions",
	"healthcare":    "PHI access, claims submission, patient records (HIPAA)",
	"legal":         "document review, matter management, legal research",
	"life-sciences": "FDA submissions, regulatory affairs, clinical trials",
	"devops":        "CI/CD pipelines, production deployments, infrastructure",
}

// newInitCommand returns the `truebearing init` subcommand.
//
// This is the entry point for an engineer who has never written a TrueBearing
// policy. A vertical selection routes to a pre-built policy pack; choosing
// "other" asks five questions and produces a blank template instead.
func newInitCommand() *cobra.Command {
	var outputPath string
	var vertical string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactively scaffold a new TrueBearing policy file",
		Long: `Generate a truebearing.policy.yaml from a pre-built policy pack or five questions.

Choose your agent's vertical to receive a policy pack tailored to that domain.
Choose "other" to answer five questions and build from a blank template.

No prior knowledge of the policy DSL is required. Policy packs are pre-set to
enforcement_mode: shadow (or block for PHI/regulatory use cases). Review the
generated file and the inline comments before enabling full enforcement.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), outputPath, vertical)
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "truebearing.policy.yaml",
		"path for the generated policy file")
	cmd.Flags().StringVar(&vertical, "vertical", "",
		"agent vertical: finance, healthcare, legal, life-sciences, devops, other (skips interactive question)")
	return cmd
}

// runInit drives the interactive scaffolding session, validates all inputs,
// generates or copies the appropriate policy YAML, and writes it to outputPath.
//
// vertical selects the policy pack to copy. Valid values are the identifiers
// returned by initpacks.KnownVerticals() plus "other". An empty string triggers
// an interactive vertical selection question before proceeding.
func runInit(out io.Writer, outputPath, vertical string) error {
	reader := bufio.NewReader(os.Stdin)

	// Warn if the output file already exists so the operator does not lose work.
	if _, statErr := os.Stat(outputPath); statErr == nil {
		fmt.Fprintf(out, "Warning: %s already exists and will be overwritten.\n", outputPath)
		fmt.Fprintf(out, "Press Enter to continue, or Ctrl-C to cancel: ")
		if _, err := reader.ReadString('\n'); err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
	}

	fmt.Fprintln(out, "TrueBearing Policy Scaffolder")
	fmt.Fprintln(out, "─────────────────────────────")

	// ── Question 0: Vertical selection ───────────────────────────────────────
	// When --vertical is provided via flag, validate and skip the interactive
	// prompt. "other" uses the blank template; any known vertical copies its pack.

	if vertical == "" {
		var err error
		vertical, err = promptVertical(out, reader)
		if err != nil {
			return err
		}
	} else {
		if !isKnownVertical(vertical) {
			return fmt.Errorf("unknown --vertical %q; valid values: finance, healthcare, legal, life-sciences, devops, other", vertical)
		}
	}

	// Policy-pack path: copy the matching pack and exit — no further questions.
	if vertical != "other" {
		return runInitFromPack(out, outputPath, vertical)
	}

	// Blank-template path: ask five questions and generate a policy from scratch.
	fmt.Fprintln(out, "Answer five questions to generate a policy file.")
	fmt.Fprintln(out, "Press Enter to accept the default shown in [brackets].")
	fmt.Fprintln(out)

	// ── Question 1: Agent name ────────────────────────────────────────────────

	agentName, err := prompt(out, reader, "1. Agent name (e.g. payments-agent)", "my-agent")
	if err != nil {
		return err
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return fmt.Errorf("agent name cannot be empty")
	}

	// ── Question 2: Tool list ────────────────────────────────────────────────

	fmt.Fprintln(out)
	toolsRaw, err := prompt(out, reader,
		"2. List the tools your agent uses (comma-separated)", "")
	if err != nil {
		return err
	}
	allTools := parseCSV(toolsRaw)
	if len(allTools) == 0 {
		return fmt.Errorf("at least one tool must be listed")
	}
	toolsSet := makeSet(allTools)

	// ── Question 3: High-risk tools ──────────────────────────────────────────

	fmt.Fprintln(out)
	fmt.Fprintf(out, "   Tools entered: %s\n", strings.Join(allTools, ", "))
	highRiskRaw, err := prompt(out, reader,
		"3. Which tools are high-risk and need sequence guards? (comma-separated, or Enter to skip)", "")
	if err != nil {
		return err
	}
	highRiskTools := parseCSV(highRiskRaw)
	for _, t := range highRiskTools {
		if !toolsSet[t] {
			return fmt.Errorf("high-risk tool %q is not in the tool list you entered", t)
		}
	}

	// ── Question 4: Prerequisites (only_after) per high-risk tool ────────────

	prerequisites := make(map[string][]string)
	for _, t := range highRiskTools {
		fmt.Fprintln(out)
		prereqRaw, err := prompt(out, reader,
			fmt.Sprintf("4. What must happen before %q? (comma-separated tool names, or Enter to skip)", t),
			"")
		if err != nil {
			return err
		}
		prereqs := parseCSV(prereqRaw)
		for _, p := range prereqs {
			if !toolsSet[p] {
				return fmt.Errorf("prerequisite %q for %q is not in the tool list", p, t)
			}
		}
		prerequisites[t] = prereqs
	}

	// ── Question 5: Budget ───────────────────────────────────────────────────

	fmt.Fprintln(out)
	maxCallsStr, err := prompt(out, reader,
		"5. Maximum tool calls per session", "50")
	if err != nil {
		return err
	}
	maxCalls, err := parsePositiveInt(maxCallsStr, 50)
	if err != nil {
		return fmt.Errorf("max tool calls: %w", err)
	}

	maxCostStr, err := prompt(out, reader,
		"   Maximum cost per session (USD)", "5.00")
	if err != nil {
		return err
	}
	maxCost, err := parsePositiveFloat(maxCostStr, 5.00)
	if err != nil {
		return fmt.Errorf("max cost USD: %w", err)
	}

	// ── Generate and validate before writing ─────────────────────────────────

	yamlContent := generatePolicyYAML(agentName, allTools, highRiskTools, prerequisites, maxCalls, maxCost)

	// Design: validate the generated YAML before writing to disk so that any
	// lint ERRORs (e.g. L013 circular dependency from two high-risk tools that
	// list each other as prerequisites) are caught without producing a broken
	// file. ParseBytes exercises the same code path as ParseFile.
	parsed, parseErr := intpolicy.ParseBytes([]byte(yamlContent), outputPath)
	if parseErr != nil {
		// A parse error here is a bug in the scaffolder, not a user mistake —
		// the generated YAML is always structurally valid.
		return fmt.Errorf("scaffolder produced invalid YAML (bug — please report): %w", parseErr)
	}

	results := intpolicy.Lint(parsed)

	// Count ERRORs before writing. If any exist, surface them and abort so
	// the operator is not left with a file that fails lint.
	errCount := 0
	for _, r := range results {
		if r.Severity == intpolicy.SeverityError {
			errCount++
		}
	}
	if errCount > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "The policy you described has errors. No file was written.")
		fmt.Fprintln(out)
		printInitLintResults(out, results)
		return fmt.Errorf("%d error(s) found in the generated policy; see output above", errCount)
	}

	// Write to disk only after validation passes.
	if err := os.WriteFile(outputPath, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("writing policy file: %w", err)
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "✓ Created %s\n", outputPath)

	// Print any warnings or info diagnostics (no ERRORs at this point).
	if len(results) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Lint results:")
		printInitLintResults(out, results)
	}

	// ── Next-steps checklist ─────────────────────────────────────────────────

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintf(out, "  1. truebearing agent register %s --policy %s\n", agentName, outputPath)
	fmt.Fprintf(out, "  2. truebearing serve --upstream <your-mcp-url> --policy %s\n", outputPath)
	fmt.Fprintln(out, "  3. Run your agent for a week in shadow mode")
	fmt.Fprintln(out, "  4. truebearing audit query --decision shadow_deny   (review what would have been blocked)")
	fmt.Fprintf(out, "  5. Set enforcement_mode: block in %s when you're ready\n", outputPath)

	return nil
}

// generatePolicyYAML produces the YAML text for the scaffolded policy.
// Inline comments make the generated file self-documenting for an operator
// who has never seen the TrueBearing DSL before.
//
// Design: we build the YAML as a formatted string rather than marshaling Go
// structs because yaml.Marshal cannot emit inline comments. The operator-
// facing documentation value of the comments outweighs the minor cost of
// hand-formatting the output.
func generatePolicyYAML(
	agentName string,
	allTools, highRiskTools []string,
	prerequisites map[string][]string,
	maxCalls int,
	maxCost float64,
) string {
	var b strings.Builder
	highRiskSet := makeSet(highRiskTools)
	allToolsLookup := makeSet(allTools)

	fmt.Fprintf(&b, "# Generated by: truebearing init\n")
	fmt.Fprintf(&b, "# Review this file, then run: truebearing policy explain %s\n", agentName+".policy.yaml")
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# SHADOW MODE: policy violations are logged but not blocked.\n")
	fmt.Fprintf(&b, "# After reviewing shadow_deny audit records for a week, change\n")
	fmt.Fprintf(&b, "# enforcement_mode to block to enable production enforcement.\n")
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "version: \"1\"\n")
	fmt.Fprintf(&b, "agent: %s\n", agentName)
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "# shadow: log violations, allow calls — safe for onboarding and policy tuning\n")
	fmt.Fprintf(&b, "# block:  deny on any violation — use in production after shadow-mode review\n")
	fmt.Fprintf(&b, "enforcement_mode: shadow\n")
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "session:\n")
	fmt.Fprintf(&b, "  max_history: 1000          # hard cap; start a new session when reached\n")
	fmt.Fprintf(&b, "  max_duration_seconds: 3600  # 1 hour per session; adjust to your workflow\n")
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "budget:\n")
	fmt.Fprintf(&b, "  max_tool_calls: %d\n", maxCalls)
	fmt.Fprintf(&b, "  max_cost_usd: %.2f\n", maxCost)
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "# Complete list of tools this agent is permitted to call.\n")
	fmt.Fprintf(&b, "# Any tool not in this list is denied before any other check runs.\n")
	fmt.Fprintf(&b, "may_use:\n")
	for _, t := range allTools {
		fmt.Fprintf(&b, "  - %s\n", t)
	}
	// check_escalation_status is a TrueBearing-injected virtual tool required
	// for the escalation polling loop. Add it unless the operator already
	// included it explicitly in their tool list.
	if !allToolsLookup["check_escalation_status"] {
		fmt.Fprintf(&b, "  - check_escalation_status  # TrueBearing virtual tool; required for escalation polling\n")
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "tools:\n")

	// High-risk tools appear first with sequence guards and enforcement_mode: block.
	// Even when global mode is shadow, these tools always enforce — they may
	// execute irreversible real-world actions.
	for _, t := range highRiskTools {
		fmt.Fprintf(&b, "  %s:\n", t)
		fmt.Fprintf(&b, "    # Block this tool even when global enforcement_mode is shadow:\n")
		fmt.Fprintf(&b, "    # it may execute an irreversible real-world action.\n")
		fmt.Fprintf(&b, "    enforcement_mode: block\n")
		if prereqs := prerequisites[t]; len(prereqs) > 0 {
			fmt.Fprintf(&b, "    sequence:\n")
			fmt.Fprintf(&b, "      only_after:\n")
			for _, p := range prereqs {
				fmt.Fprintf(&b, "        - %s\n", p)
			}
		}
		fmt.Fprintf(&b, "\n")
	}

	// Low-risk tools are emitted as empty entries, which the engine treats as
	// allowed with no restrictions beyond the may_use check and budget.
	// check_escalation_status is a virtual tool and needs no entry in tools:.
	hasLowRisk := false
	for _, t := range allTools {
		if highRiskSet[t] || t == "check_escalation_status" {
			continue
		}
		if !hasLowRisk {
			fmt.Fprintf(&b, "  # Tools below have no restrictions beyond may_use and budget.\n")
			hasLowRisk = true
		}
		fmt.Fprintf(&b, "  %s: {}\n", t)
	}

	return b.String()
}

// isKnownVertical returns true when v is one of the accepted vertical identifiers,
// including "other" for the blank-template path.
func isKnownVertical(v string) bool {
	if v == "other" {
		return true
	}
	for _, k := range initpacks.KnownVerticals() {
		if k == v {
			return true
		}
	}
	return false
}

// promptVertical displays the vertical selection menu, reads one line from
// reader, and returns the corresponding vertical identifier.
// The default selection is "other" (blank template).
func promptVertical(out io.Writer, reader *bufio.Reader) (string, error) {
	knownVerticals := initpacks.KnownVerticals()

	fmt.Fprintln(out)
	fmt.Fprintln(out, "0. What best describes your agent?")
	for i, v := range knownVerticals {
		fmt.Fprintf(out, "   %d. %-20s — %s\n", i+1, v, verticalDescriptions[v])
	}
	n := len(knownVerticals)
	fmt.Fprintf(out, "   %d. other               — start from a blank template (answer five questions)\n", n+1)
	fmt.Fprintln(out)

	line, err := prompt(out, reader, fmt.Sprintf("Enter 1–%d", n+1), fmt.Sprintf("%d", n+1))
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)

	// Empty input (user pressed Enter) → default is "other".
	if line == "" || line == fmt.Sprintf("%d", n+1) {
		return "other", nil
	}

	choice, err := strconv.Atoi(line)
	if err != nil || choice < 1 || choice > n+1 {
		return "", fmt.Errorf("invalid selection %q; enter a number between 1 and %d", line, n+1)
	}
	if choice == n+1 {
		return "other", nil
	}
	return knownVerticals[choice-1], nil
}

// runInitFromPack copies the embedded policy pack for the given vertical to
// outputPath, runs the linter, and prints the result. No interactive questions
// are asked — the operator customises the generated file after init exits.
func runInitFromPack(out io.Writer, outputPath, vertical string) error {
	content, ok := initpacks.ByVertical(vertical)
	if !ok {
		// Should not happen: caller already validated via isKnownVertical.
		return fmt.Errorf("no policy pack found for vertical %q", vertical)
	}

	parsed, err := intpolicy.ParseBytes(content, outputPath)
	if err != nil {
		// A parse error here is a bug in the embedded policy pack, not a user mistake.
		return fmt.Errorf("embedded policy pack for %q is invalid (bug — please report): %w", vertical, err)
	}

	results := intpolicy.Lint(parsed)

	// Count errors before writing. Any lint ERROR in an embedded pack is a
	// ship-stopping bug: the pack must lint clean before it can be distributed.
	errCount := 0
	for _, r := range results {
		if r.Severity == intpolicy.SeverityError {
			errCount++
		}
	}
	if errCount > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "The embedded %q policy pack has lint errors (bug — please report):\n", vertical)
		fmt.Fprintln(out)
		printInitLintResults(out, results)
		return fmt.Errorf("embedded pack for %q has %d lint error(s); file not written", vertical, errCount)
	}

	if err := os.WriteFile(outputPath, content, 0644); err != nil {
		return fmt.Errorf("writing policy file: %w", err)
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "✓ Created %s (from %s policy pack)\n", outputPath, vertical)

	if len(results) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Lint results:")
		printInitLintResults(out, results)
	}

	agentName := parsed.Agent
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  1. Edit the tool names in the generated file to match your actual MCP tools")
	fmt.Fprintf(out, "  2. truebearing agent register %s --policy %s\n", agentName, outputPath)
	fmt.Fprintf(out, "  3. truebearing serve --upstream <your-mcp-url> --policy %s\n", outputPath)
	fmt.Fprintln(out, "  4. Run your agent and review: truebearing audit query --decision shadow_deny")

	return nil
}

// prompt writes the question with its default value to out, reads a line from
// reader, and returns the trimmed answer. If the answer is empty, defaultVal
// is returned.
func prompt(out io.Writer, reader *bufio.Reader, question, defaultVal string) (string, error) {
	if defaultVal != "" {
		fmt.Fprintf(out, "%s [%s]: ", question, defaultVal)
	} else {
		fmt.Fprintf(out, "%s: ", question)
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// parseCSV splits a comma-separated string, trims whitespace from each token,
// removes empty tokens, and deduplicates while preserving first-seen order.
func parseCSV(s string) []string {
	parts := strings.Split(s, ",")
	seen := make(map[string]bool)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// makeSet converts a string slice into a membership set for O(1) lookups.
func makeSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// parsePositiveInt parses s as a positive integer. Returns defaultVal when s
// is empty. Returns an error if s is not a valid positive integer.
func parsePositiveInt(s string, defaultVal int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a positive integer", s)
	}
	return n, nil
}

// parsePositiveFloat parses s as a positive float64. Returns defaultVal when
// s is empty. Returns an error if s is not a valid positive number.
func parsePositiveFloat(s string, defaultVal float64) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultVal, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("%q is not a positive number", s)
	}
	return f, nil
}

// printInitLintResults writes lint diagnostics to out using ANSI colour codes.
// Mirrors printLintResults in cmd/policy/lint.go; the duplication is intentional
// to keep the two commands independent.
func printInitLintResults(out io.Writer, results []intpolicy.LintResult) {
	for _, r := range results {
		color := initAnsiCyan
		switch r.Severity {
		case intpolicy.SeverityError:
			color = initAnsiRed
		case intpolicy.SeverityWarning:
			color = initAnsiYellow
		}
		fmt.Fprintf(out, "%s%s [%s]%s %s\n", color, r.Code, r.Severity, initAnsiReset, r.Message)
	}
}
