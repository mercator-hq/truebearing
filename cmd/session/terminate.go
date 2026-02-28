package session

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newTerminateCommand returns the `session terminate` subcommand.
func newTerminateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "terminate <session-id>",
		Short: "Force-terminate a session",
		Long: `Mark a session as terminated. Any subsequent tool calls from an agent
using this session ID will receive a 410 Gone response.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			dbPath := resolveSessionDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() { _ = st.Close() }()

			if err := st.TerminateSession(sessionID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("session %q not found", sessionID)
				}
				return fmt.Errorf("terminating session %q: %w", sessionID, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Session %s terminated.\n", sessionID)
			return nil
		},
	}
}
