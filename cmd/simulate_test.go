package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// --- groupTraceBySession ---

func TestGroupTraceBySession_Empty(t *testing.T) {
	groups := groupTraceBySession(nil)
	if len(groups) != 0 {
		t.Errorf("groupTraceBySession(nil): want 0 groups, got %d", len(groups))
	}
}

func TestGroupTraceBySession_SingleSession(t *testing.T) {
	entries := []traceEntry{
		{SessionID: "sess-1", ToolName: "tool_a"},
		{SessionID: "sess-1", ToolName: "tool_b"},
		{SessionID: "sess-1", ToolName: "tool_c"},
	}
	groups := groupTraceBySession(entries)
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	if len(groups[0]) != 3 {
		t.Errorf("want 3 entries in group, got %d", len(groups[0]))
	}
	// Within-group order must be preserved (file order is authoritative).
	for i, want := range []string{"tool_a", "tool_b", "tool_c"} {
		if groups[0][i].ToolName != want {
			t.Errorf("entry %d ToolName = %q, want %q", i, groups[0][i].ToolName, want)
		}
	}
}

func TestGroupTraceBySession_MultipleSessionsPreservesOrder(t *testing.T) {
	// Interleaved entries from three sessions. First-encounter order must be
	// sess-A → sess-B → sess-C, and within each group entries keep file order.
	entries := []traceEntry{
		{SessionID: "sess-A", ToolName: "a1"},
		{SessionID: "sess-B", ToolName: "b1"},
		{SessionID: "sess-A", ToolName: "a2"},
		{SessionID: "sess-C", ToolName: "c1"},
		{SessionID: "sess-B", ToolName: "b2"},
	}
	groups := groupTraceBySession(entries)
	if len(groups) != 3 {
		t.Fatalf("want 3 groups, got %d", len(groups))
	}
	wantOrder := []string{"sess-A", "sess-B", "sess-C"}
	for i, g := range groups {
		if g[0].SessionID != wantOrder[i] {
			t.Errorf("group %d: want session %q, got %q", i, wantOrder[i], g[0].SessionID)
		}
	}
	if groups[0][0].ToolName != "a1" || groups[0][1].ToolName != "a2" {
		t.Errorf("sess-A entries out of file order: %v", groups[0])
	}
	if groups[1][0].ToolName != "b1" || groups[1][1].ToolName != "b2" {
		t.Errorf("sess-B entries out of file order: %v", groups[1])
	}
}

// --- parseTraceFile ---

func TestParseTraceFile_ValidFile(t *testing.T) {
	raw := `{"session_id":"s1","agent_name":"agent","tool_name":"tool_a","arguments":{"x":1},"requested_at":"2026-02-28T10:00:00Z"}
{"session_id":"s1","agent_name":"agent","tool_name":"tool_b","arguments":{},"requested_at":"2026-02-28T10:00:01Z"}
`
	path := writeTempTrace(t, raw)
	entries, err := parseTraceFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].ToolName != "tool_a" {
		t.Errorf("entry 0 ToolName: got %q, want %q", entries[0].ToolName, "tool_a")
	}
	if entries[1].ToolName != "tool_b" {
		t.Errorf("entry 1 ToolName: got %q, want %q", entries[1].ToolName, "tool_b")
	}
}

func TestParseTraceFile_SkipsEmptyLines(t *testing.T) {
	raw := `{"session_id":"s1","agent_name":"agent","tool_name":"tool_a","arguments":{}}

{"session_id":"s1","agent_name":"agent","tool_name":"tool_b","arguments":{}}
`
	path := writeTempTrace(t, raw)
	entries, err := parseTraceFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("want 2 entries (blank line skipped), got %d", len(entries))
	}
}

func TestParseTraceFile_InvalidJSON(t *testing.T) {
	raw := `{"session_id":"s1","agent_name":"agent","tool_name":"tool_a","arguments":{}}
not valid json
`
	path := writeTempTrace(t, raw)
	_, err := parseTraceFile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should mention line 2, got: %v", err)
	}
}

