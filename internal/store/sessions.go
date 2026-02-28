package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/mercator-hq/truebearing/internal/session"
)

// SessionRow is the full database representation of a session, including
// timestamps that are not needed by the evaluation pipeline but are required
// for CLI display commands such as `session list`. It is distinct from
// session.Session, which is the leaner evaluation-pipeline struct.
type SessionRow struct {
	// ID is the unique session identifier.
	ID string

	// AgentName is the registered name of the agent that owns this session.
	AgentName string

	// PolicyFingerprint is the SHA-256 fingerprint of the policy bound at
	// session creation time.
	PolicyFingerprint string

	// Tainted is true if the session has been tainted and not yet cleared.
	Tainted bool

	// ToolCallCount is the total number of tool calls recorded.
	ToolCallCount int

	// EstimatedCostUSD is the accumulated estimated cost in US dollars.
	EstimatedCostUSD float64

	// Terminated is true if the session was hard-terminated by an operator.
	Terminated bool

	// CreatedAt is the session creation timestamp in unix nanoseconds.
	CreatedAt int64

	// LastSeenAt is the timestamp of the most recent activity in unix nanoseconds.
	LastSeenAt int64
}

// CreateSession inserts a new session row with the given ID, agent name, and policy
// fingerprint. created_at and last_seen_at are set to now. Returns an error if a
// session with the same ID already exists.
func (s *Store) CreateSession(id, agentName, policyFingerprint string) error {
	const query = `
		INSERT INTO sessions
			(id, agent_name, policy_fingerprint, tainted, tool_call_count, estimated_cost_usd, created_at, last_seen_at, terminated)
		VALUES (?, ?, ?, 0, 0, 0.0, ?, ?, 0)`
	now := time.Now().UnixNano()
	if _, err := s.db.Exec(query, id, agentName, policyFingerprint, now, now); err != nil {
		return fmt.Errorf("creating session %q: %w", id, err)
	}
	return nil
}

// GetSession retrieves the session with the given ID from the database.
// Returns a wrapped sql.ErrNoRows if no session with that ID exists.
func (s *Store) GetSession(id string) (*session.Session, error) {
	const query = `
		SELECT id, agent_name, policy_fingerprint, tainted, tool_call_count, estimated_cost_usd, terminated
		FROM sessions
		WHERE id = ?`
	row := s.db.QueryRow(query, id)
	sess := new(session.Session)
	var tainted, terminated int
	if err := row.Scan(
		&sess.ID, &sess.AgentName, &sess.PolicyFingerprint,
		&tainted, &sess.ToolCallCount, &sess.EstimatedCostUSD, &terminated,
	); err != nil {
		return nil, fmt.Errorf("looking up session %q: %w", id, err)
	}
	sess.Tainted = tainted != 0
	sess.Terminated = terminated != 0
	return sess, nil
}

// UpdateSessionTaint sets the tainted flag for the given session and refreshes
// last_seen_at. Returns a wrapped sql.ErrNoRows if no session with the given ID exists.
func (s *Store) UpdateSessionTaint(id string, tainted bool) error {
	const query = `UPDATE sessions SET tainted = ?, last_seen_at = ? WHERE id = ?`
	taintedInt := 0
	if tainted {
		taintedInt = 1
	}
	res, err := s.db.Exec(query, taintedInt, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("updating taint for session %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("checking rows affected for session %q taint update: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("updating taint for session %q: %w", id, sql.ErrNoRows)
	}
	return nil
}

// IncrementSessionCounters atomically increments tool_call_count by 1 and adds costDelta
// to estimated_cost_usd for the given session, and refreshes last_seen_at.
// Returns a wrapped sql.ErrNoRows if no session with the given ID exists.
//
// Design: a single UPDATE statement performs both increments atomically. SQLite
// evaluates the right-hand side expressions before writing, so
// `tool_call_count + 1` reads the current value and writes it incremented in one
// step — no separate read required. This is safe without an explicit transaction
// because SetMaxOpenConns(1) serialises all writes through a single connection.
func (s *Store) IncrementSessionCounters(id string, costDelta float64) error {
	const query = `
		UPDATE sessions
		SET tool_call_count    = tool_call_count + 1,
		    estimated_cost_usd = estimated_cost_usd + ?,
		    last_seen_at       = ?
		WHERE id = ?`
	res, err := s.db.Exec(query, costDelta, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("incrementing counters for session %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("checking rows affected for session %q counter increment: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("incrementing counters for session %q: %w", id, sql.ErrNoRows)
	}
	return nil
}

// ListSessions returns all non-terminated sessions ordered by created_at DESC
// (most recent first). It returns a full SessionRow for each session, including
// timestamps needed by the `session list` CLI command.
//
// Design: terminated sessions are excluded here because the CLI command is a
// monitoring view of active sessions. Operators who need to see terminated
// sessions can query the database directly or filter via a future --all flag.
func (s *Store) ListSessions() ([]SessionRow, error) {
	const query = `
		SELECT id, agent_name, policy_fingerprint, tainted, tool_call_count, estimated_cost_usd, terminated, created_at, last_seen_at
		FROM sessions
		WHERE terminated = 0
		ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRow
	for rows.Next() {
		var r SessionRow
		var tainted, terminated int
		if err := rows.Scan(
			&r.ID, &r.AgentName, &r.PolicyFingerprint,
			&tainted, &r.ToolCallCount, &r.EstimatedCostUSD,
			&terminated, &r.CreatedAt, &r.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		r.Tainted = tainted != 0
		r.Terminated = terminated != 0
		sessions = append(sessions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session rows: %w", err)
	}
	if sessions == nil {
		sessions = []SessionRow{}
	}
	return sessions, nil
}

// TerminateSession marks the given session as hard-terminated and refreshes last_seen_at.
// Subsequent tool calls on a terminated session are rejected by the proxy with 410 Gone.
// Returns a wrapped sql.ErrNoRows if no session with the given ID exists.
func (s *Store) TerminateSession(id string) error {
	const query = `UPDATE sessions SET terminated = 1, last_seen_at = ? WHERE id = ?`
	res, err := s.db.Exec(query, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("terminating session %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("checking rows affected for session %q termination: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("terminating session %q: %w", id, sql.ErrNoRows)
	}
	return nil
}
