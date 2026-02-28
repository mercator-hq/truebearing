package store

import "fmt"

// migrate applies the TrueBearing schema to the database using
// CREATE TABLE IF NOT EXISTS, making it safe to call on an existing database.
// All five tables are created in dependency order: sessions before session_events
// (which has a foreign key on sessions.id), then the independent tables.
func (s *Store) migrate() error {
	stmts := []string{
		createSessionsTable,
		createSessionEventsTable,
		createAuditLogTable,
		createAgentsTable,
		createEscalationsTable,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("executing schema statement: %w", err)
		}
	}
	return nil
}

// createSessionsTable holds per-session state: identity, policy binding, taint
// flag, running counters, and lifecycle timestamps.
//
// tainted, tool_call_count, estimated_cost_usd, and terminated use integer/real
// columns because SQLite has no boolean or decimal type.
const createSessionsTable = `
CREATE TABLE IF NOT EXISTS sessions (
    id                  TEXT    PRIMARY KEY,
    agent_name          TEXT    NOT NULL,
    policy_fingerprint  TEXT    NOT NULL,
    tainted             INTEGER NOT NULL DEFAULT 0,
    tool_call_count     INTEGER NOT NULL DEFAULT 0,
    estimated_cost_usd  REAL    NOT NULL DEFAULT 0.0,
    created_at          INTEGER NOT NULL,
    last_seen_at        INTEGER NOT NULL,
    terminated          INTEGER NOT NULL DEFAULT 0
)`

// createSessionEventsTable is the sequence engine's source of truth.
// seq is a monotonically increasing uint64 scoped to session_id and must never
// reuse values within a session. The PRIMARY KEY on (session_id, seq) enforces
// this at the database level.
//
// Design: arguments_json stores raw JSON for the sequence engine to evaluate
// predicates against. This is local-only storage and is never exported. See
// CLAUDE.md §8 security invariant 4.
const createSessionEventsTable = `
CREATE TABLE IF NOT EXISTS session_events (
    seq             INTEGER NOT NULL,
    session_id      TEXT    NOT NULL,
    tool_name       TEXT    NOT NULL,
    arguments_json  TEXT,
    decision        TEXT    NOT NULL,
    policy_rule     TEXT,
    recorded_at     INTEGER NOT NULL,
    PRIMARY KEY (session_id, seq),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
)`

// createAuditLogTable is append-only. No UPDATE or DELETE may ever touch this
// table. Every tool call decision — allow, deny, shadow_deny, or escalate —
// produces exactly one row here, signed with the proxy's Ed25519 private key.
//
// arguments_sha256 stores the SHA-256 hash of the raw arguments JSON, not the
// arguments themselves, to satisfy CLAUDE.md §8 security invariant 4.
//
// client_trace_id stores the W3C traceparent or vendor trace ID extracted from
// the inbound request headers. It is nullable: requests without a trace header
// leave this field NULL (omitted from the signed JSON payload via omitempty).
//
// agent_name is the "agent" JWT claim identifying who made this call.
// decision_reason carries the human-readable policy violation explanation;
// NULL for allow decisions.
const createAuditLogTable = `
CREATE TABLE IF NOT EXISTS audit_log (
    id                  TEXT PRIMARY KEY,
    session_id          TEXT NOT NULL,
    seq                 INTEGER NOT NULL,
    agent_name          TEXT NOT NULL,
    tool_name           TEXT NOT NULL,
    arguments_sha256    TEXT NOT NULL,
    decision            TEXT NOT NULL,
    decision_reason     TEXT,
    policy_fingerprint  TEXT NOT NULL,
    agent_jwt_sha256    TEXT NOT NULL,
    client_trace_id     TEXT,
    recorded_at         INTEGER NOT NULL,
    signature           TEXT NOT NULL
)`

// createAgentsTable stores registered agents: their Ed25519 public key, the
// policy file they were registered with, the tools they are allowed to call,
// and a preview of the issued JWT for operator display.
const createAgentsTable = `
CREATE TABLE IF NOT EXISTS agents (
    name                TEXT PRIMARY KEY,
    public_key_pem      TEXT NOT NULL,
    policy_file         TEXT NOT NULL,
    allowed_tools_json  TEXT NOT NULL,
    registered_at       INTEGER NOT NULL,
    jwt_preview         TEXT NOT NULL
)`

// createEscalationsTable holds the state for pending human-approval escalations.
// The status column transitions from 'pending' to 'approved' or 'rejected'.
// resolved_at is nullable: it is set only when the escalation leaves the
// pending state.
const createEscalationsTable = `
CREATE TABLE IF NOT EXISTS escalations (
    id              TEXT    PRIMARY KEY,
    session_id      TEXT    NOT NULL,
    seq             INTEGER NOT NULL,
    tool_name       TEXT    NOT NULL,
    arguments_json  TEXT,
    status          TEXT    NOT NULL DEFAULT 'pending',
    reason          TEXT,
    created_at      INTEGER NOT NULL,
    resolved_at     INTEGER
)`
