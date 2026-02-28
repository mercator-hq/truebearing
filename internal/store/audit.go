package store

import "fmt"

// AppendAuditRecord inserts a signed audit log entry into the append-only
// audit_log table. This is the only write path for audit_log — no UPDATE or
// DELETE may ever be issued against this table (CLAUDE.md §7 invariant 1).
//
// clientTraceID and decisionReason may be empty; they are stored as NULL when
// empty to preserve the "not set" semantic in nullable TEXT columns.
//
// Design: parameters are accepted individually rather than as a struct to
// avoid a circular import between internal/store and internal/audit (the audit
// package imports store for *store.Store; store must not import audit).
func (s *Store) AppendAuditRecord(
	id, sessionID string,
	seq uint64,
	agentName, toolName, argumentsSHA256, decision, decisionReason string,
	policyFingerprint, agentJWTSHA256 string,
	clientTraceID string,
	recordedAt int64,
	signature string,
) error {
	const query = `
		INSERT INTO audit_log
			(id, session_id, seq, agent_name, tool_name, arguments_sha256,
			 decision, decision_reason, policy_fingerprint, agent_jwt_sha256,
			 client_trace_id, recorded_at, signature)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.Exec(query,
		id, sessionID, int64(seq),
		agentName, toolName, argumentsSHA256,
		decision, nullableString(decisionReason),
		policyFingerprint, agentJWTSHA256,
		nullableString(clientTraceID),
		recordedAt, signature,
	); err != nil {
		return fmt.Errorf("appending audit record %s: %w", id, err)
	}
	return nil
}
