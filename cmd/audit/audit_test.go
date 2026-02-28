package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// seedAuditRecord is a test helper that inserts one audit record into st.
// All non-essential fields receive deterministic placeholder values so that
// tests focus on the fields under test (session_id, tool_name, decision).
func seedAuditRecord(t *testing.T, st *store.Store, id, sessionID string, seq uint64, toolName, decision string) {
	t.Helper()
	err := st.AppendAuditRecord(
		id, sessionID, seq,
		"test-agent", toolName,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", // sha256("")
		decision, "",
		"fp-test", "jwtsha-test",
		"",
		time.Now().UnixNano(),
		"sig-placeholder",
	)
	if err != nil {
		t.Fatalf("seedAuditRecord %s: %v", id, err)
	}
}

// --- buildAuditFilter tests ---

func TestBuildAuditFilter_EmptyInputs(t *testing.T) {
	filter, err := buildAuditFilter("", "", "", "", "", "")
	if err != nil {
		t.Fatalf("buildAuditFilter with all-empty inputs: unexpected error: %v", err)
	}
	if filter.SessionID != "" || filter.ToolName != "" || filter.Decision != "" || filter.TraceID != "" {
		t.Errorf("buildAuditFilter: expected all string fields empty, got %+v", filter)
	}
	if !filter.From.IsZero() || !filter.To.IsZero() {
		t.Errorf("buildAuditFilter: expected From and To to be zero time, got From=%v To=%v",
			filter.From, filter.To)
	}
}

