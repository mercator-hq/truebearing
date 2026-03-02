package audit

import (
	"fmt"

	"github.com/mercator-hq/truebearing/internal/store"
)

// Write inserts record into the append-only audit_log table via st.
//
// Sign must be called on record before Write. Write does not sign the record
// and does not verify the signature — it persists whatever Signature is set.
// Callers are responsible for the sign-then-write ordering.
//
// Write returns an error if the database insert fails. Per the audit_log
// append-only invariant (CLAUDE.md §7), Write never issues UPDATE or DELETE.
func Write(record *AuditRecord, st *store.Store) error {
	if err := st.AppendAuditRecord(
		record.ID,
		record.SessionID,
		record.Seq,
		record.AgentName,
		record.ToolName,
		record.ArgumentsSHA256,
		record.Decision,
		record.DecisionReason,
		record.PolicyFingerprint,
		record.AgentJWTSHA256,
		record.ClientTraceID,
		record.DelegationChain,
		record.RecordedAt,
		record.Signature,
	); err != nil {
		return fmt.Errorf("writing audit record %s: %w", record.ID, err)
	}
	return nil
}
