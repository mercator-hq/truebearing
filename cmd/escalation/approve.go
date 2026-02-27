package escalation

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newApproveCommand returns the `escalation approve` subcommand.
// The real implementation is added in Task 5.5.
func newApproveCommand() *cobra.Command {
	var note string

	cmd := &cobra.Command{
		Use:   "approve <escalation-id>",
		Short: "Approve a pending escalation",
		Long: `Approve a pending escalation. The next check_escalation_status call
from the agent will return "approved", allowing the agent to retry
the original tool call.`,
		Args: cobra.ExactArgs(1),
		// TODO(5.5): remove stub and implement escalation approve.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: escalation approve")
			return nil
		},
	}

	cmd.Flags().StringVar(&note, "note", "", "optional approval note recorded in the audit log")

	return cmd
}
