package store_test

import (
	"testing"

	"github.com/mercator-hq/truebearing/internal/store"
)

// mustCreateSession is a test helper that creates a session or fails the test.
func mustCreateSession(t *testing.T, db *store.Store, id string) {
	t.Helper()
	if err := db.CreateSession(id, "test-agent", "fp-test"); err != nil {
		t.Fatalf("CreateSession %q: %v", id, err)
	}
}

func TestAppendEvent_AssignsSeqStartingAtOne(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-seq")

	event := &store.SessionEvent{
		SessionID: "sess-seq",
		ToolName:  "read_invoice",
		Decision:  "allow",
	}
	if err := db.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if event.Seq != 1 {
		t.Errorf("Seq after first AppendEvent: got %d, want 1", event.Seq)
	}
}

func TestAppendEvent_MonotonicSeq(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-mono")

	toolNames := []string{"tool_a", "tool_b", "tool_c", "tool_d", "tool_e"}
	for i, name := range toolNames {
		ev := &store.SessionEvent{
			SessionID: "sess-mono",
			ToolName:  name,
			Decision:  "allow",
		}
		if err := db.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
		wantSeq := uint64(i + 1)
		if ev.Seq != wantSeq {
			t.Errorf("event %d Seq: got %d, want %d", i, ev.Seq, wantSeq)
		}
	}
}

func TestAppendEvent_SeqVerifiedViaGetEvents(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-verify")

	for i := 0; i < 5; i++ {
		ev := &store.SessionEvent{
			SessionID: "sess-verify",
			ToolName:  "tool",
			Decision:  "allow",
		}
		if err := db.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}

	events, err := db.GetSessionEvents("sess-verify")
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len(events): got %d, want 5", len(events))
	}
	for i, ev := range events {
		wantSeq := uint64(i + 1)
		if ev.Seq != wantSeq {
			t.Errorf("events[%d].Seq: got %d, want %d", i, ev.Seq, wantSeq)
		}
	}
}

func TestAppendEvent_SetsRecordedAtIfZero(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-ts")

	ev := &store.SessionEvent{
		SessionID:  "sess-ts",
		ToolName:   "tool_a",
		Decision:   "allow",
		RecordedAt: 0, // zero → should be set by AppendEvent
	}
	if err := db.AppendEvent(ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, _ := db.GetSessionEvents("sess-ts")
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].RecordedAt == 0 {
		t.Error("RecordedAt was not set by AppendEvent when zero")
	}
}

func TestAppendEvent_PreservesExplicitRecordedAt(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-explicit-ts")

	const explicitTS int64 = 1_700_000_000_000_000_000
	ev := &store.SessionEvent{
		SessionID:  "sess-explicit-ts",
		ToolName:   "tool_a",
		Decision:   "allow",
		RecordedAt: explicitTS,
	}
	if err := db.AppendEvent(ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, _ := db.GetSessionEvents("sess-explicit-ts")
	if events[0].RecordedAt != explicitTS {
		t.Errorf("RecordedAt: got %d, want %d", events[0].RecordedAt, explicitTS)
	}
}

func TestAppendEvent_NullableFieldsRoundTrip(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-nullable")

	// Event with empty ArgumentsJSON and PolicyRule — both should round-trip as empty strings.
	ev := &store.SessionEvent{
		SessionID:     "sess-nullable",
		ToolName:      "tool_a",
		Decision:      "allow",
		ArgumentsJSON: "",
		PolicyRule:    "",
	}
	if err := db.AppendEvent(ev); err != nil {
		t.Fatalf("AppendEvent with empty nullable fields: %v", err)
	}

	events, err := db.GetSessionEvents("sess-nullable")
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].ArgumentsJSON != "" {
		t.Errorf("ArgumentsJSON: got %q, want empty string", events[0].ArgumentsJSON)
	}
	if events[0].PolicyRule != "" {
		t.Errorf("PolicyRule: got %q, want empty string", events[0].PolicyRule)
	}
}

