// Package policy implements the `truebearing policy` subcommand group.
//
// It owns CLI commands that parse, lint, explain, and diff policy YAML files.
// It does not own the policy parser or linter logic (see internal/policy).
package policy

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the `policy` subcommand group with all subcommands registered.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Manage and inspect TrueBearing policy files",
		Long: `Commands for working with TrueBearing policy YAML files.

Use these commands to validate, lint, explain, and compare policies
before deploying them to the proxy.`,
	}

	cmd.AddCommand(newValidateCommand())
	cmd.AddCommand(newLintCommand())
	cmd.AddCommand(newExplainCommand())
	cmd.AddCommand(newDiffCommand())

	return cmd
}
