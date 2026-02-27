package agent

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newListCommand returns the `agent list` subcommand.
// The real implementation is added in Task 1.6.
func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered agents",
		Long: `Show all registered agents: name, registration date, policy file,
allowed tool count, and JWT expiry.`,
		// TODO(1.6): remove stub and implement agent list.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: agent list")
			return nil
		},
	}
}
