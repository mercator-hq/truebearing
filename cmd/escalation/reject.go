package escalation

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newRejectCommand returns the `escalation reject` subcommand.
// The real implementation is added in Task 5.5.
func newRejectCommand() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "reject <escalation-id>",
		Short: "Reject a pending escalation",
		Long: `Reject a pending escalation. The next check_escalation_status call
from the agent will return "rejected". The agent should then abort
or take an alternate path.`,
		Args: cobra.ExactArgs(1),
		// TODO(5.5): remove stub and implement escalation reject.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: escalation reject")
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "reason for rejection (required in production)")

	return cmd
}
