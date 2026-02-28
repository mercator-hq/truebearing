package escalation

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/mercator-hq/truebearing/internal/store"
)

// Create inserts a new pending escalation for the given session, sequence number,
// tool name, and raw arguments JSON. It returns the generated UUID that the caller
// (the proxy) returns to the agent as the escalation_id for polling via
// check_escalation_status.
//
// Design: UUID generation happens here, not in the store, so the caller owns the ID
// and can embed it in the synthetic JSON-RPC response before the DB write completes.
// The store layer is kept simple: it inserts what it is given.
func Create(sessionID string, seq uint64, toolName, argumentsJSON string, st *store.Store) (string, error) {
	id := uuid.New().String()
	e := &store.Escalation{
		ID:            id,
		SessionID:     sessionID,
		Seq:           seq,
		ToolName:      toolName,
		ArgumentsJSON: argumentsJSON,
		Status:        "pending",
	}
	if err := st.CreateEscalation(e); err != nil {
		return "", fmt.Errorf("creating escalation for session %q tool %q: %w", sessionID, toolName, err)
	}
	return id, nil
}

// Approve transitions the escalation with the given ID from "pending" to "approved"
// and records the operator note. Returns an error if the escalation does not exist
// or has already been resolved.
func Approve(id, note string, st *store.Store) error {
	if err := st.ApproveEscalation(id, note); err != nil {
		return fmt.Errorf("approving escalation %q: %w", id, err)
	}
	return nil
}

// Reject transitions the escalation with the given ID from "pending" to "rejected"
// and records the operator reason. Returns an error if the escalation does not
// exist or has already been resolved.
func Reject(id, reason string, st *store.Store) error {
	if err := st.RejectEscalation(id, reason); err != nil {
		return fmt.Errorf("rejecting escalation %q: %w", id, err)
	}
	return nil
}

// GetStatus returns the current lifecycle status of the escalation: "pending",
// "approved", or "rejected". Returns an error wrapping sql.ErrNoRows if no
// escalation with the given ID exists.
func GetStatus(id string, st *store.Store) (string, error) {
	status, err := st.GetEscalationStatus(id)
	if err != nil {
		return "", fmt.Errorf("getting status for escalation %q: %w", id, err)
	}
	return status, nil
}
