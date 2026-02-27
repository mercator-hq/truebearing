package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSimulateCommand returns the `truebearing simulate` command.
// The real implementation is added in Task 5.4.
func newSimulateCommand() *cobra.Command {
	var (
		traceFile string
		oldPolicy string
	)

	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "Replay a captured MCP trace against a policy",
		Long: `Replay a captured MCP session trace against a policy file and show
a diff table of decisions. If --old-policy is provided, the table
compares decisions under both policies side-by-side.

Simulate never writes to the database and never contacts an upstream.
It is a pure offline evaluation tool.`,
		// TODO(5.4): remove stub and implement the real simulate logic.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: simulate")
			return nil
		},
	}

	cmd.Flags().StringVar(&traceFile, "trace", "", "JSONL trace file to replay (required)")
	cmd.Flags().StringVar(&oldPolicy, "old-policy", "", "previous policy file for diff comparison")

	return cmd
}
