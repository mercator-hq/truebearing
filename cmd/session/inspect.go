package session

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newInspectCommand returns the `session inspect` subcommand.
func newInspectCommand() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "inspect <session-id>",
		Short: "Show the full event history for a session",
		Long: `Print every tool call in a session in order: sequence number, tool
name, decision, policy rule that fired, and timestamp.

Use --format mermaid to export a Mermaid sequenceDiagram block suitable
for pasting into Notion, GitHub pull requests, or any Mermaid renderer.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "table" && format != "mermaid" {
				return fmt.Errorf("unknown format %q: must be \"table\" or \"mermaid\"", format)
			}

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

			if format == "mermaid" {
				escalations, err := st.GetEscalationsBySession(sessionID)
				if err != nil {
					return fmt.Errorf("getting escalations for session %q: %w", sessionID, err)
				}
				return writeMermaidOutput(sess.AgentName, events, escalations, cmd.OutOrStdout())
			}

			return writeInspectOutput(sess.ID, sess.AgentName, sess.PolicyFingerprint,
				sess.Tainted, sess.Terminated, events, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&format, "format", "table", `Output format: "table" (default) or "mermaid"`)
	return cmd
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

// writeMermaidOutput writes a Mermaid sequenceDiagram block for the given session
// events and escalation records. The output is suitable for pasting into any Mermaid
// renderer (Notion, GitHub PRs, mermaid.live) and is intended for sharing with
// compliance teams, auditors, or investors who cannot read JSONL files.
//
// Annotations:
//   - Allowed events that applied session taint get: Note over Proxy: session tainted
//   - Denied events get: Note over Proxy: reason: <reason_code>
//   - Escalated events get: Note over Proxy: ESCALATED → APPROVED/PENDING/REJECTED
func writeMermaidOutput(agentName string, events []store.SessionEvent, escalations []store.Escalation, w io.Writer) error {
	// Build a lookup from seq to Escalation so that each escalated event can be
	// annotated with its current resolution status without a separate DB call.
	escBySeq := make(map[uint64]store.Escalation, len(escalations))
	for _, e := range escalations {
		escBySeq[e.Seq] = e
	}

	// Pre-scan to find which allowed events caused the session taint to become
	// active. When we first see a denied event with PolicyRule "taint.session_tainted",
	// the most recent preceding allowed event is considered the taint-applying step.
	//
	// Design: we track taint cycles rather than just the first taint event so that
	// a session with taint-clear-retaint patterns annotates each application separately.
	taintCausing := detectTaintCausingEvents(events)

	// Sanitise the agent name for use as a Mermaid participant label.
	// Mermaid participant names may not contain spaces; replace with underscores.
	participant := strings.ReplaceAll(agentName, " ", "_")
	if participant == "" {
		participant = "Agent"
	}

	fmt.Fprintln(w, "sequenceDiagram")

	for _, e := range events {
		label := mermaidDecisionLabel(e.Decision)
		fmt.Fprintf(w, "    %s->>Proxy: %s (%s)\n", participant, e.ToolName, label)

		// Taint annotation: shown on allowed events that caused session taint.
		if taintCausing[e.Seq] {
			fmt.Fprintln(w, "    Note over Proxy: session tainted")
		}

		// Denial annotation: display the reason code from the policy rule that fired.
		if e.Decision == "deny" || e.Decision == "shadow_deny" {
			reasonCode := e.PolicyRule
			if reasonCode == "" {
				reasonCode = "policy_violation"
			}
			fmt.Fprintf(w, "    Note over Proxy: reason: %s\n", reasonCode)
		}

		// Escalation annotation: show the current resolution status of the escalation.
		if e.Decision == "escalate" {
			status := "PENDING"
			if esc, ok := escBySeq[e.Seq]; ok {
				status = strings.ToUpper(esc.Status)
			}
			fmt.Fprintf(w, "    Note over Proxy: ESCALATED → %s\n", status)
		}
	}

	return nil
}

// detectTaintCausingEvents returns a set of event seq numbers that caused session
// taint to become active. It identifies the most recent allowed event before the
// first denied event with PolicyRule "taint.session_tainted" within each taint cycle.
//
// Design: taint application is a side-effect of an allowed tool call (tracked in
// the in-memory session struct by the pipeline orchestrator) and is NOT stored as
// a distinct session event. We infer it by observing that the last allowed event
// before the first taint-blocked denial must have been the taint-applying step.
// This heuristic is accurate for the common case and correct for all scenarios
// where the taint-applying tool is immediately followed by a taint-sensitive tool.
func detectTaintCausingEvents(events []store.SessionEvent) map[uint64]bool {
	result := make(map[uint64]bool)

	var lastAllowedSeq uint64
	taintActive := false

	for _, e := range events {
		switch e.Decision {
		case "allow", "shadow_deny":
			// shadow_deny calls still execute on the upstream, so they can apply
			// taint even though they record a deny in the audit log. Track them
			// as candidates for the taint-causing position.
			if e.PolicyRule != "taint.session_tainted" {
				lastAllowedSeq = e.Seq
			}

		case "deny":
			if e.PolicyRule == "taint.session_tainted" && !taintActive {
				if lastAllowedSeq > 0 {
					result[lastAllowedSeq] = true
				}
				taintActive = true
			}
		}

		// Detect taint clearing: a tool allowed through with PolicyRule that
		// indicates taint clearance resets the cycle. Without the policy loaded,
		// we approximate clearing by observing that an allowed event after a
		// taint-block cycle resets the candidate position.
		if e.Decision == "allow" && taintActive {
			// Once taint is active, an allowed call could be the clearing tool.
			// Reset the tracking so we can detect the next taint application.
			taintActive = false
			lastAllowedSeq = e.Seq
		}
	}

	return result
}

// mermaidDecisionLabel returns the human-readable uppercase label for a decision
// value as it appears in the Mermaid diagram lines.
func mermaidDecisionLabel(decision string) string {
	switch decision {
	case "allow":
		return "ALLOWED"
	case "deny":
		return "DENIED"
	case "shadow_deny":
		return "SHADOW DENIED"
	case "escalate":
		return "ESCALATED"
	default:
		return strings.ToUpper(decision)
	}
}
