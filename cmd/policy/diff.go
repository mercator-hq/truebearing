package policy

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newDiffCommand returns the `policy diff` subcommand.
// The real implementation is added in Task 2.3.
func newDiffCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <old-file> <new-file>",
		Short: "Show rule changes between two policy files",
		Long: `Compare two policy YAML files and print a structured diff showing:
added or removed tools, changed sequence predicates, changed budget
limits, and enforcement mode changes.`,
		Args: cobra.ExactArgs(2),
		// TODO(2.3): remove stub and implement policy diff.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: policy diff")
			return nil
		},
	}
}
