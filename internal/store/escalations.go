package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Escalation is a pending or resolved human-review record for a tool call that
// triggered an escalate_when rule. Status transitions: pending → approved | rejected.
//
// Design: ArgumentsJSON stores the raw JSON arguments from the tool call so the
// EscalationEvaluator can match a subsequent call's arguments against the approved
// record by comparing SHA-256 hashes. This is local-only storage and is never exported.
// See CLAUDE.md §8 security invariant 4.
type Escalation struct {
	// ID is the UUID primary key for this escalation record.
	ID string

	// SessionID is the session that generated this escalation.
	SessionID string

	// Seq is the sequence number of the event that triggered the escalation,
	// scoped to SessionID.
	Seq uint64

	// ToolName is the name of the tool whose call triggered the escalation.
	ToolName string

	// ArgumentsJSON is the raw JSON arguments from the triggering call.
	// It may be empty; stored as NULL in the database when empty.
	ArgumentsJSON string

	// Status is the lifecycle state: "pending", "approved", or "rejected".
	Status string

	// Reason is the operator-supplied note recorded when the escalation was
	// approved or rejected. Empty for pending escalations.
	Reason string

	// CreatedAt is the creation timestamp in unix nanoseconds.
	CreatedAt int64

	// ResolvedAt is the resolution timestamp in unix nanoseconds.
	// Zero for pending escalations; stored as NULL in the database.
	ResolvedAt int64
}

// CreateEscalation inserts a new escalation record with status "pending".
// CreatedAt is set to time.Now() when the caller leaves it zero.
func (s *Store) CreateEscalation(e *Escalation) error {
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().UnixNano()
	}

	const query = `
		INSERT INTO escalations
			(id, session_id, seq, tool_name, arguments_json, status, reason, created_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var resolvedAt *int64
	if e.ResolvedAt != 0 {
		v := e.ResolvedAt
		resolvedAt = &v
	}

	if _, err := s.db.Exec(query,
		e.ID, e.SessionID, e.Seq, e.ToolName,
		nullableString(e.ArgumentsJSON),
		e.Status,
		nullableString(e.Reason),
		e.CreatedAt,
		resolvedAt,
	); err != nil {
		return fmt.Errorf("inserting escalation %q: %w", e.ID, err)
	}
	return nil
}

// HasApprovedEscalation reports whether any previously created escalation for
// the given session and tool has been approved with arguments whose SHA-256 hash
// matches argumentsHash. The hash is computed over the raw JSON bytes stored in
// the escalation record (NULL arguments are treated as an empty byte slice).
//
// This is the read path for the EscalationEvaluator: a human approval recorded via
// "truebearing escalation approve" unblocks the next call with the same arguments
// without re-triggering the escalation flow.
func (s *Store) HasApprovedEscalation(sessionID, toolName, argumentsHash string) (bool, error) {
	const query = `
		SELECT arguments_json
		FROM escalations
		WHERE session_id = ? AND tool_name = ? AND status = 'approved'`

	rows, err := s.db.Query(query, sessionID, toolName)
	if err != nil {
		return false, fmt.Errorf("querying approved escalations for session %q tool %q: %w", sessionID, toolName, err)
	}
	defer rows.Close()

	for rows.Next() {
		var argsJSON *string
		if err := rows.Scan(&argsJSON); err != nil {
			return false, fmt.Errorf("scanning approved escalation for session %q tool %q: %w", sessionID, toolName, err)
		}

		// Compute SHA-256 of the stored arguments JSON and compare against the
		// provided hash. A NULL arguments_json is treated as an empty payload so
		// the hash of nil arguments is sha256("").
		var raw []byte
		if argsJSON != nil {
			raw = []byte(*argsJSON)
		}
		h := sha256.Sum256(raw)
		if hex.EncodeToString(h[:]) == argumentsHash {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterating approved escalations for session %q tool %q: %w", sessionID, toolName, err)
	}
	return false, nil
}

// GetEscalationStatus returns the current status ("pending", "approved", or
// "rejected") for the escalation with the given ID. Returns a wrapped
// sql.ErrNoRows if no escalation with that ID exists.
func (s *Store) GetEscalationStatus(id string) (string, error) {
	const query = `SELECT status FROM escalations WHERE id = ?`
	var status string
	if err := s.db.QueryRow(query, id).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("escalation %q not found: %w", id, sql.ErrNoRows)
		}
		return "", fmt.Errorf("looking up escalation %q: %w", id, err)
	}
	return status, nil
}

// ApproveEscalation transitions a pending escalation to "approved" and records
// the operator note. Returns an error if the escalation does not exist or is
// not in "pending" status — approved or rejected escalations cannot be re-resolved.
func (s *Store) ApproveEscalation(id, note string) error {
	return s.resolveEscalation(id, "approved", note)
}

// RejectEscalation transitions a pending escalation to "rejected" and records
// the operator reason. Returns an error if the escalation does not exist or is
// not in "pending" status.
func (s *Store) RejectEscalation(id, reason string) error {
	return s.resolveEscalation(id, "rejected", reason)
}

// resolveEscalation applies a status transition to a pending escalation. It
// updates status, reason, and resolved_at atomically in a single UPDATE that
// enforces the "pending only" guard via the WHERE clause. A zero rows-affected
// result means the escalation either does not exist or has already been resolved.
func (s *Store) resolveEscalation(id, status, note string) error {
	const query = `
		UPDATE escalations
		SET status = ?, reason = ?, resolved_at = ?
		WHERE id = ? AND status = 'pending'`

	res, err := s.db.Exec(query, status, nullableString(note), time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("resolving escalation %q to %q: %w", id, status, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for escalation %q: %w", id, err)
	}
	if n == 0 {
		// Distinguish "not found" from "already resolved" with a targeted lookup.
		current, lookupErr := s.GetEscalationStatus(id)
		if lookupErr != nil {
			// Not found.
			return fmt.Errorf("escalation %q not found: %w", id, sql.ErrNoRows)
		}
		return fmt.Errorf("escalation %q cannot be resolved: current status is %q", id, current)
	}
	return nil
}

// ListEscalations returns all escalation records, ordered by created_at DESC.
// If status is non-empty it is used as an exact filter; pass "" to return all statuses.
func (s *Store) ListEscalations(status string) ([]Escalation, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		const query = `
			SELECT id, session_id, seq, tool_name, arguments_json, status, reason, created_at, resolved_at
			FROM escalations
			ORDER BY created_at DESC`
		rows, err = s.db.Query(query)
	} else {
		const query = `
			SELECT id, session_id, seq, tool_name, arguments_json, status, reason, created_at, resolved_at
			FROM escalations
			WHERE status = ?
			ORDER BY created_at DESC`
		rows, err = s.db.Query(query, status)
	}
	if err != nil {
		return nil, fmt.Errorf("listing escalations (status=%q): %w", status, err)
	}
	defer rows.Close()

	var out []Escalation
	for rows.Next() {
		var e Escalation
		var argsJSON, reason *string
		var resolvedAt *int64
		if err := rows.Scan(
			&e.ID, &e.SessionID, &e.Seq, &e.ToolName,
			&argsJSON, &e.Status, &reason, &e.CreatedAt, &resolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning escalation row: %w", err)
		}
		if argsJSON != nil {
			e.ArgumentsJSON = *argsJSON
		}
		if reason != nil {
			e.Reason = *reason
		}
		if resolvedAt != nil {
			e.ResolvedAt = *resolvedAt
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating escalation rows: %w", err)
	}
	if out == nil {
		out = []Escalation{}
	}
	return out, nil
}
