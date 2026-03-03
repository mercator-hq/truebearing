package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/mercator-hq/truebearing/internal/engine"
	inpolicy "github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
	pkgaudit "github.com/mercator-hq/truebearing/pkg/audit"
)

// replayEntry holds the per-call result of a replay evaluation alongside the
// decision that was originally recorded in the audit log.
type replayEntry struct {
	SessionID   string
	Seq         uint64
	ToolName    string
	OldDecision string
	NewDecision string
	// Changed is true when NewDecision differs from OldDecision.
	Changed bool
	// Reason is the policy violation explanation when NewDecision is a denial.
	Reason string
}

// auditLogLine is a type alias for pkg/audit.AuditRecord, the canonical
// JSONL audit log entry type. Using the alias rather than a local struct
// ensures json.Unmarshal sees the correct field names and that schema changes
// (including DelegationChain, added in Task 12.2) are picked up automatically.
//
// Design: a named alias rather than a direct use of pkgaudit.AuditRecord keeps
// the function signatures below stable; tests in audit_test.go that construct
// auditLogLine literals continue to compile without any changes.
type auditLogLine = pkgaudit.AuditRecord

// newReplayCommand returns the `audit replay` subcommand.
func newReplayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay <file>",
		Short: "Re-run an audit log through a (potentially different) policy",
		Long: `Read a JSONL audit log and re-evaluate each recorded call through the
policy specified by --policy. Shows which decisions would change.
Useful for retroactive policy analysis without a live proxy.

Note: escalate_when rules are skipped during replay because the audit log
stores only the SHA-256 hash of arguments, not the raw values required to
evaluate numeric or string conditions. MayUse, Budget, Taint, and Sequence
rules are fully replayed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			policyPath := viper.GetString("policy")
			if policyPath == "" {
				policyPath = "./truebearing.policy.yaml"
			}
			return runReplay(args[0], policyPath, cmd)
		},
	}
	return cmd
}

// runReplay implements `truebearing audit replay`. It reads a JSONL audit log,
// re-evaluates each call against the current policy in an in-memory SQLite
// database, and prints a diff table showing changed decisions.
//
// Design: the Escalation evaluator is omitted from the replay pipeline because
// raw arguments are not available in the audit log (only their SHA-256 hash).
// Including it would cause all tools with escalate_when rules to fail with
// "argument path not found", producing incorrect deny decisions.
func runReplay(filePath, policyPath string, cmd *cobra.Command) error {
	pol, err := inpolicy.ParseFile(policyPath)
	if err != nil {
		return fmt.Errorf("loading policy from %s: %w", policyPath, err)
	}

	records, err := parseAuditLogFile(filePath)
	if err != nil {
		return fmt.Errorf("reading audit log %s: %w", filePath, err)
	}
	if len(records) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no records found in file)")
		return nil
	}

	// Use a named in-memory SQLite database. The PID suffix ensures a unique
	// instance even if multiple replay commands run concurrently in the same OS
	// process space (uncommon, but correct).
	dsn := fmt.Sprintf("file:replay%d?mode=memory&cache=shared", os.Getpid())
	st, err := store.Open(dsn)
	if err != nil {
		return fmt.Errorf("creating in-memory store for replay: %w", err)
	}
	defer func() { _ = st.Close() }()

	// Build the evaluation pipeline without EscalationEvaluator (see Design
	// comment above). The store reference is required by SequenceEvaluator to
	// read per-session event history from the in-memory database.
	pipeline := engine.New(
		&engine.MayUseEvaluator{},
		&engine.BudgetEvaluator{},
		&engine.TaintEvaluator{},
		&engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.RateLimitEvaluator{Store: &engine.StoreBackend{Store: st}},
	)

	groups := groupAuditBySession(records)
	var allResults []replayEntry

	for _, group := range groups {
		results, err := replaySession(context.Background(), group, pol, st, pipeline)
		if err != nil {
			return fmt.Errorf("replaying session %q: %w", group[0].SessionID, err)
		}
		allResults = append(allResults, results...)
	}

	printReplayTable(allResults, pol.ShortFingerprint(), cmd.OutOrStdout())
	return nil
}

// parseAuditLogFile reads a JSONL audit log file and returns all parsed records.
// Empty lines are skipped. A parse error on any line returns an error.
func parseAuditLogFile(filePath string) ([]auditLogLine, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var records []auditLogLine
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lineNum++
		var rec auditLogLine
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parsing line %d: %w", lineNum, err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return records, nil
}

// groupAuditBySession groups records by SessionID, preserving first-encounter
// order of sessions. Within each group, records are sorted by Seq ascending so
// that the sequence evaluator sees events in the correct order.
func groupAuditBySession(records []auditLogLine) [][]auditLogLine {
	var order []string
	groups := make(map[string][]auditLogLine)
	for _, r := range records {
		if _, ok := groups[r.SessionID]; !ok {
			order = append(order, r.SessionID)
		}
		groups[r.SessionID] = append(groups[r.SessionID], r)
	}

	result := make([][]auditLogLine, 0, len(order))
	for _, sid := range order {
		group := groups[sid]
		sort.Slice(group, func(i, j int) bool {
			return group[i].Seq < group[j].Seq
		})
		result = append(result, group)
	}
	return result
}

// replaySession evaluates all records in a single session against pol using
// pipeline and st. It creates the session row in st (for the foreign key
// constraint on session_events), maintains per-session state (taint, counters)
// in the Session struct across calls, and appends each new event to st so that
// subsequent SequenceEvaluator calls see the correct history.
func replaySession(
	ctx context.Context,
	records []auditLogLine,
	pol *inpolicy.Policy,
	st *store.Store,
	pipeline *engine.Pipeline,
) ([]replayEntry, error) {
	if len(records) == 0 {
		return nil, nil
	}

	sessionID := records[0].SessionID
	agentName := records[0].AgentName

	// Create the session row so session_events inserts satisfy the FK constraint.
	if err := st.CreateSession(sessionID, agentName, pol.Fingerprint); err != nil {
		return nil, fmt.Errorf("creating replay session: %w", err)
	}

	// Session state is maintained in memory across calls. The SequenceEvaluator
	// reads event history from st; Budget and Taint read from this struct.
	sess := &session.Session{
		ID:                sessionID,
		AgentName:         agentName,
		PolicyFingerprint: pol.Fingerprint,
	}

	results := make([]replayEntry, 0, len(records))
	for _, rec := range records {
		// Raw arguments are not available in the audit log; use an empty JSON
		// object so that the MayUse, Budget, Taint, and Sequence evaluators
		// (which do not inspect arguments) work correctly. The Escalation
		// evaluator is excluded from the pipeline for this reason.
		call := &engine.ToolCall{
			SessionID:   rec.SessionID,
			AgentName:   rec.AgentName,
			ToolName:    rec.ToolName,
			Arguments:   json.RawMessage("{}"),
			RequestedAt: time.Unix(0, rec.RecordedAt),
		}

		// The pipeline applies taint mutations to sess in-place on Allow.
		newDecision := pipeline.Evaluate(ctx, call, sess, pol)

		// Append the event with the NEW decision so that subsequent calls
		// within this session see the correct sequence history for the new
		// policy, not the original policy's decisions.
		event := &store.SessionEvent{
			SessionID:  rec.SessionID,
			ToolName:   rec.ToolName,
			Decision:   string(newDecision.Action),
			PolicyRule: newDecision.RuleID,
		}
		if err := st.AppendEvent(event); err != nil {
			return nil, fmt.Errorf("appending replay event for tool %q: %w", rec.ToolName, err)
		}

		// Update session counters for allowed calls. The BudgetEvaluator reads
		// these values from the struct on the next iteration. Using the same flat
		// cost as the proxy (0.001 USD per call) keeps budget checks consistent.
		if newDecision.Action == engine.Allow || newDecision.Action == engine.ShadowDeny {
			sess.ToolCallCount++
			sess.EstimatedCostUSD += 0.001
		}

		results = append(results, replayEntry{
			SessionID:   rec.SessionID,
			Seq:         rec.Seq,
			ToolName:    rec.ToolName,
			OldDecision: rec.Decision,
			NewDecision: string(newDecision.Action),
			Changed:     string(newDecision.Action) != rec.Decision,
			Reason:      newDecision.Reason,
		})
	}
	return results, nil
}

// printReplayTable writes the replay diff table to w in the format described
// in mvp-plan.md §9.2. Changed decisions are highlighted in upper-case and
// annotated with the policy rule that triggered the change.
func printReplayTable(results []replayEntry, policyFingerprint string, w io.Writer) {
	changedCount := 0
	for _, r := range results {
		if r.Changed {
			changedCount++
		}
	}

	fmt.Fprintf(w, "Policy:  %s\n", policyFingerprint)
	fmt.Fprintln(w, strings.Repeat("─", 80))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, " seq\tsession\ttool\told\tnew\tchange")
	fmt.Fprintln(tw, "────\t────────\t────────────────────────\t──────────\t──────────\t──────────────────────")

	for _, r := range results {
		newDecision := r.NewDecision
		change := ""
		if r.Changed {
			// Upper-case the new decision and append the rule ID for visibility.
			newDecision = strings.ToUpper(r.NewDecision)
			change = "◄── " + r.Reason
			if len(change) > 60 {
				change = change[:57] + "..."
			}
		}
		fmt.Fprintf(tw, " %d\t%.8s\t%s\t%s\t%s\t%s\n",
			r.Seq, r.SessionID, r.ToolName, r.OldDecision, newDecision, change)
	}
	_ = tw.Flush()

	fmt.Fprintln(w, strings.Repeat("─", 80))
	switch changedCount {
	case 0:
		fmt.Fprintf(w, "Summary: %d record(s) processed. No decisions changed.\n", len(results))
	default:
		fmt.Fprintf(w, "Summary: %d record(s) processed. %d decision(s) changed.\n", len(results), changedCount)
	}
}
