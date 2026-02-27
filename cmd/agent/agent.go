// Package agent implements the `truebearing agent` subcommand group.
//
// It owns CLI commands for registering agents and listing registered agents.
// It does not own keypair generation or JWT minting (see internal/identity) or
// database access (see internal/store).
package agent

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the `agent` subcommand group with all subcommands registered.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Register and list TrueBearing agents",
		Long: `Commands for managing agent identities.

Registering an agent generates an Ed25519 keypair and issues a JWT
bound to the agent's policy. The JWT must be presented on every
request to the proxy via the Authorization: Bearer header.`,
	}

	cmd.AddCommand(newRegisterCommand())
	cmd.AddCommand(newListCommand())

	return cmd
}
