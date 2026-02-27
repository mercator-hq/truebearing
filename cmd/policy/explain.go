package policy

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newExplainCommand returns the `policy explain` subcommand.
// The real implementation is added in Task 2.3.
func newExplainCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <file>",
		Short: "Print a plain-English summary of what a policy enforces",
		Long: `Parse a policy file and render a human-readable summary of every
rule it contains — enforcement mode, allowed tools, sequence guards,
taint rules, budget, and escalation thresholds.`,
		Args: cobra.ExactArgs(1),
		// TODO(2.3): remove stub and implement policy explain.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: policy explain")
			return nil
		},
	}
}
