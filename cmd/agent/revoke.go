package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newRevokeCommand returns the `agent revoke` subcommand.
func newRevokeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <name>",
		Short: "Revoke an agent's credentials",
		Long: `Mark the named agent as revoked in the database.

Once revoked, all subsequent proxy requests using that agent's JWT are
rejected with HTTP 401 — including requests on sessions that were started
before the revocation. The cryptographic validity of the JWT is irrelevant
once the agent is revoked.

To restore access, re-register the agent with 'truebearing agent register'.
Re-registration issues fresh credentials and clears the revocation.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRevoke(args[0])
		},
	}
}

// runRevoke implements truebearing agent revoke.
func runRevoke(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	tbHome := filepath.Join(home, ".truebearing")

	dbPath := resolveDBPath(tbHome)
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database at %s: %w", dbPath, err)
	}
	defer st.Close()

	if err := st.RevokeAgent(name); err != nil {
		return fmt.Errorf("revoking agent %q: %w", name, err)
	}

	fmt.Printf("Agent %q has been revoked.\n", name)
	fmt.Println("All future proxy requests using this agent's JWT will be rejected with HTTP 401.")
	fmt.Printf("To restore access: truebearing agent register %s --policy <policy-file>\n", name)
	return nil
}
