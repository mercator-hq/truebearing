// Package session implements the `truebearing session` subcommand group.
//
// It owns CLI commands for listing, inspecting, and terminating TrueBearing sessions.
// It does not own session persistence (see internal/store) or session state logic (see internal/session).
package session

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the `session` subcommand group with all subcommands registered.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect and manage active TrueBearing sessions",
		Long: `Commands for observing and controlling agent sessions.

Each session tracks tool call history, budget consumption, and taint
state for a single agent run. Use these commands to inspect what an
agent has done or to force-terminate a misbehaving session.`,
	}

	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newInspectCommand())
	cmd.AddCommand(newTerminateCommand())

	return cmd
}
