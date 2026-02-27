package policy

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newLintCommand returns the `policy lint` subcommand.
// The real implementation is added in Task 2.3.
func newLintCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "lint <file>",
		Short: "Check a policy file for common mistakes",
		Long: `Run the TrueBearing policy linter against a YAML file and report
any rule violations (see mvp-plan.md §6.4 for the full rule table).

Exit code 0 if no ERRORs; exit code 1 if any ERROR rules fire.
WARNINGs and INFOs are printed but do not affect the exit code.`,
		Args: cobra.ExactArgs(1),
		// TODO(2.3): remove stub and implement policy linting.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: policy lint")
			return nil
		},
	}
}
