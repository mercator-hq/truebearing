package session

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

// newListCommand returns the `session list` subcommand.
func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active sessions",
		Long: `Show all non-terminated sessions with their ID, agent name, policy
fingerprint, taint status, tool call count, estimated cost, and age.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := resolveSessionDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() { _ = st.Close() }()

			sessions, err := st.ListSessions()
			if err != nil {
				return fmt.Errorf("listing sessions: %w", err)
			}

			return writeSessionTable(sessions, cmd.OutOrStdout())
		},
	}
}

// writeSessionTable formats sessions as an aligned text table.
func writeSessionTable(sessions []store.SessionRow, w io.Writer) error {
	if len(sessions) == 0 {
		fmt.Fprintln(w, "(no active sessions)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAGENT\tPOLICY\tTAINTED\tCALLS\tCOST ($)\tAGE")
	now := time.Now().UnixNano()
	for _, s := range sessions {
		tainted := "no"
		if s.Tainted {
			tainted = "YES"
		}
		age := formatSessionAge(now - s.CreatedAt)
		// Show first 8 characters of IDs for readability; full IDs are
		// available via `session inspect`.
		fmt.Fprintf(tw, "%.8s\t%s\t%.8s\t%s\t%d\t%.4f\t%s\n",
			s.ID, s.AgentName, s.PolicyFingerprint, tainted,
			s.ToolCallCount, s.EstimatedCostUSD, age)
	}
	return tw.Flush()
}

// formatSessionAge converts a nanosecond duration into a human-readable age string.
func formatSessionAge(ns int64) string {
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

// resolveSessionDBPath returns the database path for session commands.
// It honours the --db viper key (bound to the persistent root flag) and falls
// back to ~/.truebearing/truebearing.db.
func resolveSessionDBPath() string {
	if p := viper.GetString("db"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "truebearing.db"
	}
	return filepath.Join(home, ".truebearing", "truebearing.db")
}
