package audit

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newQueryCommand returns the `audit query` subcommand.
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
combined. Multiple filters are AND-ed together.

Use --format to control the output format (table, json, or csv).

Example:
  truebearing audit query --decision deny --from 2026-01-01T00:00:00Z`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := buildAuditFilter(sessionID, toolName, decision, traceID, from, to)
			if err != nil {
				return err
			}
			return runQuery(filter, format, cmd)
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "filter by session ID")
	cmd.Flags().StringVar(&toolName, "tool", "", "filter by tool name")
	cmd.Flags().StringVar(&decision, "decision", "", "filter by decision (allow, deny, shadow_deny, escalate)")
	cmd.Flags().StringVar(&traceID, "trace-id", "", "filter by client trace ID (W3C traceparent, x-datadog-trace-id, etc.)")
	cmd.Flags().StringVar(&from, "from", "", "start of time range inclusive (RFC3339, e.g. 2026-01-01T00:00:00Z)")
	cmd.Flags().StringVar(&to, "to", "", "end of time range inclusive (RFC3339)")
	cmd.Flags().StringVar(&format, "format", "table", "output format: table, json, csv")

	return cmd
}

// buildAuditFilter converts CLI flag strings into a store.AuditFilter.
// from and to are parsed as RFC3339 timestamps; an error is returned if either
// is non-empty but cannot be parsed.
func buildAuditFilter(sessionID, toolName, decision, traceID, from, to string) (store.AuditFilter, error) {
	filter := store.AuditFilter{
		SessionID: sessionID,
		ToolName:  toolName,
		Decision:  decision,
		TraceID:   traceID,
	}
	if from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return store.AuditFilter{}, fmt.Errorf("parsing --from %q as RFC3339: %w", from, err)
		}
		filter.From = t
	}
	if to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return store.AuditFilter{}, fmt.Errorf("parsing --to %q as RFC3339: %w", to, err)
		}
		filter.To = t
	}
	return filter, nil
}

// runQuery opens the database, executes the filtered audit log query, and
// writes the results in the chosen format to cmd's standard output.
func runQuery(filter store.AuditFilter, format string, cmd *cobra.Command) error {
	dbPath := resolveQueryDBPath()
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database at %s: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	records, err := st.QueryAuditLog(filter)
	if err != nil {
		return fmt.Errorf("querying audit log: %w", err)
	}

	switch format {
	case "table":
		return writeQueryTable(records, cmd.OutOrStdout())
	case "json":
		return writeQueryJSON(records, cmd.OutOrStdout())
	case "csv":
		return writeQueryCSV(records, cmd.OutOrStdout())
	default:
		return fmt.Errorf("unknown --format %q: must be table, json, or csv", format)
	}
}

// resolveQueryDBPath returns the database path for query commands. It honours
// the --db viper key (bound to the persistent root flag) and falls back to
// ~/.truebearing/truebearing.db.
func resolveQueryDBPath() string {
	if p := viper.GetString("db"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "truebearing.db"
	}
	return filepath.Join(home, ".truebearing", "truebearing.db")
}

// writeQueryTable formats records as an aligned text table written to w.
// Decision reasons longer than 60 characters are truncated with an ellipsis.
func writeQueryTable(records []store.AuditRecord, w io.Writer) error {
	if len(records) == 0 {
		fmt.Fprintln(w, "(no records match the given filters)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RECORDED_AT\tSESSION\tSEQ\tAGENT\tTOOL\tDECISION\tREASON")
	for _, r := range records {
		ts := time.Unix(0, r.RecordedAt).UTC().Format(time.RFC3339)
		reason := r.DecisionReason
		if len(reason) > 60 {
			reason = reason[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%.8s\t%d\t%s\t%s\t%s\t%s\n",
			ts, r.SessionID, r.Seq, r.AgentName, r.ToolName, r.Decision, reason)
	}
	return tw.Flush()
}

// writeQueryJSON serialises records as a JSON array written to w.
func writeQueryJSON(records []store.AuditRecord, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(records)
}

// writeQueryCSV formats records as RFC 4180 CSV written to w. The header row
// names the columns used in this report; signature and JWT hash fields are
// omitted from CSV output for readability.
func writeQueryCSV(records []store.AuditRecord, w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"recorded_at", "session_id", "seq", "agent_name",
		"tool_name", "decision", "decision_reason", "policy_fingerprint",
	}); err != nil {
		return fmt.Errorf("writing CSV header: %w", err)
	}
	for _, r := range records {
		ts := time.Unix(0, r.RecordedAt).UTC().Format(time.RFC3339)
		if err := cw.Write([]string{
			ts,
			r.SessionID,
			fmt.Sprintf("%d", r.Seq),
			r.AgentName,
			r.ToolName,
			r.Decision,
			r.DecisionReason,
			r.PolicyFingerprint,
		}); err != nil {
			return fmt.Errorf("writing CSV row: %w", err)
		}
	}
	cw.Flush()
	return cw.Error()
}