func TestAppendEvent_FullFieldsRoundTrip(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-full")

	ev := &store.SessionEvent{
		SessionID:     "sess-full",
		ToolName:      "execute_payment",
		Decision:      "deny",
		ArgumentsJSON: `{"amount_usd":15000}`,
		PolicyRule:    "sequence.only_after",
	}
	if err := db.AppendEvent(ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, _ := db.GetSessionEvents("sess-full")
	got := events[0]

	if got.ToolName != ev.ToolName {
		t.Errorf("ToolName: got %q, want %q", got.ToolName, ev.ToolName)
	}
	if got.Decision != ev.Decision {
		t.Errorf("Decision: got %q, want %q", got.Decision, ev.Decision)
	}
	if got.ArgumentsJSON != ev.ArgumentsJSON {
		t.Errorf("ArgumentsJSON: got %q, want %q", got.ArgumentsJSON, ev.ArgumentsJSON)
	}
	if got.PolicyRule != ev.PolicyRule {
		t.Errorf("PolicyRule: got %q, want %q", got.PolicyRule, ev.PolicyRule)
	}
}

func TestGetSessionEvents_Empty(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-empty-events")

	events, err := db.GetSessionEvents("sess-empty-events")
	if err != nil {
		t.Fatalf("GetSessionEvents on session with no events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestGetSessionEvents_OrderedBySeq(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-order")

	tools := []string{"tool_c", "tool_a", "tool_b"}
	for _, name := range tools {
		ev := &store.SessionEvent{
			SessionID: "sess-order",
			ToolName:  name,
			Decision:  "allow",
		}
		if err := db.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent %q: %v", name, err)
		}
	}

	events, err := db.GetSessionEvents("sess-order")
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events): got %d, want 3", len(events))
	}

	// Events must be in insertion order (seq ASC), not alphabetical order.
	for i, wantTool := range tools {
		if events[i].ToolName != wantTool {
			t.Errorf("events[%d].ToolName: got %q, want %q", i, events[i].ToolName, wantTool)
		}
	}
}

func TestGetSessionEvents_IsolatedBySession(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-a")
	mustCreateSession(t, db, "sess-b")

	for _, ev := range []*store.SessionEvent{
		{SessionID: "sess-a", ToolName: "tool_x", Decision: "allow"},
		{SessionID: "sess-b", ToolName: "tool_y", Decision: "deny"},
		{SessionID: "sess-a", ToolName: "tool_z", Decision: "allow"},
	} {
		if err := db.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	eventsA, _ := db.GetSessionEvents("sess-a")
	eventsB, _ := db.GetSessionEvents("sess-b")

	if len(eventsA) != 2 {
		t.Errorf("sess-a: got %d events, want 2", len(eventsA))
	}
	if len(eventsB) != 1 {
		t.Errorf("sess-b: got %d events, want 1", len(eventsB))
	}

	// sess-a events should have seq 1, 2 (independent of sess-b's seq).
	if eventsA[0].Seq != 1 || eventsA[1].Seq != 2 {
		t.Errorf("sess-a seqs: got %d, %d; want 1, 2", eventsA[0].Seq, eventsA[1].Seq)
	}
	// sess-b events should start at seq 1 independently.
	if eventsB[0].Seq != 1 {
		t.Errorf("sess-b events[0].Seq: got %d, want 1", eventsB[0].Seq)
	}
}

func TestCountSessionEvents(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-count")

	// Zero events initially.
	count, err := db.CountSessionEvents("sess-count")
	if err != nil {
		t.Fatalf("CountSessionEvents on empty session: %v", err)
	}
	if count != 0 {
		t.Errorf("count before any events: got %d, want 0", count)
	}

	// Append three events and verify count tracks correctly.
	for i := 0; i < 3; i++ {
		ev := &store.SessionEvent{
			SessionID: "sess-count",
			ToolName:  "tool",
			Decision:  "allow",
		}
		if err := db.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}

		count, err = db.CountSessionEvents("sess-count")
		if err != nil {
			t.Fatalf("CountSessionEvents after %d events: %v", i+1, err)
		}
		if count != i+1 {
			t.Errorf("count after %d events: got %d, want %d", i+1, count, i+1)
		}
	}
}

func TestCountSessionEvents_MaxHistoryDetectable(t *testing.T) {
	db := store.NewTestDB(t)
	mustCreateSession(t, db, "sess-maxhist")

	const maxHistory = 5
	for i := 0; i < maxHistory; i++ {
		ev := &store.SessionEvent{
			SessionID: "sess-maxhist",
			ToolName:  "tool",
			Decision:  "allow",
		}
		if err := db.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}

	count, err := db.CountSessionEvents("sess-maxhist")
	if err != nil {
		t.Fatalf("CountSessionEvents: %v", err)
	}
	// The caller can compare count >= maxHistory to detect the cap.
	if count < maxHistory {
		t.Errorf("count %d should be >= maxHistory %d", count, maxHistory)
	}
}
