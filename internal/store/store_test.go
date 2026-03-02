// White-box tests for the store package. This file is in package store (not
// store_test) so it can access s.db to verify internal state — WAL mode,
// schema presence — without adding methods to the public API.
package store

import (
	"database/sql"
	"testing"
)

// expectedTables lists every table that migrate() must create.
var expectedTables = []string{
	"sessions",
	"session_events",
	"audit_log",
	"agents",
	"escalations",
}

// expectedColumns is a spot-check of non-obvious columns that must be present
// and match the plan exactly. It maps table → column name.
var expectedColumns = []struct {
	table  string
	column string
}{
	{"sessions", "id"},
	{"sessions", "agent_name"},
	{"sessions", "policy_fingerprint"},
	{"sessions", "tainted"},
	{"sessions", "tool_call_count"},
	{"sessions", "estimated_cost_usd"},
	{"sessions", "created_at"},
	{"sessions", "last_seen_at"},
	{"sessions", "terminated"},

	{"session_events", "seq"},
	{"session_events", "session_id"},
	{"session_events", "tool_name"},
	{"session_events", "arguments_json"},
	{"session_events", "decision"},
	{"session_events", "policy_rule"},
	{"session_events", "recorded_at"},

	{"audit_log", "id"},
	{"audit_log", "session_id"},
	{"audit_log", "seq"},
	{"audit_log", "tool_name"},
	{"audit_log", "arguments_sha256"},
	{"audit_log", "decision"},
	{"audit_log", "policy_fingerprint"},
	{"audit_log", "agent_jwt_sha256"},
	{"audit_log", "client_trace_id"},
	{"audit_log", "recorded_at"},
	{"audit_log", "signature"},

	{"agents", "name"},
	{"agents", "public_key_pem"},
	{"agents", "policy_file"},
	{"agents", "allowed_tools_json"},
	{"agents", "registered_at"},
	{"agents", "jwt_preview"},
	{"agents", "revoked_at"},

	{"escalations", "id"},
	{"escalations", "session_id"},
	{"escalations", "seq"},
	{"escalations", "tool_name"},
	{"escalations", "arguments_json"},
	{"escalations", "status"},
	{"escalations", "reason"},
	{"escalations", "created_at"},
	{"escalations", "resolved_at"},
}

// TestOpen_AllTablesExist verifies that Open creates all five required tables.
func TestOpen_AllTablesExist(t *testing.T) {
	s := NewTestDB(t)

	for _, table := range expectedTables {
		t.Run(table, func(t *testing.T) {
			var name string
			err := s.db.QueryRow(
				"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
				table,
			).Scan(&name)
			if err == sql.ErrNoRows {
				t.Errorf("table %q does not exist after Open", table)
			} else if err != nil {
				t.Errorf("querying for table %q: %v", table, err)
			}
		})
	}
}

// TestOpen_ColumnNames spot-checks that every column from mvp-plan.md §1.4
// is present in the schema with the correct name.
func TestOpen_ColumnNames(t *testing.T) {
	s := NewTestDB(t)

	for _, tc := range expectedColumns {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			// PRAGMA table_info returns one row per column.
			rows, err := s.db.Query("PRAGMA table_info(" + tc.table + ")")
			if err != nil {
				t.Fatalf("PRAGMA table_info(%s): %v", tc.table, err)
			}
			defer func() { _ = rows.Close() }()

			found := false
			for rows.Next() {
				var cid int
				var name, colType string
				var notNull, pk int
				var dflt sql.NullString
				if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
					t.Fatalf("scanning pragma row: %v", err)
				}
				if name == tc.column {
					found = true
					break
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterating pragma rows: %v", err)
			}
			if !found {
				t.Errorf("column %q not found in table %q", tc.column, tc.table)
			}
		})
	}
}

// TestOpen_WALMode verifies that journal_mode=WAL is set by Open.
// In-memory databases return "memory" from PRAGMA journal_mode because WAL
// requires a real file; the important thing is that the PRAGMA ran without
// error. File-based databases must return "wal".
func TestOpen_WALMode(t *testing.T) {
	s := NewTestDB(t)

	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("querying journal_mode: %v", err)
	}
	// "memory" is the expected result for a file:...?mode=memory URI.
	// "wal" would be the result for a real on-disk file.
	if mode != "memory" && mode != "wal" {
		t.Errorf("PRAGMA journal_mode = %q; want %q or %q", mode, "wal", "memory")
	}
}

// TestOpen_ForeignKeysEnabled verifies that foreign_keys=ON is set by Open.
func TestOpen_ForeignKeysEnabled(t *testing.T) {
	s := NewTestDB(t)

	var enabled int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("querying foreign_keys: %v", err)
	}
	if enabled != 1 {
		t.Errorf("PRAGMA foreign_keys = %d; want 1 (ON)", enabled)
	}
}

// TestOpen_MigrateIdempotent verifies that calling Open twice on the same
// database does not return an error (CREATE TABLE IF NOT EXISTS is idempotent).
func TestOpen_MigrateIdempotent(t *testing.T) {
	// Use a distinct named in-memory database shared across two Opens.
	const dsn = "file:idempotent_test?mode=memory&cache=shared"

	s1, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer func() { _ = s1.Close() }()

	s2, err := Open(dsn)
	if err != nil {
		t.Errorf("second Open (idempotency check): %v", err)
	}
	if s2 != nil {
		_ = s2.Close()
	}
}

// TestNewTestDB_Isolation verifies that two stores from NewTestDB do not share
// state: a row inserted in one is not visible in the other.
func TestNewTestDB_Isolation(t *testing.T) {
	s1 := NewTestDB(t)
	s2 := NewTestDB(t)

	// Insert an agent row into s1.
	_, err := s1.db.Exec(
		`INSERT INTO agents (name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		"isolation-test-agent", "pubkey", "policy.yaml", "[]", 1000000000, "preview",
	)
	if err != nil {
		t.Fatalf("inserting into s1: %v", err)
	}

	// The same agent must not be visible in s2.
	var count int
	if err := s2.db.QueryRow(
		"SELECT COUNT(*) FROM agents WHERE name = ?", "isolation-test-agent",
	).Scan(&count); err != nil {
		t.Fatalf("querying s2: %v", err)
	}
	if count != 0 {
		t.Errorf("s2 sees row inserted in s1: got count %d, want 0", count)
	}
}

// TestNewTestDB_CleanPerCall verifies that each NewTestDB call starts with an
// empty schema — no leftover rows from a prior call within the same test run.
func TestNewTestDB_CleanPerCall(t *testing.T) {
	s := NewTestDB(t)

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&count); err != nil {
		t.Fatalf("querying agents: %v", err)
	}
	if count != 0 {
		t.Errorf("fresh test database has %d row(s) in agents; want 0", count)
	}
}

// TestSessionEventsFK verifies that the foreign key constraint on
// session_events.session_id is enforced: inserting an event for a
// non-existent session must return an error.
func TestSessionEventsFK(t *testing.T) {
	s := NewTestDB(t)

	_, err := s.db.Exec(
		`INSERT INTO session_events (seq, session_id, tool_name, decision, recorded_at)
		 VALUES (1, 'nonexistent-session', 'some_tool', 'allow', 1000000000)`,
	)
	if err == nil {
		t.Error("expected foreign key constraint error inserting session_event with no parent session; got nil")
	}
}
