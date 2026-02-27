package policy

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/policy"
)

// newDiffCommand returns the `policy diff` subcommand.
func newDiffCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <old-file> <new-file>",
		Short: "Show rule changes between two policy files",
		Long: `Compare two policy YAML files and print a structured diff showing:
added or removed tools, changed sequence predicates, changed budget
limits, and enforcement mode changes.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldPolicy, err := policy.ParseFile(args[0])
			if err != nil {
				return fmt.Errorf("parsing old policy: %w", err)
			}
			newPolicy, err := policy.ParseFile(args[1])
			if err != nil {
				return fmt.Errorf("parsing new policy: %w", err)
			}
			printDiff(cmd.OutOrStdout(), oldPolicy, newPolicy, args[0], args[1])
			return nil
		},
	}
}

// printDiff renders the structured diff between two policies to w. Every changed
// field is listed; unchanged fields are omitted. If nothing changed, a single
// "(no changes detected)" line is printed.
func printDiff(w io.Writer, old, new *policy.Policy, oldPath, newPath string) {
	fmt.Fprintln(w, "Policy diff")
	fmt.Fprintf(w, "  old: %s  [fingerprint: %s]\n", oldPath, old.ShortFingerprint())
	fmt.Fprintf(w, "  new: %s  [fingerprint: %s]\n", newPath, new.ShortFingerprint())
	fmt.Fprintln(w, strings.Repeat("─", 60))

	changed := false

	// Enforcement mode.
	if old.EnforcementMode != new.EnforcementMode {
		fmt.Fprintf(w, "\nMode: %s → %s\n", old.EnforcementMode, new.EnforcementMode)
		changed = true
	}

	// May-use: show added and removed tool names.
	oldMayUse := toSet(old.MayUse)
	newMayUse := toSet(new.MayUse)
	var added, removed []string
	for _, t := range new.MayUse {
		if !oldMayUse[t] {
			added = append(added, t)
		}
	}
	for _, t := range old.MayUse {
		if !newMayUse[t] {
			removed = append(removed, t)
		}
	}
	if len(added) > 0 || len(removed) > 0 {
		fmt.Fprintln(w, "\nMay-use:")
		sort.Strings(added)
		for _, t := range added {
			fmt.Fprintf(w, "  + %s (added)\n", t)
		}
		sort.Strings(removed)
		for _, t := range removed {
			fmt.Fprintf(w, "  - %s (removed)\n", t)
		}
		changed = true
	}

	// Budget limits.
	var budgetLines []string
	if old.Budget.MaxToolCalls != new.Budget.MaxToolCalls {
		budgetLines = append(budgetLines, fmt.Sprintf(
			"  max_tool_calls: %d → %d",
			old.Budget.MaxToolCalls, new.Budget.MaxToolCalls,
		))
	}
	if old.Budget.MaxCostUSD != new.Budget.MaxCostUSD {
		budgetLines = append(budgetLines, fmt.Sprintf(
			"  max_cost_usd: %.2f → %.2f",
			old.Budget.MaxCostUSD, new.Budget.MaxCostUSD,
		))
	}
	if len(budgetLines) > 0 {
		fmt.Fprintln(w, "\nBudget:")
		for _, line := range budgetLines {
			fmt.Fprintln(w, line)
		}
		changed = true
	}

	// Session limits.
	var sessionLines []string
	if old.Session.MaxHistory != new.Session.MaxHistory {
		sessionLines = append(sessionLines, fmt.Sprintf(
			"  max_history: %d → %d",
			old.Session.MaxHistory, new.Session.MaxHistory,
		))
	}
	if old.Session.MaxDurationSeconds != new.Session.MaxDurationSeconds {
		sessionLines = append(sessionLines, fmt.Sprintf(
			"  max_duration_seconds: %d → %d",
			old.Session.MaxDurationSeconds, new.Session.MaxDurationSeconds,
		))
	}
	if len(sessionLines) > 0 {
		fmt.Fprintln(w, "\nSession:")
		for _, line := range sessionLines {
			fmt.Fprintln(w, line)
		}
		changed = true
	}

	// Per-tool changes: added tools, removed tools, and changed predicates.
	allNames := unionKeys(old.Tools, new.Tools)
	var toolLines []string
	for _, name := range allNames {
		oldTool, inOld := old.Tools[name]
		newTool, inNew := new.Tools[name]
		if !inOld {
			toolLines = append(toolLines, fmt.Sprintf("  + %s (added)", name))
			continue
		}
		if !inNew {
			toolLines = append(toolLines, fmt.Sprintf("  - %s (removed)", name))
			continue
		}
		// Both exist — compare individual fields and collect any changes.
		var changes []string
		if oldTool.EnforcementMode != newTool.EnforcementMode {
			changes = append(changes, fmt.Sprintf(
				"    enforcement_mode: %q → %q",
				oldTool.EnforcementMode, newTool.EnforcementMode,
			))
		}
		if !sameStringSet(oldTool.Sequence.OnlyAfter, newTool.Sequence.OnlyAfter) {
			changes = append(changes, fmt.Sprintf(
				"    only_after: [%s] → [%s]",
				strings.Join(sortedCopy(oldTool.Sequence.OnlyAfter), ", "),
				strings.Join(sortedCopy(newTool.Sequence.OnlyAfter), ", "),
			))
		}
		if !sameStringSet(oldTool.Sequence.NeverAfter, newTool.Sequence.NeverAfter) {
			changes = append(changes, fmt.Sprintf(
				"    never_after: [%s] → [%s]",
				strings.Join(sortedCopy(oldTool.Sequence.NeverAfter), ", "),
				strings.Join(sortedCopy(newTool.Sequence.NeverAfter), ", "),
			))
		}
		if !samePriorN(oldTool.Sequence.RequiresPriorN, newTool.Sequence.RequiresPriorN) {
			changes = append(changes, fmt.Sprintf(
				"    requires_prior_n: %s → %s",
				formatPriorN(oldTool.Sequence.RequiresPriorN),
				formatPriorN(newTool.Sequence.RequiresPriorN),
			))
		}
		if oldTool.Taint != newTool.Taint {
			changes = append(changes, fmt.Sprintf(
				"    taint: {applies:%v, clears:%v, label:%q} → {applies:%v, clears:%v, label:%q}",
				oldTool.Taint.Applies, oldTool.Taint.Clears, oldTool.Taint.Label,
				newTool.Taint.Applies, newTool.Taint.Clears, newTool.Taint.Label,
			))
		}
		if !sameEscalateRule(oldTool.EscalateWhen, newTool.EscalateWhen) {
			changes = append(changes, fmt.Sprintf(
				"    escalate_when: %s → %s",
				formatEscalateRule(oldTool.EscalateWhen),
				formatEscalateRule(newTool.EscalateWhen),
			))
		}
		if len(changes) > 0 {
			toolLines = append(toolLines, fmt.Sprintf("  %s:", name))
			toolLines = append(toolLines, changes...)
		}
	}
	if len(toolLines) > 0 {
		fmt.Fprintln(w, "\nTools:")
		for _, line := range toolLines {
			fmt.Fprintln(w, line)
		}
		changed = true
	}

	if !changed {
		fmt.Fprintln(w, "\n(no changes detected)")
	}
}

// toSet converts a string slice to a boolean membership map.
func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// unionKeys returns the alphabetically sorted union of keys from two tool maps.
func unionKeys(a, b map[string]policy.ToolPolicy) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedCopy returns a sorted copy of ss without modifying the original.
// Used when displaying slice contents for diff output where order is cosmetic.
func sortedCopy(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}

// sameStringSet returns true if a and b contain the same set of strings,
// regardless of order. Used to compare only_after and never_after lists
// because their evaluation semantics are order-independent.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSet := toSet(a)
	for _, s := range b {
		if !aSet[s] {
			return false
		}
	}
	return true
}

// samePriorN returns true if two *PriorNRule values are structurally equal.
func samePriorN(a, b *policy.PriorNRule) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Tool == b.Tool && a.Count == b.Count
}

// formatPriorN formats a *PriorNRule for diff display.
func formatPriorN(r *policy.PriorNRule) string {
	if r == nil {
		return "(none)"
	}
	return fmt.Sprintf("{tool:%q, count:%d}", r.Tool, r.Count)
}

// sameEscalateRule returns true if two *EscalateRule values are structurally
// equal. Value is compared via fmt.Sprintf because it is interface{} — its
// concrete type depends on the YAML scalar type (int, float64, or string).
//
// Design: using fmt.Sprintf for interface{} equality in a display-only diff is
// acceptable. The diff only needs to detect that something changed, not prove
// deep equality for policy enforcement. The evaluation engine uses typed
// comparisons; this helper is not on the hot path.
func sameEscalateRule(a, b *policy.EscalateRule) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.ArgumentPath == b.ArgumentPath &&
		a.Operator == b.Operator &&
		fmt.Sprintf("%v", a.Value) == fmt.Sprintf("%v", b.Value)
}

// formatEscalateRule formats a *EscalateRule for diff display.
func formatEscalateRule(r *policy.EscalateRule) string {
	if r == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s %s %v", r.ArgumentPath, r.Operator, r.Value)
}
