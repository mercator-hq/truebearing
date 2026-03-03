// Package audit implements the `truebearing audit` subcommand group.
//
// It owns CLI commands for verifying audit log signatures, querying the audit
// log, and replaying captured traces against a policy.
// It does not own audit record signing or database access (see internal/audit and internal/store).
package audit

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the `audit` subcommand group with all subcommands registered.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect and verify the TrueBearing audit log",
		Long: `Commands for working with the TrueBearing signed audit log.

Use these commands to verify record integrity, query decisions by filter,
and replay captured session traces against updated policies.`,
	}

	cmd.AddCommand(newVerifyCommand())
	cmd.AddCommand(newQueryCommand())
	cmd.AddCommand(newExportCommand())
	cmd.AddCommand(newReplayCommand())
	cmd.AddCommand(newReportCommand())

	return cmd
}
