package session

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newInspectCommand returns the `session inspect` subcommand.
// The real implementation is added in Task 5.6.
func newInspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <session-id>",
		Short: "Show the full event history for a session",
		Long: `Print every tool call in a session in order: sequence number, tool
name, decision, policy rule that fired, and timestamp.`,
		Args: cobra.ExactArgs(1),
		// TODO(5.6): remove stub and implement session inspect.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: session inspect")
			return nil
		},
	}
}
