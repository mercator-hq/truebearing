package escalation

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newListCommand returns the `escalation list` subcommand.
// The real implementation is added in Task 5.5.
func newListCommand() *cobra.Command {
	var status string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List escalations",
		Long: `List escalations with their ID, session, tool, argument preview,
status, and age. Filter by status to see only pending items.`,
		// TODO(5.5): remove stub and implement escalation list.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: escalation list")
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "filter by status: pending, approved, rejected")

	return cmd
}
