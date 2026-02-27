package store

import (
	"fmt"
	"time"
)

// SessionEvent is a single entry in the per-session ordered event log.
// It is the sequence engine's source of truth for evaluating sequence predicates.
// Seq is 1-based and monotonically increasing within a session; it is assigned by
// AppendEvent and must never be set by callers.
//
// Design: ArgumentsJSON stores raw JSON from the tool call for the sequence engine
// to evaluate predicates against. This is local-only storage that is never exported.
// See CLAUDE.md §8 security invariant 4.
type SessionEvent struct {
	// Seq is the monotonically increasing sequence number, scoped to the session.
	// It is assigned by AppendEvent; callers must leave it zero before calling.
	Seq uint64

	SessionID string

	// ToolName is the name of the tool that was called.
	ToolName string

	// ArgumentsJSON is the raw JSON arguments from the tool call.
	// It may be empty; it is stored as NULL in the database when empty.
	ArgumentsJSON string

	// Decision is the outcome of the evaluation: allow, deny, escalate, or shadow_deny.
	Decision string

	// PolicyRule is the rule that triggered the decision, if any.
	// It is empty for allow decisions and stored as NULL in the database when empty.
	PolicyRule string

	// RecordedAt is the timestamp of the event in unix nanoseconds.
	// AppendEvent sets this to time.Now() if the caller leaves it zero.
	RecordedAt int64
}

// AppendEvent inserts a new session event with the next monotonically increasing seq
// for the session and updates event.Seq to the assigned value. Seq starts at 1 for
// the first event in a session and increments by 1 for each subsequent event.
//
// Design: seq assignment uses an explicit transaction to atomically SELECT MAX(seq)+1
// and INSERT. This guarantees uniqueness and monotonicity even under the single-connection
// constraint imposed by SetMaxOpenConns(1). The SELECT-then-INSERT pattern (rather than
// a single INSERT...SELECT subquery) is used to expose the assigned seq to the caller
// without a second round-trip.
func (s *Store) AppendEvent(event *SessionEvent) error {
	if event.RecordedAt == 0 {
		event.RecordedAt = time.Now().UnixNano()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction for session %q event: %w", event.SessionID, err)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback() }()

	var maxSeq uint64
	row := tx.QueryRow(
		`SELECT COALESCE(MAX(seq), 0) FROM session_events WHERE session_id = ?`,
		event.SessionID,
	)
	if err := row.Scan(&maxSeq); err != nil {
		return fmt.Errorf("computing next seq for session %q: %w", event.SessionID, err)
	}

	nextSeq := maxSeq + 1

	const insertQuery = `
		INSERT INTO session_events
			(seq, session_id, tool_name, arguments_json, decision, policy_rule, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := tx.Exec(insertQuery,
		nextSeq, event.SessionID, event.ToolName,
		nullableString(event.ArgumentsJSON), event.Decision,
		nullableString(event.PolicyRule), event.RecordedAt,
	); err != nil {
		return fmt.Errorf("inserting event for session %q: %w", event.SessionID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing event for session %q: %w", event.SessionID, err)
	}

	event.Seq = nextSeq
	return nil
}

// GetSessionEvents returns all events for the given session ordered by seq ASC.
// Returns an empty slice (not nil) if the session has no events.
//
// Design: ORDER BY seq is performed in SQL rather than in Go. The composite primary
// key index on (session_id, seq) makes this sort effectively free — SQLite reads the
// B-tree in key order. Sorting 1000 events in Go would add unnecessary O(n log n)
// work in the hot path of the sequence evaluator (see mvp-plan.md §8.5).
func (s *Store) GetSessionEvents(sessionID string) ([]SessionEvent, error) {
	const query = `
		SELECT seq, session_id, tool_name, arguments_json, decision, policy_rule, recorded_at
		FROM session_events
		WHERE session_id = ?
		ORDER BY seq ASC`

	rows, err := s.db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying events for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	events := []SessionEvent{}
	for rows.Next() {
		var e SessionEvent
		var argsJSON, policyRule *string
		if err := rows.Scan(
			&e.Seq, &e.SessionID, &e.ToolName,
			&argsJSON, &e.Decision, &policyRule, &e.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning event row for session %q: %w", sessionID, err)
		}
		if argsJSON != nil {
			e.ArgumentsJSON = *argsJSON
		}
		if policyRule != nil {
			e.PolicyRule = *policyRule
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events for session %q: %w", sessionID, err)
	}
	return events, nil
}

// CountSessionEvents returns the total number of events recorded for the given session.
// This is used to detect when a session has reached the max_history policy limit before
// attempting to append another event.
func (s *Store) CountSessionEvents(sessionID string) (int, error) {
	const query = `SELECT COUNT(*) FROM session_events WHERE session_id = ?`
	row := s.db.QueryRow(query, sessionID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("counting events for session %q: %w", sessionID, err)
	}
	return count, nil
}

// nullableString converts an empty string to a nil *string so that empty fields
// are stored as NULL in nullable TEXT columns rather than as empty strings.
// SQLite treats both equally for most queries, but NULL is the canonical
// representation of "not set" and makes schema intent clear.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