func TestParseTraceFile_MissingToolName(t *testing.T) {
	raw := `{"session_id":"s1","agent_name":"agent","arguments":{}}
`
	path := writeTempTrace(t, raw)
	_, err := parseTraceFile(path)
	if err == nil {
		t.Fatal("expected error for missing tool_name, got nil")
	}
	if !strings.Contains(err.Error(), "tool_name") {
		t.Errorf("error should mention tool_name, got: %v", err)
	}
}

func TestParseTraceFile_FileNotFound(t *testing.T) {
	_, err := parseTraceFile("/does/not/exist.jsonl")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// --- mergeResults ---

func TestMergeResults_NoChange(t *testing.T) {
	old := []simulateResult{
		{Seq: 1, ToolName: "tool_a", NewDecision: "allow"},
		{Seq: 2, ToolName: "tool_b", NewDecision: "allow"},
	}
	newR := []simulateResult{
		{Seq: 1, ToolName: "tool_a", NewDecision: "allow"},
		{Seq: 2, ToolName: "tool_b", NewDecision: "allow"},
	}
	merged := mergeResults(old, newR)
	for i, r := range merged {
		if r.Changed {
			t.Errorf("entry %d: Changed should be false when decisions match", i)
		}
		if r.OldDecision != "allow" {
			t.Errorf("entry %d: OldDecision = %q, want %q", i, r.OldDecision, "allow")
		}
	}
}

func TestMergeResults_Changed(t *testing.T) {
	old := []simulateResult{
		{Seq: 1, ToolName: "tool_a", NewDecision: "allow"},
		{Seq: 2, ToolName: "tool_b", NewDecision: "allow"},
	}
	newR := []simulateResult{
		{Seq: 1, ToolName: "tool_a", NewDecision: "allow"},
		{Seq: 2, ToolName: "tool_b", NewDecision: "deny", Reason: "sequence.only_after: not satisfied"},
	}
	merged := mergeResults(old, newR)
	if merged[0].Changed {
		t.Error("entry 0: Changed should be false (same decision)")
	}
	if !merged[1].Changed {
		t.Error("entry 1: Changed should be true (allow → deny)")
	}
	if merged[1].OldDecision != "allow" {
		t.Errorf("entry 1: OldDecision = %q, want %q", merged[1].OldDecision, "allow")
	}
	if merged[1].NewDecision != "deny" {
		t.Errorf("entry 1: NewDecision = %q, want %q", merged[1].NewDecision, "deny")
	}
}

// --- printSimulateTable ---

func TestPrintSimulateTable_EmptyResults(t *testing.T) {
	var buf bytes.Buffer
	printSimulateTable(nil, "abc12345", false, &buf)
	out := buf.String()
	if !strings.Contains(out, "Policy:") {
		t.Errorf("output should contain Policy: line, got:\n%s", out)
	}
	if !strings.Contains(out, "0 call(s)") {
		t.Errorf("output should report 0 call(s), got:\n%s", out)
	}
}

func TestPrintSimulateTable_NoDiff(t *testing.T) {
	results := []simulateResult{
		{Seq: 1, ToolName: "tool_a", NewDecision: "allow"},
		{Seq: 2, ToolName: "tool_b", NewDecision: "deny", Reason: "sequence.only_after: required tool not called"},
	}
	var buf bytes.Buffer
	printSimulateTable(results, "abc12345", false, &buf)
	out := buf.String()

	if !strings.Contains(out, "tool_a") {
		t.Errorf("output should contain tool_a, got:\n%s", out)
	}
	if !strings.Contains(out, "deny") {
		t.Errorf("output should contain deny decision, got:\n%s", out)
	}
	// Summary line should tally allow and deny counts.
	if !strings.Contains(out, "1 allow, 1 deny") {
		t.Errorf("summary should report 1 allow and 1 deny, got:\n%s", out)
	}
}

func TestPrintSimulateTable_DiffMode(t *testing.T) {
	results := []simulateResult{
		{Seq: 1, ToolName: "tool_a", OldDecision: "allow", NewDecision: "allow", Changed: false},
		{Seq: 2, ToolName: "tool_b", OldDecision: "allow", NewDecision: "deny",
			Changed: true, Reason: "sequence.only_after: tool_x not called"},
	}
	var buf bytes.Buffer
	printSimulateTable(results, "abc12345", true, &buf)
	out := buf.String()

	if !strings.Contains(out, "old_decision") {
		t.Errorf("diff mode: output should have old_decision header, got:\n%s", out)
	}
	// Changed denial should appear as upper-case.
	if !strings.Contains(out, "DENY") {
		t.Errorf("diff mode: changed denial should appear as DENY, got:\n%s", out)
	}
	if !strings.Contains(out, "◄──") {
		t.Errorf("diff mode: changed row should contain ◄── marker, got:\n%s", out)
	}
	if !strings.Contains(out, "1 decision(s) changed") {
		t.Errorf("diff mode: summary should report 1 change, got:\n%s", out)
	}
}

func TestPrintSimulateTable_LongReasonTruncated(t *testing.T) {
	longReason := strings.Repeat("x", 80)
	results := []simulateResult{
		{Seq: 1, ToolName: "tool_a", NewDecision: "deny", Reason: longReason},
	}
	var buf bytes.Buffer
	printSimulateTable(results, "fp", false, &buf)
	out := buf.String()
	if strings.Contains(out, longReason) {
		t.Error("long reason should be truncated, but full string appeared in output")
	}
	if !strings.Contains(out, "...") {
		t.Errorf("truncated reason should end with '...', got:\n%s", out)
	}
}

// --- parseRFC3339OrNow ---

func TestParseRFC3339OrNow_Valid(t *testing.T) {
	got := parseRFC3339OrNow("2026-02-28T10:00:00Z")
	if got.IsZero() {
		t.Error("expected non-zero time for valid RFC3339 input")
	}
	if got.Year() != 2026 {
		t.Errorf("year: got %d, want 2026", got.Year())
	}
}

func TestParseRFC3339OrNow_Empty(t *testing.T) {
	got := parseRFC3339OrNow("")
	if got.IsZero() {
		t.Error("expected non-zero time (time.Now()) for empty input")
	}
}

func TestParseRFC3339OrNow_Invalid(t *testing.T) {
	got := parseRFC3339OrNow("not-a-timestamp")
	if got.IsZero() {
		t.Error("expected non-zero time (time.Now()) for unparseable input")
	}
}

// --- integration: canonical payment-sequence-violation trace ---

// TestRunSimulate_PaymentSequenceViolation exercises the full simulate pipeline
// against the canonical trace fixture and asserts the satisfaction check from
// TODO.md Task 5.4: execute_wire_transfer is DENY because manager_approval was
// skipped.
func TestRunSimulate_PaymentSequenceViolation(t *testing.T) {
	traceFile := filepath.Join("..", "testdata", "traces", "payment-sequence-violation.trace.jsonl")
	policyFile := filepath.Join("..", "testdata", "policies", "fintech-payment-sequence.policy.yaml")

	for _, f := range []string{traceFile, policyFile} {
		if _, err := os.Stat(f); err != nil {
			t.Skipf("fixture not found (%v); skipping integration test", err)
		}
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runSimulate(traceFile, policyFile, "", cmd); err != nil {
		t.Fatalf("runSimulate returned error: %v", err)
	}

	out := buf.String()
	t.Logf("simulate output:\n%s", out)

	// read_invoice and verify_invoice must be allowed.
	if !strings.Contains(out, "allow") {
		t.Errorf("output should show allow decisions; got:\n%s", out)
	}
	// execute_wire_transfer must be denied (manager_approval missing).
	if !strings.Contains(out, "deny") {
		t.Errorf("output should show a deny decision; got:\n%s", out)
	}
	// The denial reason must mention the missing prerequisite.
	if !strings.Contains(out, "manager_approval") {
		t.Errorf("deny reason should mention manager_approval; got:\n%s", out)
	}
	// Summary should tally exactly 1 deny.
	if !strings.Contains(out, "1 deny") {
		t.Errorf("summary should report 1 deny; got:\n%s", out)
	}
}

// --- rate-limit timestamp accuracy ---

// TestEvaluateSession_RateLimitSpreadAllowed verifies that six calls to a
// rate-limited tool, spread two minutes apart over ten minutes, are all allowed
// when simulated. The fix under test is that AppendEvent receives the original
// trace timestamp (not time.Now()), so CountSessionEventsSince correctly finds
// zero calls inside the 60-second window for each successive call.
func TestEvaluateSession_RateLimitSpreadAllowed(t *testing.T) {
	polYAML := `
version: "1.0"
agent: search-agent
enforcement_mode: block
may_use:
  - search_web
tools:
  search_web:
    rate_limit:
      max_calls: 5
      window_seconds: 60
`
	pol, err := policy.ParseBytes([]byte(polYAML), "")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	st := store.NewTestDB(t)
	pipeline := engine.New(
		&engine.MayUseEvaluator{},
		&engine.RateLimitEvaluator{Store: &engine.StoreBackend{Store: st}},
	)

	// Base time: fixed so the test is deterministic.
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	// Six calls, each two minutes apart. No 60-second window contains ≥5 calls,
	// so every call must be allowed.
	entries := make([]traceEntry, 6)
	for i := range entries {
		entries[i] = traceEntry{
			SessionID:   "sess-rate-spread",
			AgentName:   "search-agent",
			ToolName:    "search_web",
			RequestedAt: base.Add(time.Duration(i) * 2 * time.Minute).Format(time.RFC3339),
		}
	}

	results, err := evaluateSession(context.Background(), entries, pol, st, pipeline)
	if err != nil {
		t.Fatalf("evaluateSession: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}
	for i, r := range results {
		if r.NewDecision != string(engine.Allow) {
			t.Errorf("call %d (T+%dm): want allow, got %s (reason: %s)",
				i+1, i*2, r.NewDecision, r.Reason)
		}
	}
}

// TestEvaluateSession_RateLimitBurstDenied verifies that when six calls to a
// rate-limited tool occur within a single 60-second window, the sixth call is
// denied. This confirms that the timestamp fix does not suppress legitimate
// rate-limit enforcement.
func TestEvaluateSession_RateLimitBurstDenied(t *testing.T) {
	polYAML := `
version: "1.0"
agent: search-agent
enforcement_mode: block
may_use:
  - search_web
tools:
  search_web:
    rate_limit:
      max_calls: 5
      window_seconds: 60
`
	pol, err := policy.ParseBytes([]byte(polYAML), "")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	st := store.NewTestDB(t)
	pipeline := engine.New(
		&engine.MayUseEvaluator{},
		&engine.RateLimitEvaluator{Store: &engine.StoreBackend{Store: st}},
	)

	// Six calls within 10 seconds — all inside the 60-second window.
	// The 6th call must be denied (5 preceding allowed calls are in the window).
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	entries := make([]traceEntry, 6)
	for i := range entries {
		entries[i] = traceEntry{
			SessionID:   "sess-rate-burst",
			AgentName:   "search-agent",
			ToolName:    "search_web",
			RequestedAt: base.Add(time.Duration(i) * 2 * time.Second).Format(time.RFC3339),
		}
	}

	results, err := evaluateSession(context.Background(), entries, pol, st, pipeline)
	if err != nil {
		t.Fatalf("evaluateSession: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}
	// First five calls must be allowed.
	for i := 0; i < 5; i++ {
		if results[i].NewDecision != string(engine.Allow) {
			t.Errorf("call %d: want allow, got %s", i+1, results[i].NewDecision)
		}
	}
	// Sixth call must be denied.
	if results[5].NewDecision != string(engine.Deny) {
		t.Errorf("call 6: want deny (rate limit), got %s (reason: %s)",
			results[5].NewDecision, results[5].Reason)
	}
}

// --- helpers ---

// writeTempTrace writes content to a temporary file and returns its path.
// The file is cleaned up automatically via t.TempDir().
func writeTempTrace(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.trace.jsonl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing temp trace: %v", err)
	}
	return path
}
