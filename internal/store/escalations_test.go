package store_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/mercator-hq/truebearing/internal/store"
)

// escalationStore returns a test store with a session pre-created so that
// session-scoped escalation queries work without foreign key failures. The
// escalations table has no FK on session_id, so any session ID is valid.
func escalationStore(t *testing.T) *store.Store {
	t.Helper()
	return store.NewTestDB(t)
}

// argHash returns the SHA-256 hex digest of the given raw JSON bytes,
// matching the computation used by HasApprovedEscalation for stored records.
func argHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func TestCreateEscalation_Insert(t *testing.T) {
	st := escalationStore(t)

	e := &store.Escalation{
		ID:            "esc-001",
		SessionID:     "sess-001",
		Seq:           1,
		ToolName:      "guarded-tool",
		ArgumentsJSON: `{"amount":500}`,
		Status:        "pending",
	}
	if err := st.CreateEscalation(e); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}
}

func TestCreateEscalation_DuplicateIDReturnsError(t *testing.T) {
	st := escalationStore(t)

	e := &store.Escalation{
		ID:        "esc-dup",
		SessionID: "sess-dup",
		Seq:       1,
		ToolName:  "tool-x",
		Status:    "pending",
	}
	if err := st.CreateEscalation(e); err != nil {
		t.Fatalf("first CreateEscalation: %v", err)
	}
	if err := st.CreateEscalation(e); err == nil {
		t.Fatal("expected error inserting duplicate escalation ID; got nil")
	}
}

func TestHasApprovedEscalation_NoRecords(t *testing.T) {
	st := escalationStore(t)

	ok, err := st.HasApprovedEscalation("sess-none", "tool-x", argHash(`{"amount":100}`))
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if ok {
		t.Error("expected false with no escalation records; got true")
	}
}

