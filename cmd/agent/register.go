package agent

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newRegisterCommand returns the `agent register` subcommand.
// The real implementation is added in Task 1.6.
func newRegisterCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "register <name>",
		Short: "Register a new agent and issue its credentials",
		Long: `Generate an Ed25519 keypair for the named agent, issue a signed JWT
bound to the specified policy, and write both to ~/.truebearing/keys/.

The JWT is scoped to the tools listed in the policy's may_use field.
Re-registering an existing agent name overwrites its credentials.`,
		Args: cobra.ExactArgs(1),
		// TODO(1.6): remove stub and implement agent registration.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: agent register")
			return nil
		},
	}
}
