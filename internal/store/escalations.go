package store

import (
	"crypto/sha256"
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
//
// TODO(5.5): the full escalation state machine (Approve, Reject, GetStatus, List) is
// implemented in Task 5.5. CreateEscalation is added here because it is required by
// the EscalationEvaluator (Task 4.7) and its test harness.
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