func TestHasApprovedEscalation_PendingNotMatched(t *testing.T) {
	st := escalationStore(t)

	args := `{"amount":15000}`
	if err := st.CreateEscalation(&store.Escalation{
		ID:            "esc-pend",
		SessionID:     "sess-p",
		Seq:           1,
		ToolName:      "high-value-tool",
		ArgumentsJSON: args,
		Status:        "pending",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	// Pending escalations must not be treated as approvals.
	ok, err := st.HasApprovedEscalation("sess-p", "high-value-tool", argHash(args))
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if ok {
		t.Error("expected false for pending escalation; got true")
	}
}

func TestHasApprovedEscalation_ApprovedMatchesHash(t *testing.T) {
	st := escalationStore(t)

	args := `{"amount":15000}`
	if err := st.CreateEscalation(&store.Escalation{
		ID:            "esc-appr",
		SessionID:     "sess-a",
		Seq:           2,
		ToolName:      "wire-tool",
		ArgumentsJSON: args,
		Status:        "approved",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	ok, err := st.HasApprovedEscalation("sess-a", "wire-tool", argHash(args))
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if !ok {
		t.Error("expected true for approved escalation with matching hash; got false")
	}
}

func TestHasApprovedEscalation_HashMismatch(t *testing.T) {
	st := escalationStore(t)

	if err := st.CreateEscalation(&store.Escalation{
		ID:            "esc-diff",
		SessionID:     "sess-b",
		Seq:           1,
		ToolName:      "wire-tool",
		ArgumentsJSON: `{"amount":15000}`,
		Status:        "approved",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	// A different arguments payload should not match the approved record.
	ok, err := st.HasApprovedEscalation("sess-b", "wire-tool", argHash(`{"amount":20000}`))
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if ok {
		t.Error("expected false when argument hash differs from approved record; got true")
	}
}

func TestHasApprovedEscalation_SessionIsolation(t *testing.T) {
	st := escalationStore(t)

	args := `{"amount":15000}`
	// Approval in session-X must not affect session-Y.
	if err := st.CreateEscalation(&store.Escalation{
		ID:            "esc-iso",
		SessionID:     "session-x",
		Seq:           1,
		ToolName:      "wire-tool",
		ArgumentsJSON: args,
		Status:        "approved",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	ok, err := st.HasApprovedEscalation("session-y", "wire-tool", argHash(args))
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if ok {
		t.Error("approval in session-x should not match session-y; got true")
	}
}

func TestHasApprovedEscalation_ToolIsolation(t *testing.T) {
	st := escalationStore(t)

	args := `{"amount":15000}`
	// Approval for tool-a must not affect tool-b.
	if err := st.CreateEscalation(&store.Escalation{
		ID:            "esc-tool-iso",
		SessionID:     "sess-ti",
		Seq:           1,
		ToolName:      "tool-a",
		ArgumentsJSON: args,
		Status:        "approved",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	ok, err := st.HasApprovedEscalation("sess-ti", "tool-b", argHash(args))
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if ok {
		t.Error("approval for tool-a should not match tool-b; got true")
	}
}

func TestHasApprovedEscalation_NullArgumentsMatchEmptyHash(t *testing.T) {
	st := escalationStore(t)

	// An escalation with no arguments (empty ArgumentsJSON → stored as NULL)
	// should match the SHA-256 of an empty byte slice.
	if err := st.CreateEscalation(&store.Escalation{
		ID:            "esc-null-args",
		SessionID:     "sess-null",
		Seq:           1,
		ToolName:      "no-args-tool",
		ArgumentsJSON: "", // stored as NULL
		Status:        "approved",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	// Hash of empty slice matches the NULL record.
	h := sha256.Sum256(nil)
	emptyHash := hex.EncodeToString(h[:])

	ok, err := st.HasApprovedEscalation("sess-null", "no-args-tool", emptyHash)
	if err != nil {
		t.Fatalf("HasApprovedEscalation: %v", err)
	}
	if !ok {
		t.Error("expected true for approved escalation with NULL arguments and empty-hash query; got false")
	}
}

func TestGetEscalationStatus_NotFound(t *testing.T) {
	st := escalationStore(t)

	_, err := st.GetEscalationStatus("ghost-id")
	if err == nil {
		t.Fatal("GetEscalationStatus: expected error for non-existent ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetEscalationStatus: error = %v, want to wrap sql.ErrNoRows", err)
	}
}

func TestGetEscalationStatus_ReturnsStatus(t *testing.T) {
	st := escalationStore(t)

	e := &store.Escalation{
		ID:        "esc-status",
		SessionID: "sess-s",
		Seq:       1,
		ToolName:  "tool-x",
		Status:    "pending",
	}
	if err := st.CreateEscalation(e); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	status, err := st.GetEscalationStatus("esc-status")
	if err != nil {
		t.Fatalf("GetEscalationStatus: %v", err)
	}
	if status != "pending" {
		t.Errorf("GetEscalationStatus = %q, want %q", status, "pending")
	}
}

func TestApproveEscalation_TransitionsToApproved(t *testing.T) {
	st := escalationStore(t)

	if err := st.CreateEscalation(&store.Escalation{
		ID: "esc-approve", SessionID: "sess-a", Seq: 1,
		ToolName: "tool-x", Status: "pending",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	if err := st.ApproveEscalation("esc-approve", "looks good"); err != nil {
		t.Fatalf("ApproveEscalation: %v", err)
	}

	status, err := st.GetEscalationStatus("esc-approve")
	if err != nil {
		t.Fatalf("GetEscalationStatus: %v", err)
	}
	if status != "approved" {
		t.Errorf("status = %q, want %q", status, "approved")
	}
}

func TestRejectEscalation_TransitionsToRejected(t *testing.T) {
	st := escalationStore(t)

	if err := st.CreateEscalation(&store.Escalation{
		ID: "esc-reject", SessionID: "sess-r", Seq: 1,
		ToolName: "tool-x", Status: "pending",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	if err := st.RejectEscalation("esc-reject", "too risky"); err != nil {
		t.Fatalf("RejectEscalation: %v", err)
	}

	status, err := st.GetEscalationStatus("esc-reject")
	if err != nil {
		t.Fatalf("GetEscalationStatus: %v", err)
	}
	if status != "rejected" {
		t.Errorf("status = %q, want %q", status, "rejected")
	}
}

func TestApproveEscalation_NonExistentID(t *testing.T) {
	st := escalationStore(t)

	err := st.ApproveEscalation("no-such-id", "note")
	if err == nil {
		t.Fatal("ApproveEscalation: expected error for non-existent ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ApproveEscalation: error = %v, want to wrap sql.ErrNoRows", err)
	}
}

func TestRejectEscalation_NonExistentID(t *testing.T) {
	st := escalationStore(t)

	err := st.RejectEscalation("no-such-id", "reason")
	if err == nil {
		t.Fatal("RejectEscalation: expected error for non-existent ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RejectEscalation: error = %v, want to wrap sql.ErrNoRows", err)
	}
}

func TestApproveEscalation_AlreadyResolved(t *testing.T) {
	st := escalationStore(t)

	if err := st.CreateEscalation(&store.Escalation{
		ID: "esc-double", SessionID: "sess-d", Seq: 1,
		ToolName: "tool-x", Status: "pending",
	}); err != nil {
		t.Fatalf("CreateEscalation: %v", err)
	}

	if err := st.ApproveEscalation("esc-double", "first"); err != nil {
		t.Fatalf("ApproveEscalation (first): %v", err)
	}
	if err := st.ApproveEscalation("esc-double", "second"); err == nil {
		t.Error("ApproveEscalation (second): expected error for already-approved escalation, got nil")
	}
}

func TestListEscalations_All(t *testing.T) {
	st := escalationStore(t)

	for _, e := range []store.Escalation{
		{ID: "e1", SessionID: "s1", Seq: 1, ToolName: "tool-a", Status: "pending"},
		{ID: "e2", SessionID: "s1", Seq: 2, ToolName: "tool-b", Status: "approved"},
		{ID: "e3", SessionID: "s1", Seq: 3, ToolName: "tool-c", Status: "rejected"},
	} {
		ec := e
		if err := st.CreateEscalation(&ec); err != nil {
			t.Fatalf("CreateEscalation %q: %v", e.ID, err)
		}
	}

	all, err := st.ListEscalations("")
	if err != nil {
		t.Fatalf("ListEscalations: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListEscalations (all) returned %d records, want 3", len(all))
	}
}

func TestListEscalations_FilterByStatus(t *testing.T) {
	st := escalationStore(t)

	for _, e := range []store.Escalation{
		{ID: "f1", SessionID: "s2", Seq: 1, ToolName: "tool-a", Status: "pending"},
		{ID: "f2", SessionID: "s2", Seq: 2, ToolName: "tool-b", Status: "pending"},
		{ID: "f3", SessionID: "s2", Seq: 3, ToolName: "tool-c", Status: "approved"},
	} {
		ec := e
		if err := st.CreateEscalation(&ec); err != nil {
			t.Fatalf("CreateEscalation %q: %v", e.ID, err)
		}
	}

	pending, err := st.ListEscalations("pending")
	if err != nil {
		t.Fatalf("ListEscalations(pending): %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("ListEscalations(pending) returned %d records, want 2", len(pending))
	}

	approved, err := st.ListEscalations("approved")
	if err != nil {
		t.Fatalf("ListEscalations(approved): %v", err)
	}
	if len(approved) != 1 {
		t.Errorf("ListEscalations(approved) returned %d records, want 1", len(approved))
	}
}

func TestListEscalations_Empty(t *testing.T) {
	st := escalationStore(t)

	escs, err := st.ListEscalations("")
	if err != nil {
		t.Fatalf("ListEscalations: %v", err)
	}
	if len(escs) != 0 {
		t.Errorf("ListEscalations on empty store returned %d records, want 0", len(escs))
	}
}
