package store_test

import (
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// appendRecord is a test helper that calls AppendAuditRecord with the given
// fields and fails the test immediately on error.
func appendRecord(t *testing.T, st *store.Store, r store.AuditRecord) {
	t.Helper()
	err := st.AppendAuditRecord(
		r.ID, r.SessionID, r.Seq,
		r.AgentName, r.ToolName, r.ArgumentsSHA256,
		r.Decision, r.DecisionReason,
		r.PolicyFingerprint, r.AgentJWTSHA256,
		r.ClientTraceID,
		r.DelegationChain,
		r.RecordedAt,
		r.Signature,
	)
	if err != nil {
		t.Fatalf("appendRecord %q: %v", r.ID, err)
	}
}

// baseRecord returns a valid AuditRecord with sensible defaults. Tests override
// individual fields to exercise specific filter paths.
func baseRecord(id, sessionID, toolName, decision string, recordedAt int64) store.AuditRecord {
	return store.AuditRecord{
		ID:                id,
		SessionID:         sessionID,
		Seq:               1,
		AgentName:         "test-agent",
		ToolName:          toolName,
		ArgumentsSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Decision:          decision,
		DecisionReason:    "",
		PolicyFingerprint: "fp-abc123",
		AgentJWTSHA256:    "jwtsha-abc",
		ClientTraceID:     "",
		RecordedAt:        recordedAt,
		Signature:         "sig-placeholder",
	}
}

// epoch is a fixed reference point used to produce deterministic timestamps in
// tests without relying on time.Now(). All test timestamps are offsets from epoch.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestQueryAuditLog_NoFilter_ReturnsAll(t *testing.T) {
	st := store.NewTestDB(t)

	r1 := baseRecord("rec-1", "sess-a", "tool-x", "allow", epoch.UnixNano())
	r2 := baseRecord("rec-2", "sess-b", "tool-y", "deny", epoch.Add(time.Second).UnixNano())
	appendRecord(t, st, r1)
	appendRecord(t, st, r2)

	got, err := st.QueryAuditLog(store.AuditFilter{})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records with no filter; got %d", len(got))
	}
}

