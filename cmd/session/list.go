package session

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newListCommand returns the `session list` subcommand.
// The real implementation is added in Task 5.6.
func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active sessions",
		Long: `Show all non-terminated sessions with their ID, agent name, policy
fingerprint, taint status, tool call count, estimated cost, and age.`,
		// TODO(5.6): remove stub and implement session list.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: session list")
			return nil
		},
	}
}
