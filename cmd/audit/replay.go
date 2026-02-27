package audit

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newReplayCommand returns the `audit replay` subcommand.
// The real implementation is added in Task 5.3.
func newReplayCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "replay <file>",
		Short: "Re-run an audit log through a (potentially different) policy",
		Long: `Read a JSONL audit log and re-evaluate each recorded call through the
policy specified by --policy. Shows which decisions would change.
Useful for retroactive policy analysis without a live proxy.`,
		Args: cobra.ExactArgs(1),
		// TODO(5.3): remove stub and implement audit replay.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: audit replay")
			return nil
		},
	}
}
