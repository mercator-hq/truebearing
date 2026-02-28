package escalation

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/escalation"
	"github.com/mercator-hq/truebearing/internal/store"
)

// newRejectCommand returns the `escalation reject` subcommand.
func newRejectCommand() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "reject <escalation-id>",
		Short: "Reject a pending escalation",
		Long: `Reject a pending escalation. The next check_escalation_status call
from the agent will return "rejected". The agent should then abort
or take an alternate path.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			dbPath := resolveEscalationDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() { _ = st.Close() }()

			if err := escalation.Reject(id, reason, st); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Escalation %s rejected.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "reason for rejection")

	return cmd
}
