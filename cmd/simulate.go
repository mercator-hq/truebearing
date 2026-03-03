package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// traceEntry is a single MCP tools/call request captured during a live proxy
// session. It is the JSON schema of one line in a JSONL trace file written by
// `truebearing serve --capture-trace`.
//
// Design: RequestedAt is stored as an RFC3339 string rather than unix
// nanoseconds. Trace files are operator-readable artifacts; human-readable
// timestamps are easier to inspect, edit, and reason about than nanosecond
// integers.
type traceEntry struct {
	SessionID   string          `json:"session_id"`
	AgentName   string          `json:"agent_name"`
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	RequestedAt string          `json:"requested_at"` // RFC3339; empty is accepted
}

// simulateResult holds the per-call evaluation result from a single simulate
// run, with optional old-policy comparison fields populated when --old-policy
// is provided.
type simulateResult struct {
	SessionID string
	Seq       uint64
	ToolName  string
	// OldDecision is the decision under the old policy. It is empty when
	// --old-policy was not provided.
	OldDecision string
	// NewDecision is the decision under the current (--policy) policy.
	NewDecision string
	// Changed is true when OldDecision and NewDecision differ.
	Changed bool
	// Reason is the policy violation explanation for a non-allow NewDecision.
	Reason string
}

// newSimulateCommand returns the `truebearing simulate` command.
func newSimulateCommand() *cobra.Command {
	var (
		traceFile string
		oldPolicy string
	)

	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "Replay a captured MCP trace against a policy",
		Long: `Replay a captured MCP session trace against a policy file and show
a table of decisions for each call. If --old-policy is provided, the table
compares decisions under both policies side-by-side, highlighting calls where
the decision would change.

Simulate never writes to the database and never contacts an upstream.
It is a pure offline evaluation tool.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			policyPath := viper.GetString("policy")
			if policyPath == "" {
				policyPath = "./truebearing.policy.yaml"
			}
			if traceFile == "" {
				return fmt.Errorf("--trace is required")
			}
			return runSimulate(traceFile, policyPath, oldPolicy, cmd)
		},
	}

	cmd.Flags().StringVar(&traceFile, "trace", "", "JSONL trace file to replay (required)")
	cmd.Flags().StringVar(&oldPolicy, "old-policy", "", "previous policy file for diff comparison")

	return cmd
}

// runSimulate implements `truebearing simulate`. It reads a JSONL trace file
// of raw MCP tool call requests, evaluates each call against the given policy
// using a fresh in-memory SQLite database, and prints a decision table.
//
// When oldPolicyPath is non-empty, the trace is evaluated against both
// policies and a diff table is printed showing which decisions changed.
// Simulate never writes to the persistent database and never contacts an
// upstream MCP server.
func runSimulate(traceFile, policyPath, oldPolicyPath string, cmd *cobra.Command) error {
	pol, err := policy.ParseFile(policyPath)
	if err != nil {
		return fmt.Errorf("loading policy from %s: %w", policyPath, err)
	}

	entries, err := parseTraceFile(traceFile)
	if err != nil {
		return fmt.Errorf("reading trace file %s: %w", traceFile, err)
	}
	if len(entries) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no entries found in trace file)")
		return nil
	}

	groups := groupTraceBySession(entries)

	// Evaluate the trace against the current (--policy) policy.
	newResults, err := evaluateAllSessions(groups, pol, "new")
	if err != nil {
		return fmt.Errorf("evaluating trace against %s: %w", policyPath, err)
	}

	// When --old-policy is provided, evaluate against the old policy too and
	// merge the results into a diff view.
	var results []simulateResult
	if oldPolicyPath != "" {
		oldPol, err := policy.ParseFile(oldPolicyPath)
		if err != nil {
			return fmt.Errorf("loading old policy from %s: %w", oldPolicyPath, err)
		}
		oldResults, err := evaluateAllSessions(groups, oldPol, "old")
		if err != nil {
			return fmt.Errorf("evaluating trace against old policy %s: %w", oldPolicyPath, err)
		}
		results = mergeResults(oldResults, newResults)
	} else {
		results = newResults
	}

	printSimulateTable(results, pol.ShortFingerprint(), oldPolicyPath != "", cmd.OutOrStdout())
	return nil
}

// evaluateAllSessions runs each session group through the full evaluation
// pipeline using a fresh in-memory SQLite database. dsnSuffix distinguishes
// the in-memory database name when old and new policies are evaluated in the
// same process (both calls are sequential but share the same PID).
func evaluateAllSessions(
	groups [][]traceEntry,
	pol *policy.Policy,
	dsnSuffix string,
) ([]simulateResult, error) {
	// Use a named in-memory SQLite database. The suffix and PID together
	// ensure a unique instance even when this function is called twice (for
	// old and new policies) within the same process.
	dsn := fmt.Sprintf("file:simulate_%s_%d?mode=memory&cache=shared", dsnSuffix, os.Getpid())
	st, err := store.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("creating in-memory store for simulate_%s: %w", dsnSuffix, err)
	}
	defer func() { _ = st.Close() }()

	// The full pipeline including EscalationEvaluator: simulate has access to
	// raw arguments (unlike audit replay which only has SHA-256 hashes), so
	// escalate_when conditions can be evaluated correctly.
	pipeline := engine.New(
		&engine.MayUseEvaluator{},
		&engine.BudgetEvaluator{},
		&engine.TaintEvaluator{},
		&engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.RateLimitEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.EscalationEvaluator{Store: &engine.StoreBackend{Store: st}},
	)

	var allResults []simulateResult
	for _, group := range groups {
		results, err := evaluateSession(context.Background(), group, pol, st, pipeline)
		if err != nil {
			return nil, fmt.Errorf("evaluating session %q: %w", group[0].SessionID, err)
		}
		allResults = append(allResults, results...)
	}
	return allResults, nil
}

// evaluateSession evaluates all trace entries in a single session against pol
// using pipeline and st. It creates the session row in st (required for the
// foreign key constraint on session_events), maintains per-session state in
// the Session struct across calls, and appends each event to st so that
// subsequent SequenceEvaluator calls see the correct history.
func evaluateSession(
	ctx context.Context,
	entries []traceEntry,
	pol *policy.Policy,
	st *store.Store,
	pipeline *engine.Pipeline,
) ([]simulateResult, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	sessionID := entries[0].SessionID
	agentName := entries[0].AgentName

	if err := st.CreateSession(sessionID, agentName, pol.Fingerprint); err != nil {
		return nil, fmt.Errorf("creating simulate session %q: %w", sessionID, err)
	}

	// Session state is tracked in memory across calls within this session.
	// SequenceEvaluator reads event history from st; BudgetEvaluator and
	// TaintEvaluator read directly from this struct.
	sess := &session.Session{
		ID:                sessionID,
		AgentName:         agentName,
		PolicyFingerprint: pol.Fingerprint,
	}

	results := make([]simulateResult, 0, len(entries))
	for i, entry := range entries {
		args := entry.Arguments
		if len(args) == 0 {
			// Fall back to an empty JSON object so evaluators that inspect
			// arguments (e.g. EscalationEvaluator) receive valid JSON.
			args = json.RawMessage("{}")
		}

		requestedAt := parseRFC3339OrNow(entry.RequestedAt)

		call := &engine.ToolCall{
			SessionID:   entry.SessionID,
			AgentName:   entry.AgentName,
			ToolName:    entry.ToolName,
			Arguments:   args,
			RequestedAt: requestedAt,
		}

		// The pipeline applies taint mutations to sess in-place on Allow.
		decision := pipeline.Evaluate(ctx, call, sess, pol)

		// Append the event with the resulting decision so that subsequent
		// SequenceEvaluator calls within this session see the correct history.
		// RecordedAt is set to the original trace timestamp so that the
		// RateLimitEvaluator's window query (which filters by recorded_at)
		// reflects the original call distribution rather than collapsing all
		// events to the current wall-clock time.
		event := &store.SessionEvent{
			SessionID:  entry.SessionID,
			ToolName:   entry.ToolName,
			Decision:   string(decision.Action),
			PolicyRule: decision.RuleID,
			RecordedAt: requestedAt.UnixNano(),
		}
		if err := st.AppendEvent(event); err != nil {
			return nil, fmt.Errorf("appending simulate event for tool %q seq %d: %w", entry.ToolName, i+1, err)
		}

		// Update session counters in memory for BudgetEvaluator on the next
		// iteration. Using the same flat cost as the proxy (0.001 USD per
		// call) keeps budget checks consistent with live proxy behaviour.
		if decision.Action == engine.Allow || decision.Action == engine.ShadowDeny {
			sess.ToolCallCount++
			sess.EstimatedCostUSD += 0.001
		}

		results = append(results, simulateResult{
			SessionID:   entry.SessionID,
			Seq:         uint64(i + 1),
			ToolName:    entry.ToolName,
			NewDecision: string(decision.Action),
			Reason:      decision.Reason,
		})
	}
	return results, nil
}

// mergeResults combines old-policy and new-policy evaluation results into a
// single diff view. The two slices are assumed to be the same length and in
// the same order (same trace, evaluated twice).
func mergeResults(oldResults, newResults []simulateResult) []simulateResult {
	merged := make([]simulateResult, len(newResults))
	for i, r := range newResults {
		merged[i] = r
		if i < len(oldResults) {
			merged[i].OldDecision = oldResults[i].NewDecision
			merged[i].Changed = oldResults[i].NewDecision != r.NewDecision
		}
	}
	return merged
}

// parseTraceFile reads a JSONL trace file and returns all parsed traceEntry
// values. Empty lines are skipped. An error on any non-empty line is returned
// immediately with the line number for easy diagnosis.
func parseTraceFile(filePath string) ([]traceEntry, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// Allow lines up to 1 MiB (large argument JSON payloads are possible).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var entries []traceEntry
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lineNum++
		var entry traceEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parsing line %d of %s: %w", lineNum, filePath, err)
		}
		if entry.ToolName == "" {
			return nil, fmt.Errorf("line %d of %s: tool_name is required", lineNum, filePath)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}
	return entries, nil
}

// groupTraceBySession groups trace entries by session_id, preserving
// first-encounter order of sessions. Within each group, entries are kept in
// their original file order, which reflects the chronological order in which
// the proxy intercepted them.
//
// Design: entries are NOT sorted within a group. The trace file's order is
// authoritative — re-sorting by timestamp would corrupt the sequence history
// for entries with identical or missing timestamps.
func groupTraceBySession(entries []traceEntry) [][]traceEntry {
	var order []string
	groups := make(map[string][]traceEntry)
	for _, e := range entries {
		if _, ok := groups[e.SessionID]; !ok {
			order = append(order, e.SessionID)
		}
		groups[e.SessionID] = append(groups[e.SessionID], e)
	}
	result := make([][]traceEntry, 0, len(order))
	for _, sid := range order {
		result = append(result, groups[sid])
	}
	return result
}

// printSimulateTable writes the simulation decision table to w.
//
// When showDiff is true (--old-policy was provided), the table includes an
// old_decision column and marks changed decisions in upper-case, annotated
// with the policy rule that triggered the change.
//
// When showDiff is false, the table shows a single decision column and a
// reason column, followed by a summary of allow/deny/shadow_deny/escalate
// counts.
func printSimulateTable(results []simulateResult, policyShortFP string, showDiff bool, w io.Writer) {
	changedCount := 0
	for _, r := range results {
		if r.Changed {
			changedCount++
		}
	}

	fmt.Fprintf(w, "Policy: %s\n", policyShortFP)
	separator := strings.Repeat("─", 80)
	fmt.Fprintln(w, separator)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if showDiff {
		fmt.Fprintln(tw, " seq\ttool\told_decision\tnew_decision\tchanged")
		fmt.Fprintln(tw, "────\t────────────────────────\t────────────\t────────────\t──────────────────────────────")
	} else {
		fmt.Fprintln(tw, " seq\ttool\tdecision\treason")
		fmt.Fprintln(tw, "────\t────────────────────────\t────────────\t──────────────────────────────")
	}

	for _, r := range results {
		newDecision := r.NewDecision
		if showDiff {
			changeStr := ""
			if r.Changed {
				newDecision = strings.ToUpper(r.NewDecision)
				changeStr = "◄── " + r.Reason
				if len(changeStr) > 60 {
					changeStr = changeStr[:57] + "..."
				}
			}
			fmt.Fprintf(tw, " %d\t%s\t%s\t%s\t%s\n",
				r.Seq, r.ToolName, r.OldDecision, newDecision, changeStr)
		} else {
			reason := r.Reason
			if len(reason) > 60 {
				reason = reason[:57] + "..."
			}
			fmt.Fprintf(tw, " %d\t%s\t%s\t%s\n",
				r.Seq, r.ToolName, newDecision, reason)
		}
	}
	_ = tw.Flush()
	fmt.Fprintln(w, separator)

	if showDiff {
		switch changedCount {
		case 0:
			fmt.Fprintf(w, "Summary: %d call(s) simulated. No decisions changed.\n", len(results))
		default:
			fmt.Fprintf(w, "Summary: %d call(s) simulated. %d decision(s) changed.\n", len(results), changedCount)
		}
	} else {
		allowCount, denyCount, shadowCount, escalateCount := 0, 0, 0, 0
		for _, r := range results {
			switch engine.Action(r.NewDecision) {
			case engine.Allow:
				allowCount++
			case engine.Deny:
				denyCount++
			case engine.ShadowDeny:
				shadowCount++
			case engine.Escalate:
				escalateCount++
			}
		}
		fmt.Fprintf(w, "Summary: %d call(s): %d allow, %d deny, %d shadow_deny, %d escalate.\n",
			len(results), allowCount, denyCount, shadowCount, escalateCount)
	}
}

// parseRFC3339OrNow parses s as RFC3339. If s is empty or unparseable, it
// returns time.Now(). This makes the trace file format forgiving: operators
// can omit requested_at without breaking the simulation.
func parseRFC3339OrNow(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now()
	}
	return t
}
