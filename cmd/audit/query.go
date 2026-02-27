package audit

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newQueryCommand returns the `audit query` subcommand.
// The real implementation is added in Task 5.3.
func newQueryCommand() *cobra.Command {
	var (
		sessionID string
		toolName  string
		decision  string
		traceID   string
		from      string
		to        string
		format    string
	)

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the audit log with optional filters",
		Long: `Query the TrueBearing audit log. All filters are optional and may be
combined. Use --format to control the output format.`,
		// TODO(5.3): remove stub and implement audit query.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: audit query")
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "filter by session ID")
	cmd.Flags().StringVar(&toolName, "tool", "", "filter by tool name")
	cmd.Flags().StringVar(&decision, "decision", "", "filter by decision (allow, deny, shadow_deny, escalate)")
	cmd.Flags().StringVar(&traceID, "trace-id", "", "filter by client trace ID (W3C traceparent, x-datadog-trace-id, etc.)")
	cmd.Flags().StringVar(&from, "from", "", "start of time range (RFC3339)")
	cmd.Flags().StringVar(&to, "to", "", "end of time range (RFC3339)")
	cmd.Flags().StringVar(&format, "format", "table", "output format: table, json, csv")

	return cmd
}
