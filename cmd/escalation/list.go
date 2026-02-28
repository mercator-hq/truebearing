package escalation

import (
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

// newListCommand returns the `escalation list` subcommand.
func newListCommand() *cobra.Command {
	var status string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List escalations",
		Long: `List escalations with their ID, session, tool, argument preview,
status, and age. Filter by status to see only pending items.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if status != "" && status != "pending" && status != "approved" && status != "rejected" {
				return fmt.Errorf("--status must be pending, approved, or rejected (got %q)", status)
			}
			return runList(status, cmd)
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "filter by status: pending, approved, rejected")

	return cmd
}

// runList opens the database, queries escalations, and writes a table to cmd's output.
func runList(status string, cmd *cobra.Command) error {
	dbPath := resolveEscalationDBPath()
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database at %s: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	escs, err := st.ListEscalations(status)
	if err != nil {
		return fmt.Errorf("listing escalations: %w", err)
	}

	return writeEscalationTable(escs, cmd.OutOrStdout())
}

// writeEscalationTable formats escalations as an aligned text table.
// The arguments preview is truncated to 40 characters for readability.
func writeEscalationTable(escs []store.Escalation, w io.Writer) error {
	if len(escs) == 0 {
		fmt.Fprintln(w, "(no escalations match the given filters)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSESSION\tTOOL\tSTATUS\tAGE\tARGS PREVIEW")
	now := time.Now().UnixNano()
	for _, e := range escs {
		age := formatAge(now - e.CreatedAt)
		argsPreview := e.ArgumentsJSON
		if len(argsPreview) > 40 {
			argsPreview = argsPreview[:37] + "..."
		}
		fmt.Fprintf(tw, "%.8s\t%.8s\t%s\t%s\t%s\t%s\n",
			e.ID, e.SessionID, e.ToolName, e.Status, age, argsPreview)
	}
	return tw.Flush()
}

// formatAge converts a nanosecond duration into a human-readable age string.
func formatAge(ns int64) string {
	d := time.Duration(ns)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// resolveEscalationDBPath returns the database path for escalation commands.
// It honours the --db viper key (bound to the persistent root flag) and falls
// back to ~/.truebearing/truebearing.db.
func resolveEscalationDBPath() string {
	if p := viper.GetString("db"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "truebearing.db"
	}
	return filepath.Join(home, ".truebearing", "truebearing.db")
}
