// Package agent implements the `truebearing agent` subcommand group.
//
// It owns CLI commands for registering agents, listing registered agents, and
// revoking agent credentials. It does not own keypair generation or JWT minting
// (see internal/identity) or database access (see internal/store).
package agent

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the `agent` subcommand group with all subcommands registered.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Register, list, and revoke TrueBearing agents",
		Long: `Commands for managing agent identities.

Registering an agent generates an Ed25519 keypair and issues a JWT
bound to the agent's policy. The JWT must be presented on every
request to the proxy via the Authorization: Bearer header.

Revoking an agent immediately blocks all proxy requests using that
agent's JWT, including requests on sessions started before revocation.`,
	}

	cmd.AddCommand(newRegisterCommand())
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newRevokeCommand())

	return cmd
}
