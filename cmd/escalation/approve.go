package escalation

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/escalation"
	"github.com/mercator-hq/truebearing/internal/store"
)

// newApproveCommand returns the `escalation approve` subcommand.
func newApproveCommand() *cobra.Command {
	var note string

	cmd := &cobra.Command{
		Use:   "approve <escalation-id>",
		Short: "Approve a pending escalation",
		Long: `Approve a pending escalation. The next check_escalation_status call
from the agent will return "approved", allowing the agent to retry
the original tool call.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			dbPath := resolveEscalationDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() { _ = st.Close() }()

			if err := escalation.Approve(id, note, st); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Escalation %s approved.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVar(&note, "note", "", "optional approval note recorded with the escalation")

	return cmd
}
