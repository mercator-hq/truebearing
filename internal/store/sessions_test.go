package store_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/mercator-hq/truebearing/internal/store"
)

func TestCreateSession(t *testing.T) {
	db := store.NewTestDB(t)

	if err := db.CreateSession("sess-001", "test-agent", "fp-abc"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Duplicate ID must return an error.
	if err := db.CreateSession("sess-001", "other-agent", "fp-def"); err == nil {
		t.Fatal("CreateSession with duplicate ID: expected error, got nil")
	}
}

func TestGetSession_Found(t *testing.T) {
	db := store.NewTestDB(t)

	if err := db.CreateSession("sess-get", "payments-agent", "fp-123"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := db.GetSession("sess-get")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if sess.ID != "sess-get" {
		t.Errorf("ID: got %q, want %q", sess.ID, "sess-get")
	}
	if sess.AgentName != "payments-agent" {
		t.Errorf("AgentName: got %q, want %q", sess.AgentName, "payments-agent")
	}
	if sess.PolicyFingerprint != "fp-123" {
		t.Errorf("PolicyFingerprint: got %q, want %q", sess.PolicyFingerprint, "fp-123")
	}
	if sess.Tainted {
		t.Error("Tainted: got true, want false for new session")
	}
	if sess.ToolCallCount != 0 {
		t.Errorf("ToolCallCount: got %d, want 0", sess.ToolCallCount)
	}
	if sess.EstimatedCostUSD != 0.0 {
		t.Errorf("EstimatedCostUSD: got %f, want 0.0", sess.EstimatedCostUSD)
	}
	if sess.Terminated {
		t.Error("Terminated: got true, want false for new session")
	}
}

func TestGetSession_NotFound(t *testing.T) {
	db := store.NewTestDB(t)

	_, err := db.GetSession("nonexistent")
	if err == nil {
		t.Fatal("GetSession for missing session: expected error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession error: want sql.ErrNoRows in chain, got %v", err)
	}
}

func TestUpdateSessionTaint(t *testing.T) {
	cases := []struct {
		name       string
		setTainted bool
	}{
		{"set tainted true", true},
		{"set tainted false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := store.NewTestDB(t)
			id := "sess-taint-" + tc.name
			if err := db.CreateSession(id, "agent", "fp"); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}

			if err := db.UpdateSessionTaint(id, tc.setTainted); err != nil {
				t.Fatalf("UpdateSessionTaint: %v", err)
			}

			sess, err := db.GetSession(id)
			if err != nil {
				t.Fatalf("GetSession: %v", err)
			}
			if sess.Tainted != tc.setTainted {
				t.Errorf("Tainted: got %v, want %v", sess.Tainted, tc.setTainted)
			}
		})
	}
}