func TestQueryAuditLog_FilterBySessionID(t *testing.T) {
	st := store.NewTestDB(t)

	appendRecord(t, st, baseRecord("rec-a1", "sess-alpha", "tool-x", "allow", epoch.UnixNano()))
	appendRecord(t, st, baseRecord("rec-b1", "sess-beta", "tool-x", "allow", epoch.Add(time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-a2", "sess-alpha", "tool-y", "deny", epoch.Add(2*time.Second).UnixNano()))

	got, err := st.QueryAuditLog(store.AuditFilter{SessionID: "sess-alpha"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records for sess-alpha; got %d", len(got))
	}
	for _, r := range got {
		if r.SessionID != "sess-alpha" {
			t.Errorf("unexpected session ID %q in result", r.SessionID)
		}
	}
}

func TestQueryAuditLog_FilterByToolName(t *testing.T) {
	st := store.NewTestDB(t)

	appendRecord(t, st, baseRecord("rec-t1", "sess-1", "read-tool", "allow", epoch.UnixNano()))
	appendRecord(t, st, baseRecord("rec-t2", "sess-1", "write-tool", "deny", epoch.Add(time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-t3", "sess-2", "read-tool", "allow", epoch.Add(2*time.Second).UnixNano()))

	got, err := st.QueryAuditLog(store.AuditFilter{ToolName: "read-tool"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records for read-tool; got %d", len(got))
	}
	for _, r := range got {
		if r.ToolName != "read-tool" {
			t.Errorf("unexpected tool name %q in result", r.ToolName)
		}
	}
}

func TestQueryAuditLog_FilterByDecision(t *testing.T) {
	st := store.NewTestDB(t)

	appendRecord(t, st, baseRecord("rec-d1", "sess-1", "tool-x", "allow", epoch.UnixNano()))
	appendRecord(t, st, baseRecord("rec-d2", "sess-1", "tool-y", "deny", epoch.Add(time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-d3", "sess-2", "tool-z", "shadow_deny", epoch.Add(2*time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-d4", "sess-2", "tool-x", "deny", epoch.Add(3*time.Second).UnixNano()))

	got, err := st.QueryAuditLog(store.AuditFilter{Decision: "deny"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 deny records; got %d", len(got))
	}
	for _, r := range got {
		if r.Decision != "deny" {
			t.Errorf("expected decision=deny; got %q", r.Decision)
		}
	}
}

func TestQueryAuditLog_FilterByTraceID(t *testing.T) {
	st := store.NewTestDB(t)

	r1 := baseRecord("rec-tr1", "sess-1", "tool-x", "allow", epoch.UnixNano())
	r1.ClientTraceID = "traceparent=00-abc123-01"

	r2 := baseRecord("rec-tr2", "sess-1", "tool-y", "deny", epoch.Add(time.Second).UnixNano())
	r2.ClientTraceID = "x-datadog-trace-id=xyz789"

	r3 := baseRecord("rec-tr3", "sess-2", "tool-x", "allow", epoch.Add(2*time.Second).UnixNano())
	// no trace ID

	appendRecord(t, st, r1)
	appendRecord(t, st, r2)
	appendRecord(t, st, r3)

	got, err := st.QueryAuditLog(store.AuditFilter{TraceID: "traceparent=00-abc123-01"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record for trace ID; got %d", len(got))
	}
	if got[0].ClientTraceID != "traceparent=00-abc123-01" {
		t.Errorf("unexpected ClientTraceID %q", got[0].ClientTraceID)
	}
}

func TestQueryAuditLog_FilterByFrom(t *testing.T) {
	st := store.NewTestDB(t)

	t0 := epoch
	t1 := epoch.Add(10 * time.Second)
	t2 := epoch.Add(20 * time.Second)
	t3 := epoch.Add(30 * time.Second)

	appendRecord(t, st, baseRecord("rec-f1", "sess-1", "tool-x", "allow", t0.UnixNano()))
	appendRecord(t, st, baseRecord("rec-f2", "sess-1", "tool-y", "deny", t1.UnixNano()))
	appendRecord(t, st, baseRecord("rec-f3", "sess-1", "tool-z", "allow", t2.UnixNano()))
	appendRecord(t, st, baseRecord("rec-f4", "sess-1", "tool-x", "deny", t3.UnixNano()))

	// From t1 (inclusive): should return rec-f2, rec-f3, rec-f4.
	got, err := st.QueryAuditLog(store.AuditFilter{From: t1})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records with From=%v; got %d", t1, len(got))
	}
	for _, r := range got {
		if r.RecordedAt < t1.UnixNano() {
			t.Errorf("record %q has RecordedAt %d before From %d", r.ID, r.RecordedAt, t1.UnixNano())
		}
	}
}

func TestQueryAuditLog_FilterByTo(t *testing.T) {
	st := store.NewTestDB(t)

	t0 := epoch
	t1 := epoch.Add(10 * time.Second)
	t2 := epoch.Add(20 * time.Second)
	t3 := epoch.Add(30 * time.Second)

	appendRecord(t, st, baseRecord("rec-to1", "sess-1", "tool-x", "allow", t0.UnixNano()))
	appendRecord(t, st, baseRecord("rec-to2", "sess-1", "tool-y", "deny", t1.UnixNano()))
	appendRecord(t, st, baseRecord("rec-to3", "sess-1", "tool-z", "allow", t2.UnixNano()))
	appendRecord(t, st, baseRecord("rec-to4", "sess-1", "tool-x", "deny", t3.UnixNano()))

	// To t2 (inclusive): should return rec-to1, rec-to2, rec-to3.
	got, err := st.QueryAuditLog(store.AuditFilter{To: t2})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records with To=%v; got %d", t2, len(got))
	}
	for _, r := range got {
		if r.RecordedAt > t2.UnixNano() {
			t.Errorf("record %q has RecordedAt %d after To %d", r.ID, r.RecordedAt, t2.UnixNano())
		}
	}
}

func TestQueryAuditLog_FilterByFromAndTo(t *testing.T) {
	st := store.NewTestDB(t)

	t0 := epoch
	t1 := epoch.Add(10 * time.Second)
	t2 := epoch.Add(20 * time.Second)
	t3 := epoch.Add(30 * time.Second)

	appendRecord(t, st, baseRecord("rec-r1", "sess-1", "tool-x", "allow", t0.UnixNano()))
	appendRecord(t, st, baseRecord("rec-r2", "sess-1", "tool-y", "deny", t1.UnixNano()))
	appendRecord(t, st, baseRecord("rec-r3", "sess-1", "tool-z", "allow", t2.UnixNano()))
	appendRecord(t, st, baseRecord("rec-r4", "sess-1", "tool-x", "deny", t3.UnixNano()))

	// From t1 to t2 (both inclusive): should return rec-r2, rec-r3.
	got, err := st.QueryAuditLog(store.AuditFilter{From: t1, To: t2})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records in [t1, t2]; got %d", len(got))
	}
}

func TestQueryAuditLog_CombinedFilters_SessionAndDecision(t *testing.T) {
	st := store.NewTestDB(t)

	appendRecord(t, st, baseRecord("rec-c1", "sess-x", "tool-a", "allow", epoch.UnixNano()))
	appendRecord(t, st, baseRecord("rec-c2", "sess-x", "tool-b", "deny", epoch.Add(time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-c3", "sess-y", "tool-a", "deny", epoch.Add(2*time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-c4", "sess-x", "tool-c", "deny", epoch.Add(3*time.Second).UnixNano()))

	// Only deny records for sess-x.
	got, err := st.QueryAuditLog(store.AuditFilter{SessionID: "sess-x", Decision: "deny"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 deny records for sess-x; got %d", len(got))
	}
	for _, r := range got {
		if r.SessionID != "sess-x" || r.Decision != "deny" {
			t.Errorf("unexpected record session=%q decision=%q", r.SessionID, r.Decision)
		}
	}
}

func TestQueryAuditLog_EmptyResult(t *testing.T) {
	st := store.NewTestDB(t)

	appendRecord(t, st, baseRecord("rec-e1", "sess-1", "tool-x", "allow", epoch.UnixNano()))

	got, err := st.QueryAuditLog(store.AuditFilter{SessionID: "sess-nonexistent"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 records for nonexistent session; got %d", len(got))
	}
}

func TestQueryAuditLog_NullableFieldsRoundTrip(t *testing.T) {
	st := store.NewTestDB(t)

	// Insert a deny record with a non-empty DecisionReason and a ClientTraceID,
	// then verify both fields survive the round-trip through the database.
	r := baseRecord("rec-n1", "sess-nullable", "tool-x", "deny", epoch.UnixNano())
	r.DecisionReason = "sequence.only_after: prerequisite-tool not satisfied"
	r.ClientTraceID = "traceparent=00-deadbeef-01"

	appendRecord(t, st, r)

	got, err := st.QueryAuditLog(store.AuditFilter{SessionID: "sess-nullable"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record; got %d", len(got))
	}
	if got[0].DecisionReason != r.DecisionReason {
		t.Errorf("DecisionReason: got %q; want %q", got[0].DecisionReason, r.DecisionReason)
	}
	if got[0].ClientTraceID != r.ClientTraceID {
		t.Errorf("ClientTraceID: got %q; want %q", got[0].ClientTraceID, r.ClientTraceID)
	}
}

func TestQueryAuditLog_EmptyNullableFieldsAreEmptyStrings(t *testing.T) {
	st := store.NewTestDB(t)

	// An allow record has no DecisionReason and no ClientTraceID.
	// Both must come back as empty strings (not panicking on nil pointer deref).
	r := baseRecord("rec-n2", "sess-allow", "tool-x", "allow", epoch.UnixNano())
	appendRecord(t, st, r)

	got, err := st.QueryAuditLog(store.AuditFilter{SessionID: "sess-allow"})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record; got %d", len(got))
	}
	if got[0].DecisionReason != "" {
		t.Errorf("expected empty DecisionReason for allow record; got %q", got[0].DecisionReason)
	}
	if got[0].ClientTraceID != "" {
		t.Errorf("expected empty ClientTraceID for allow record; got %q", got[0].ClientTraceID)
	}
}

func TestHasAuditRecordsForFingerprint(t *testing.T) {
	cases := []struct {
		name        string
		setup       func(t *testing.T, st *store.Store)
		fingerprint string
		want        bool
	}{
		{
			name: "returns true when a record with the fingerprint exists",
			setup: func(t *testing.T, st *store.Store) {
				r := baseRecord("rec-fp1", "sess-1", "tool-x", "allow", epoch.UnixNano())
				r.PolicyFingerprint = "fp-target"
				appendRecord(t, st, r)
			},
			fingerprint: "fp-target",
			want:        true,
		},
		{
			name: "returns false when no record with the fingerprint exists",
			setup: func(t *testing.T, st *store.Store) {
				r := baseRecord("rec-fp2", "sess-1", "tool-x", "allow", epoch.UnixNano())
				r.PolicyFingerprint = "fp-other"
				appendRecord(t, st, r)
			},
			fingerprint: "fp-target",
			want:        false,
		},
		{
			name:        "returns false on empty audit log",
			setup:       func(_ *testing.T, _ *store.Store) {},
			fingerprint: "fp-target",
			want:        false,
		},
		{
			name: "returns true when multiple records exist for the fingerprint",
			setup: func(t *testing.T, st *store.Store) {
				r1 := baseRecord("rec-fp3", "sess-a", "tool-x", "allow", epoch.UnixNano())
				r1.PolicyFingerprint = "fp-target"
				r2 := baseRecord("rec-fp4", "sess-b", "tool-y", "deny", epoch.Add(time.Second).UnixNano())
				r2.PolicyFingerprint = "fp-target"
				appendRecord(t, st, r1)
				appendRecord(t, st, r2)
			},
			fingerprint: "fp-target",
			want:        true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewTestDB(t)
			tc.setup(t, st)
			got, err := st.HasAuditRecordsForFingerprint(tc.fingerprint)
			if err != nil {
				t.Fatalf("HasAuditRecordsForFingerprint: %v", err)
			}
			if got != tc.want {
				t.Errorf("HasAuditRecordsForFingerprint(%q) = %v, want %v", tc.fingerprint, got, tc.want)
			}
		})
	}
}

func TestQueryAuditLog_OrderByRecordedAtASC(t *testing.T) {
	st := store.NewTestDB(t)

	// Insert records out of chronological order; QueryAuditLog must return them sorted.
	appendRecord(t, st, baseRecord("rec-o3", "sess-1", "tool-x", "allow", epoch.Add(20*time.Second).UnixNano()))
	appendRecord(t, st, baseRecord("rec-o1", "sess-1", "tool-x", "allow", epoch.UnixNano()))
	appendRecord(t, st, baseRecord("rec-o2", "sess-1", "tool-x", "allow", epoch.Add(10*time.Second).UnixNano()))

	got, err := st.QueryAuditLog(store.AuditFilter{})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records; got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].RecordedAt < got[i-1].RecordedAt {
			t.Errorf("records not in ASC order at index %d: %d < %d", i, got[i].RecordedAt, got[i-1].RecordedAt)
		}
	}
}
