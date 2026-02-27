package store_test

import (
	"crypto/sha256"
	"encoding/hex"
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
