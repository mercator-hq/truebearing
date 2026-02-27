// Package escalation implements the `truebearing escalation` subcommand group.
//
// It owns CLI commands for listing, approving, and rejecting human escalation
// requests raised by the evaluation engine.
// It does not own escalation state transitions (see internal/escalation).
package escalation

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the `escalation` subcommand group with all subcommands registered.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "escalation",
		Short: "Review and resolve pending human escalations",
		Long: `Commands for managing escalations raised by the TrueBearing engine.

When a tool call meets an escalate_when threshold, the proxy writes a
pending escalation and returns a synthetic response to the agent. The
agent then polls check_escalation_status until an operator approves or
rejects the escalation here.`,
	}

	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newApproveCommand())
	cmd.AddCommand(newRejectCommand())

	return cmd
}