func TestBuildAuditFilter_StringFields(t *testing.T) {
	filter, err := buildAuditFilter("sess-1", "my_tool", "deny", "traceparent=abc", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filter.SessionID != "sess-1" {
		t.Errorf("SessionID: got %q, want %q", filter.SessionID, "sess-1")
	}
	if filter.ToolName != "my_tool" {
		t.Errorf("ToolName: got %q, want %q", filter.ToolName, "my_tool")
	}
	if filter.Decision != "deny" {
		t.Errorf("Decision: got %q, want %q", filter.Decision, "deny")
	}
	if filter.TraceID != "traceparent=abc" {
		t.Errorf("TraceID: got %q, want %q", filter.TraceID, "traceparent=abc")
	}
}

func TestBuildAuditFilter_ValidTimestamps(t *testing.T) {
	from := "2026-01-15T00:00:00Z"
	to := "2026-01-16T23:59:59Z"
	filter, err := buildAuditFilter("", "", "", "", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrom, _ := time.Parse(time.RFC3339, from)
	wantTo, _ := time.Parse(time.RFC3339, to)

	if !filter.From.Equal(wantFrom) {
		t.Errorf("From: got %v, want %v", filter.From, wantFrom)
	}
	if !filter.To.Equal(wantTo) {
		t.Errorf("To: got %v, want %v", filter.To, wantTo)
	}
}

func TestBuildAuditFilter_InvalidFromTimestamp(t *testing.T) {
	_, err := buildAuditFilter("", "", "", "", "not-a-time", "")
	if err == nil {
		t.Fatal("expected error for invalid --from timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("error message should mention --from, got: %v", err)
	}
}

func TestBuildAuditFilter_InvalidToTimestamp(t *testing.T) {
	_, err := buildAuditFilter("", "", "", "", "", "2026/01/01")
	if err == nil {
		t.Fatal("expected error for invalid --to timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "--to") {
		t.Errorf("error message should mention --to, got: %v", err)
	}
}

// --- groupAuditBySession tests ---

func makeTestAuditLine(sessionID string, seq uint64, toolName, decision string) auditLogLine {
	return auditLogLine{
		ID:        "rec-" + sessionID + "-" + toolName,
		SessionID: sessionID,
		Seq:       seq,
		AgentName: "test-agent",
		ToolName:  toolName,
		Decision:  decision,
	}
}

func TestGroupAuditBySession_SingleSession(t *testing.T) {
	records := []auditLogLine{
		makeTestAuditLine("sess-a", 2, "tool_b", "allow"),
		makeTestAuditLine("sess-a", 1, "tool_a", "allow"),
		makeTestAuditLine("sess-a", 3, "tool_c", "deny"),
	}

	groups := groupAuditBySession(records)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0]) != 3 {
		t.Fatalf("expected 3 records in group, got %d", len(groups[0]))
	}
	// Records must be sorted by seq ascending.
	if groups[0][0].Seq != 1 || groups[0][1].Seq != 2 || groups[0][2].Seq != 3 {
		t.Errorf("records not sorted by seq: got seqs %d, %d, %d",
			groups[0][0].Seq, groups[0][1].Seq, groups[0][2].Seq)
	}
}

func TestGroupAuditBySession_MultipleSessionsPreservesOrder(t *testing.T) {
	// sess-b appears before sess-a in the file; the group order must reflect
	// first-encounter order, not alphabetical order.
	records := []auditLogLine{
		makeTestAuditLine("sess-b", 1, "tool_x", "allow"),
		makeTestAuditLine("sess-a", 1, "tool_a", "allow"),
		makeTestAuditLine("sess-b", 2, "tool_y", "deny"),
		makeTestAuditLine("sess-a", 2, "tool_b", "allow"),
	}

	groups := groupAuditBySession(records)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	// First group must be sess-b (first encountered).
	if groups[0][0].SessionID != "sess-b" {
		t.Errorf("first group: expected sess-b, got %s", groups[0][0].SessionID)
	}
	// Second group must be sess-a.
	if groups[1][0].SessionID != "sess-a" {
		t.Errorf("second group: expected sess-a, got %s", groups[1][0].SessionID)
	}
	// Each group sorted by seq.
	if groups[0][0].Seq != 1 || groups[0][1].Seq != 2 {
		t.Errorf("sess-b group not sorted: seqs %d, %d", groups[0][0].Seq, groups[0][1].Seq)
	}
}

func TestGroupAuditBySession_Empty(t *testing.T) {
	groups := groupAuditBySession(nil)
	if len(groups) != 0 {
		t.Errorf("expected 0 groups for nil input, got %d", len(groups))
	}
}

// --- writeQueryTable tests ---

func TestWriteQueryTable_NoRecords(t *testing.T) {
	var buf bytes.Buffer
	if err := writeQueryTable(nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "no records") {
		t.Errorf("expected 'no records' message, got: %q", buf.String())
	}
}

func TestWriteQueryTable_WithRecords(t *testing.T) {
	records := []store.AuditRecord{
		{
			ID:             "rec-001",
			SessionID:      "sess-abc",
			Seq:            1,
			AgentName:      "test-agent",
			ToolName:       "read_invoice",
			Decision:       "allow",
			DecisionReason: "",
			RecordedAt:     time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC).UnixNano(),
		},
		{
			ID:             "rec-002",
			SessionID:      "sess-abc",
			Seq:            2,
			AgentName:      "test-agent",
			ToolName:       "execute_wire_transfer",
			Decision:       "deny",
			DecisionReason: "sequence.only_after: manager_approval not satisfied",
			RecordedAt:     time.Date(2026, 1, 15, 12, 1, 0, 0, time.UTC).UnixNano(),
		},
	}

	var buf bytes.Buffer
	if err := writeQueryTable(records, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "read_invoice") {
		t.Errorf("expected 'read_invoice' in table output, got:\n%s", out)
	}
	if !strings.Contains(out, "execute_wire_transfer") {
		t.Errorf("expected 'execute_wire_transfer' in table output, got:\n%s", out)
	}
	if !strings.Contains(out, "deny") {
		t.Errorf("expected 'deny' decision in table output, got:\n%s", out)
	}
}

func TestWriteQueryTable_LongReasonTruncated(t *testing.T) {
	longReason := strings.Repeat("x", 100)
	records := []store.AuditRecord{
		{
			ID:             "rec-001",
			SessionID:      "sess-abc",
			Seq:            1,
			AgentName:      "agent",
			ToolName:       "some_tool",
			Decision:       "deny",
			DecisionReason: longReason,
			RecordedAt:     time.Now().UnixNano(),
		},
	}

	var buf bytes.Buffer
	if err := writeQueryTable(records, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	// The full 100-char reason must not appear; a truncated version must.
	if strings.Contains(out, longReason) {
		t.Error("expected reason to be truncated but found full string in output")
	}
	if !strings.Contains(out, "...") {
		t.Error("expected '...' truncation indicator in output")
	}
}

// --- writeQueryJSON tests ---

func TestWriteQueryJSON_EmptySlice(t *testing.T) {
	var buf bytes.Buffer
	if err := writeQueryJSON([]store.AuditRecord{}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// An empty record set must produce empty JSONL output (no lines), not a
	// JSON null or empty array. audit verify treats an empty file as "no records
	// found", which is the correct behaviour when there is nothing to verify.
	if buf.Len() != 0 {
		t.Errorf("expected empty output for zero records, got %q", buf.String())
	}
}

func TestWriteQueryJSON_WithRecord(t *testing.T) {
	records := []store.AuditRecord{
		{
			ID:       "rec-json-001",
			Decision: "allow",
		},
	}

	var buf bytes.Buffer
	if err := writeQueryJSON(records, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "rec-json-001") {
		t.Errorf("expected record ID in JSON output, got:\n%s", out)
	}

	// Output must be JSONL: exactly one non-empty line for one record.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 JSONL line for 1 record, got %d lines", len(lines))
	}

	// Keys must be snake_case (from json tags on store.AuditRecord) so that
	// audit verify can unmarshal the output into internal/audit.AuditRecord
	// without field loss.
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("JSONL line is not valid JSON: %v\nline: %q", err, lines[0])
	}
	for _, want := range []string{"id", "decision"} {
		if _, ok := obj[want]; !ok {
			t.Errorf("expected snake_case key %q in JSON output; got keys: %v", want, obj)
		}
	}
}

func TestWriteQueryJSON_MultipleRecords(t *testing.T) {
	records := []store.AuditRecord{
		{ID: "rec-a", Decision: "allow"},
		{ID: "rec-b", Decision: "deny"},
	}

	var buf bytes.Buffer
	if err := writeQueryJSON(records, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each record must appear on its own line.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines for 2 records, got %d", len(lines))
	}
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i+1, err)
		}
	}
}

