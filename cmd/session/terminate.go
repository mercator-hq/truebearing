package session

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newTerminateCommand returns the `session terminate` subcommand.
// The real implementation is added in Task 5.6.
func newTerminateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "terminate <session-id>",
		Short: "Force-terminate a session",
		Long: `Mark a session as terminated. Any subsequent tool calls from an agent
using this session ID will receive a 410 Gone response.`,
		Args: cobra.ExactArgs(1),
		// TODO(5.6): remove stub and implement session terminate.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: session terminate")
			return nil
		},
	}
}
