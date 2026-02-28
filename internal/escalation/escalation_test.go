package escalation_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/mercator-hq/truebearing/internal/escalation"
	"github.com/mercator-hq/truebearing/internal/store"
)

// TestCreate_PendingStatus verifies that a newly created escalation has status "pending".
func TestCreate_PendingStatus(t *testing.T) {
	st := store.NewTestDB(t)

	id, err := escalation.Create("sess-1", 1, "execute_wire_transfer", `{"amount_usd":15000}`, st)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create: returned empty ID")
	}

	status, err := escalation.GetStatus(id, st)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != "pending" {
		t.Errorf("GetStatus = %q, want %q", status, "pending")
	}
}

// TestApprove_TransitionsStatus verifies that Approve moves the escalation to "approved".
func TestApprove_TransitionsStatus(t *testing.T) {
	st := store.NewTestDB(t)

	id, err := escalation.Create("sess-1", 1, "execute_wire_transfer", `{"amount_usd":15000}`, st)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := escalation.Approve(id, "CFO approved directly", st); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	status, err := escalation.GetStatus(id, st)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != "approved" {
		t.Errorf("GetStatus = %q, want %q", status, "approved")
	}
}

// TestReject_TransitionsStatus verifies that Reject moves the escalation to "rejected".
func TestReject_TransitionsStatus(t *testing.T) {
	st := store.NewTestDB(t)

	id, err := escalation.Create("sess-1", 2, "execute_wire_transfer", `{"amount_usd":50000}`, st)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := escalation.Reject(id, "Amount exceeds daily limit", st); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	status, err := escalation.GetStatus(id, st)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != "rejected" {
		t.Errorf("GetStatus = %q, want %q", status, "rejected")
	}
}

// TestApprove_NonExistentID verifies that approving a non-existent escalation returns an error
// wrapping sql.ErrNoRows.
func TestApprove_NonExistentID(t *testing.T) {
	st := store.NewTestDB(t)

	err := escalation.Approve("does-not-exist", "note", st)
	if err == nil {
		t.Fatal("Approve: expected error for non-existent ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Approve: error = %v, want to wrap sql.ErrNoRows", err)
	}
}

// TestReject_NonExistentID verifies that rejecting a non-existent escalation returns an error
// wrapping sql.ErrNoRows.
func TestReject_NonExistentID(t *testing.T) {
	st := store.NewTestDB(t)

	err := escalation.Reject("does-not-exist", "reason", st)
	if err == nil {
		t.Fatal("Reject: expected error for non-existent ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Reject: error = %v, want to wrap sql.ErrNoRows", err)
	}
}

// TestApprove_AlreadyApproved verifies that approving an already-approved escalation
// returns an error rather than silently succeeding.
func TestApprove_AlreadyApproved(t *testing.T) {
	st := store.NewTestDB(t)

	id, err := escalation.Create("sess-1", 1, "execute_wire_transfer", `{}`, st)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := escalation.Approve(id, "first approval", st); err != nil {
		t.Fatalf("Approve (first): %v", err)
	}

	// Second approval attempt must fail.
	if err := escalation.Approve(id, "second approval", st); err == nil {
		t.Error("Approve (second): expected error, got nil")
	}
}

// TestReject_AlreadyRejected verifies that rejecting an already-rejected escalation
// returns an error.
func TestReject_AlreadyRejected(t *testing.T) {
	st := store.NewTestDB(t)

	id, err := escalation.Create("sess-1", 1, "execute_wire_transfer", `{}`, st)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := escalation.Reject(id, "first rejection", st); err != nil {
		t.Fatalf("Reject (first): %v", err)
	}

	// Second rejection attempt must fail.
	if err := escalation.Reject(id, "second rejection", st); err == nil {
		t.Error("Reject (second): expected error, got nil")
	}
}

// TestGetStatus_NonExistentID verifies that querying status for an unknown ID
// returns an error wrapping sql.ErrNoRows.
func TestGetStatus_NonExistentID(t *testing.T) {
	st := store.NewTestDB(t)

	_, err := escalation.GetStatus("ghost-id", st)
	if err == nil {
		t.Fatal("GetStatus: expected error for non-existent ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetStatus: error = %v, want to wrap sql.ErrNoRows", err)
	}
}