// --- writeQueryCSV tests ---

func TestWriteQueryCSV_HeaderAlwaysPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := writeQueryCSV(nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "recorded_at") || !strings.Contains(out, "session_id") {
		t.Errorf("expected CSV header in output, got:\n%s", out)
	}
}

func TestWriteQueryCSV_WithRecord(t *testing.T) {
	records := []store.AuditRecord{
		{
			ID:             "rec-csv-001",
			SessionID:      "sess-csv",
			Seq:            5,
			AgentName:      "billing-agent",
			ToolName:       "submit_claim",
			Decision:       "shadow_deny",
			DecisionReason: "sequence violation",
			RecordedAt:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).UnixNano(),
		},
	}

	var buf bytes.Buffer
	if err := writeQueryCSV(records, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "submit_claim") {
		t.Errorf("expected 'submit_claim' in CSV output, got:\n%s", out)
	}
	if !strings.Contains(out, "shadow_deny") {
		t.Errorf("expected 'shadow_deny' in CSV output, got:\n%s", out)
	}
	if !strings.Contains(out, "2026-02-01T00:00:00Z") {
		t.Errorf("expected RFC3339 timestamp in CSV output, got:\n%s", out)
	}
}

// --- writeExport tests ---

func TestWriteExport_EmptyDB(t *testing.T) {
	st := store.NewTestDB(t)
	var buf bytes.Buffer
	if err := writeExport(st, store.AuditFilter{}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// An empty database must produce zero bytes — no JSON null, no empty array.
	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty database, got %q", buf.String())
	}
}

func TestWriteExport_NoFilter_AllRecords(t *testing.T) {
	st := store.NewTestDB(t)
	seedAuditRecord(t, st, "rec-exp-001", "sess-a", 1, "read_invoice", "allow")
	seedAuditRecord(t, st, "rec-exp-002", "sess-b", 1, "submit_claim", "deny")
	seedAuditRecord(t, st, "rec-exp-003", "sess-a", 2, "verify_invoice", "allow")

	var buf bytes.Buffer
	if err := writeExport(st, store.AuditFilter{}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()

	// All three records must appear.
	for _, wantID := range []string{"rec-exp-001", "rec-exp-002", "rec-exp-003"} {
		if !strings.Contains(out, wantID) {
			t.Errorf("expected record %q in export output, got:\n%s", wantID, out)
		}
	}

	// Output must be JSONL: exactly three non-empty lines.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines for 3 records, got %d: %q", len(lines), out)
	}

	// Each line must be valid JSON with snake_case keys.
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v\nline: %q", i+1, err, line)
		}
		if _, ok := obj["id"]; !ok {
			t.Errorf("line %d: expected snake_case key \"id\", got keys: %v", i+1, obj)
		}
		if _, ok := obj["session_id"]; !ok {
			t.Errorf("line %d: expected snake_case key \"session_id\", got keys: %v", i+1, obj)
		}
	}
}

func TestWriteExport_SessionFilter_FiltersCorrectly(t *testing.T) {
	st := store.NewTestDB(t)
	seedAuditRecord(t, st, "rec-sess-a-1", "sess-target", 1, "tool_x", "allow")
	seedAuditRecord(t, st, "rec-sess-a-2", "sess-target", 2, "tool_y", "deny")
	seedAuditRecord(t, st, "rec-sess-b-1", "sess-other", 1, "tool_z", "allow")

	filter := store.AuditFilter{SessionID: "sess-target"}
	var buf bytes.Buffer
	if err := writeExport(st, filter, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()

	// Only sess-target records must appear.
	if !strings.Contains(out, "rec-sess-a-1") {
		t.Errorf("expected rec-sess-a-1 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "rec-sess-a-2") {
		t.Errorf("expected rec-sess-a-2 in output, got:\n%s", out)
	}
	if strings.Contains(out, "rec-sess-b-1") {
		t.Errorf("record from sess-other must not appear in session-filtered output, got:\n%s", out)
	}

	// Must be exactly two JSONL lines (one per sess-target record).
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines for session-filtered export, got %d: %q", len(lines), out)
	}
}

func TestWriteExport_ValidJSONL_NoTrailingComma(t *testing.T) {
	st := store.NewTestDB(t)
	seedAuditRecord(t, st, "rec-nl-1", "sess-nl", 1, "tool_a", "allow")
	seedAuditRecord(t, st, "rec-nl-2", "sess-nl", 2, "tool_b", "allow")

	var buf bytes.Buffer
	if err := writeExport(st, store.AuditFilter{}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := buf.String()

	// JSONL must not end with a comma.
	if strings.HasSuffix(strings.TrimRight(raw, "\n"), ",") {
		t.Errorf("JSONL output must not end with a trailing comma, got: %q", raw)
	}

	// Every line must be individually parseable JSON.
	for i, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v\nline: %q", i+1, err, line)
		}
	}
}
