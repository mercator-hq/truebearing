package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newServeCommand returns the `truebearing serve` command.
// The real implementation is added in Task 3.5.
func newServeCommand() *cobra.Command {
	var (
		upstream     string
		port         int
		captureTrace string
		stdio        bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the TrueBearing proxy server",
		Long: `Start the TrueBearing MCP proxy on the configured port.

The proxy intercepts all MCP tool calls, evaluates them against the loaded
policy, and forwards allowed calls to the upstream MCP server.`,
		// TODO(1.6): remove stub and implement the real serve logic.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: serve")
			return nil
		},
	}

	cmd.Flags().StringVar(&upstream, "upstream", "", "upstream MCP server URL (required)")
	cmd.Flags().IntVar(&port, "port", 7773, "local port to listen on")
	cmd.Flags().StringVar(&captureTrace, "capture-trace", "", "write all MCP traffic to a JSONL trace file")
	cmd.Flags().BoolVar(&stdio, "stdio", false, "accept MCP requests on stdin/stdout instead of HTTP")

	return cmd
}
