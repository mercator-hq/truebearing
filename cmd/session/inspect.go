package session

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newInspectCommand returns the `session inspect` subcommand.
func newInspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <session-id>",
		Short: "Show the full event history for a session",
		Long: `Print every tool call in a session in order: sequence number, tool
name, decision, policy rule that fired, and timestamp.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			dbPath := resolveSessionDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() { _ = st.Close() }()

			sess, err := st.GetSession(sessionID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("session %q not found", sessionID)
				}
				return fmt.Errorf("looking up session %q: %w", sessionID, err)
			}

			events, err := st.GetSessionEvents(sessionID)
			if err != nil {
				return fmt.Errorf("getting events for session %q: %w", sessionID, err)
			}

			return writeInspectOutput(sess.ID, sess.AgentName, sess.PolicyFingerprint,
				sess.Tainted, sess.Terminated, events, cmd.OutOrStdout())
		},
	}
}

// writeInspectOutput writes the session header and full event history as a table.
func writeInspectOutput(id, agentName, policyFP string, tainted, terminated bool, events []store.SessionEvent, w io.Writer) error {
	// Print session header for context.
	taintedStr := "no"
	if tainted {
		taintedStr = "YES"
	}
	terminatedStr := "no"
	if terminated {
		terminatedStr = "YES"
	}
	fmt.Fprintf(w, "Session:    %s\n", id)
	fmt.Fprintf(w, "Agent:      %s\n", agentName)
	fmt.Fprintf(w, "Policy:     %.16s\n", policyFP)
	fmt.Fprintf(w, "Tainted:    %s\n", taintedStr)
	fmt.Fprintf(w, "Terminated: %s\n", terminatedStr)
	fmt.Fprintln(w)

	if len(events) == 0 {
		fmt.Fprintln(w, "(no events recorded for this session)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tTOOL\tDECISION\tRULE\tTIME")
	for _, e := range events {
		ts := time.Unix(0, e.RecordedAt).UTC().Format("2006-01-02T15:04:05Z")
		rule := e.PolicyRule
		if rule == "" {
			rule = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			e.Seq, e.ToolName, e.Decision, rule, ts)
	}
	return tw.Flush()
}