func TestUpdateSessionTaint_TogglesCorrectly(t *testing.T) {
	db := store.NewTestDB(t)
	if err := db.CreateSession("sess-toggle", "agent", "fp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Taint the session.
	if err := db.UpdateSessionTaint("sess-toggle", true); err != nil {
		t.Fatalf("UpdateSessionTaint(true): %v", err)
	}
	sess, _ := db.GetSession("sess-toggle")
	if !sess.Tainted {
		t.Error("after UpdateSessionTaint(true): Tainted should be true")
	}

	// Clear the taint.
	if err := db.UpdateSessionTaint("sess-toggle", false); err != nil {
		t.Fatalf("UpdateSessionTaint(false): %v", err)
	}
	sess, _ = db.GetSession("sess-toggle")
	if sess.Tainted {
		t.Error("after UpdateSessionTaint(false): Tainted should be false")
	}
}

func TestUpdateSessionTaint_NotFound(t *testing.T) {
	db := store.NewTestDB(t)
	err := db.UpdateSessionTaint("missing", true)
	if err == nil {
		t.Fatal("UpdateSessionTaint for missing session: expected error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateSessionTaint error: want sql.ErrNoRows in chain, got %v", err)
	}
}

func TestIncrementSessionCounters(t *testing.T) {
	db := store.NewTestDB(t)
	if err := db.CreateSession("sess-ctr", "agent", "fp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Increment once with a cost delta.
	if err := db.IncrementSessionCounters("sess-ctr", 0.001); err != nil {
		t.Fatalf("IncrementSessionCounters: %v", err)
	}

	sess, err := db.GetSession("sess-ctr")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ToolCallCount != 1 {
		t.Errorf("ToolCallCount after 1 increment: got %d, want 1", sess.ToolCallCount)
	}
	if sess.EstimatedCostUSD < 0.0009 || sess.EstimatedCostUSD > 0.0011 {
		t.Errorf("EstimatedCostUSD after 0.001 delta: got %f, want ~0.001", sess.EstimatedCostUSD)
	}

	// Increment again with a different cost delta.
	if err := db.IncrementSessionCounters("sess-ctr", 0.005); err != nil {
		t.Fatalf("IncrementSessionCounters (second): %v", err)
	}

	sess, _ = db.GetSession("sess-ctr")
	if sess.ToolCallCount != 2 {
		t.Errorf("ToolCallCount after 2 increments: got %d, want 2", sess.ToolCallCount)
	}
	wantCost := 0.006
	if sess.EstimatedCostUSD < 0.0059 || sess.EstimatedCostUSD > 0.0061 {
		t.Errorf("EstimatedCostUSD after two increments: got %f, want ~%f", sess.EstimatedCostUSD, wantCost)
	}
}

func TestIncrementSessionCounters_ZeroCost(t *testing.T) {
	db := store.NewTestDB(t)
	if err := db.CreateSession("sess-zerocost", "agent", "fp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := db.IncrementSessionCounters("sess-zerocost", 0.0); err != nil {
		t.Fatalf("IncrementSessionCounters with zero cost: %v", err)
	}

	sess, _ := db.GetSession("sess-zerocost")
	if sess.ToolCallCount != 1 {
		t.Errorf("ToolCallCount: got %d, want 1", sess.ToolCallCount)
	}
	if sess.EstimatedCostUSD != 0.0 {
		t.Errorf("EstimatedCostUSD: got %f, want 0.0", sess.EstimatedCostUSD)
	}
}

func TestIncrementSessionCounters_NotFound(t *testing.T) {
	db := store.NewTestDB(t)
	err := db.IncrementSessionCounters("missing", 0.001)
	if err == nil {
		t.Fatal("IncrementSessionCounters for missing session: expected error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("IncrementSessionCounters error: want sql.ErrNoRows in chain, got %v", err)
	}
}

func TestTerminateSession(t *testing.T) {
	db := store.NewTestDB(t)
	if err := db.CreateSession("sess-term", "agent", "fp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := db.TerminateSession("sess-term"); err != nil {
		t.Fatalf("TerminateSession: %v", err)
	}

	sess, err := db.GetSession("sess-term")
	if err != nil {
		t.Fatalf("GetSession after terminate: %v", err)
	}
	if !sess.Terminated {
		t.Error("Terminated: got false, want true after TerminateSession")
	}
}

func TestTerminateSession_NotFound(t *testing.T) {
	db := store.NewTestDB(t)
	err := db.TerminateSession("missing")
	if err == nil {
		t.Fatal("TerminateSession for missing session: expected error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("TerminateSession error: want sql.ErrNoRows in chain, got %v", err)
	}
}

func TestListSessions(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(db *store.Store)
		wantCount    int
		wantAgentIDs []string // expected agent names in result (order: created_at DESC)
	}{
		{
			name:      "empty database returns empty slice",
			setup:     func(_ *store.Store) {},
			wantCount: 0,
		},
		{
			name: "one active session",
			setup: func(db *store.Store) {
				if err := db.CreateSession("ls-s1", "agent-alpha", "fp-1"); err != nil {
					t.Fatalf("CreateSession: %v", err)
				}
			},
			wantCount:    1,
			wantAgentIDs: []string{"agent-alpha"},
		},
		{
			name: "terminated sessions are excluded",
			setup: func(db *store.Store) {
				if err := db.CreateSession("ls-active", "agent-active", "fp-1"); err != nil {
					t.Fatalf("CreateSession active: %v", err)
				}
				if err := db.CreateSession("ls-dead", "agent-dead", "fp-1"); err != nil {
					t.Fatalf("CreateSession dead: %v", err)
				}
				if err := db.TerminateSession("ls-dead"); err != nil {
					t.Fatalf("TerminateSession: %v", err)
				}
			},
			wantCount:    1,
			wantAgentIDs: []string{"agent-active"},
		},
		{
			name: "tainted flag is reflected",
			setup: func(db *store.Store) {
				if err := db.CreateSession("ls-taint", "agent-t", "fp-1"); err != nil {
					t.Fatalf("CreateSession: %v", err)
				}
				if err := db.UpdateSessionTaint("ls-taint", true); err != nil {
					t.Fatalf("UpdateSessionTaint: %v", err)
				}
			},
			wantCount:    1,
			wantAgentIDs: []string{"agent-t"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := store.NewTestDB(t)
			tc.setup(db)

			rows, err := db.ListSessions()
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
			if len(rows) != tc.wantCount {
				t.Fatalf("ListSessions count: got %d, want %d", len(rows), tc.wantCount)
			}
			for i, wantAgent := range tc.wantAgentIDs {
				if rows[i].AgentName != wantAgent {
					t.Errorf("rows[%d].AgentName: got %q, want %q", i, rows[i].AgentName, wantAgent)
				}
			}

			// Verify taint is correctly reflected for the taint test case.
			if tc.name == "tainted flag is reflected" && len(rows) > 0 {
				if !rows[0].Tainted {
					t.Error("Tainted: got false, want true for tainted session")
				}
			}
		})
	}
}

func TestListSessions_TimestampsPopulated(t *testing.T) {
	db := store.NewTestDB(t)
	if err := db.CreateSession("ls-ts", "agent", "fp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rows, err := db.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListSessions count: got %d, want 1", len(rows))
	}
	if rows[0].CreatedAt == 0 {
		t.Error("CreatedAt should be non-zero")
	}
	if rows[0].LastSeenAt == 0 {
		t.Error("LastSeenAt should be non-zero")
	}
}
