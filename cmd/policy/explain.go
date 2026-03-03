package policy

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/policy"
)

// newExplainCommand returns the `policy explain` subcommand.
func newExplainCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <file>",
		Short: "Print a plain-English summary of what a policy enforces",
		Long: `Parse a policy file and render a human-readable summary of every
rule it contains — enforcement mode, allowed tools, sequence guards,
taint rules, budget, and escalation thresholds.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := policy.ParseFile(args[0])
			if err != nil {
				return err
			}
			printExplain(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

// printExplain renders the structured plain-English policy summary to w.
// The output format matches mvp-plan.md §13 exactly so operators can read any
// policy file without needing to understand the YAML schema.
func printExplain(w io.Writer, p *policy.Policy) {
	// Header block: fixed fields always present.
	fmt.Fprintf(w, "Agent: %s\n", p.Agent)
	fmt.Fprintf(w, "Mode: %s\n", describeMode(p.EnforcementMode))
	fmt.Fprintf(w, "Allowed tools (%d): %s\n", len(p.MayUse), strings.Join(p.MayUse, ", "))
	fmt.Fprintf(w, "Budget: %s\n", describeBudget(p.Budget))

	// Optional sections — each is separated from the header and from each other
	// by a blank line only when the section has at least one item to show.
	toolNames := sortedKeys(p.Tools)

	// Sequence guards: only_after, never_after, requires_prior_n.
	var seqLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if len(tp.Sequence.OnlyAfter) > 0 {
			seqLines = append(seqLines, fmt.Sprintf(
				"  %s: may only run after [%s]",
				name, strings.Join(tp.Sequence.OnlyAfter, ", "),
			))
		}
		// One line per never_after entry so each blocked dependency is explicit.
		for _, blocked := range tp.Sequence.NeverAfter {
			seqLines = append(seqLines, fmt.Sprintf(
				"  %s: blocked if %s was called this session",
				name, blocked,
			))
		}
		if tp.Sequence.RequiresPriorN != nil {
			seqLines = append(seqLines, fmt.Sprintf(
				"  %s: requires %s called at least %d time(s)",
				name, tp.Sequence.RequiresPriorN.Tool, tp.Sequence.RequiresPriorN.Count,
			))
		}
	}
	if len(seqLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Sequence guards:")
		for _, line := range seqLines {
			fmt.Fprintln(w, line)
		}
	}

	// Taint rules: applies and clears.
	var taintLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if tp.Taint.Applies {
			label := ""
			if tp.Taint.Label != "" {
				label = fmt.Sprintf(" (label: %s)", tp.Taint.Label)
			}
			taintLines = append(taintLines, fmt.Sprintf("  %s: taints the session%s", name, label))
		}
		if tp.Taint.Clears {
			taintLines = append(taintLines, fmt.Sprintf("  %s: clears the taint", name))
		}
	}
	if len(taintLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Taint rules:")
		for _, line := range taintLines {
			fmt.Fprintln(w, line)
		}
	}

	// Escalation rules: escalate_when conditions.
	var escLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if tp.EscalateWhen != nil {
			// Strip the JSONPath "$." prefix for display; leave other path forms intact.
			path := strings.TrimPrefix(tp.EscalateWhen.ArgumentPath, "$.")
			escLines = append(escLines, fmt.Sprintf(
				"  %s: escalate to human if %s %s %v",
				name, path, tp.EscalateWhen.Operator, tp.EscalateWhen.Value,
			))
		}
	}
	if len(escLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Escalation rules:")
		for _, line := range escLines {
			fmt.Fprintln(w, line)
		}
	}

	// Content guards: never_when predicates with explicit match-mode labelling.
	var contentLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if len(tp.NeverWhen) == 0 {
			continue
		}
		matchLabel := describeMatchMode(tp.NeverWhenMatch)
		contentLines = append(contentLines, fmt.Sprintf("  %s: %s", name, matchLabel))
		for _, pred := range tp.NeverWhen {
			contentLines = append(contentLines, fmt.Sprintf(
				"    - argument %q %s %q",
				pred.Argument, pred.Operator, pred.Value,
			))
		}
	}
	if len(contentLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Content guards:")
		for _, line := range contentLines {
			fmt.Fprintln(w, line)
		}
	}
}

// describeMatchMode returns a plain-English intro phrase for a never_when block's
// match mode, suitable for prefixing the list of predicates in explain output.
func describeMatchMode(mode policy.ContentMatchMode) string {
	switch mode {
	case policy.ContentMatchAll:
		return "blocked only if ALL of:"
	default:
		// Empty string or "any" — both mean OR logic (backward-compatible default).
		return "blocked if ANY of:"
	}
}

// describeMode returns a human-readable label for an enforcement mode,
// matching the format shown in mvp-plan.md §13.
func describeMode(mode policy.EnforcementMode) string {
	switch mode {
	case policy.EnforcementBlock:
		return "BLOCK (violations are denied)"
	case policy.EnforcementShadow:
		return "SHADOW (violations are logged but not blocked)"
	default:
		// Empty enforcement_mode defaults to shadow at runtime.
		return "SHADOW (default; violations are logged but not blocked)"
	}
}

// describeBudget formats a BudgetPolicy as a human-readable string.
// Omits whichever limits are not configured (zero value = not set).
func describeBudget(b policy.BudgetPolicy) string {
	hasCalls := b.MaxToolCalls > 0
	hasCost := b.MaxCostUSD > 0
	switch {
	case hasCalls && hasCost:
		return fmt.Sprintf("%d tool calls / $%.2f per session", b.MaxToolCalls, b.MaxCostUSD)
	case hasCalls:
		return fmt.Sprintf("%d tool calls per session", b.MaxToolCalls)
	case hasCost:
		return fmt.Sprintf("$%.2f per session", b.MaxCostUSD)
	default:
		return "(not configured)"
	}
}

// sortedKeys returns the keys of tools in alphabetical order.
// Map iteration in Go is non-deterministic; sorting ensures stable output
// across runs and makes explain output easy to compare in diffs.
func sortedKeys(tools map[string]policy.ToolPolicy) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
