package store

import (
	"fmt"
	"time"
)

// AuditRecord is a single row read from the audit_log table. It mirrors
// audit.AuditRecord but lives in the store package to avoid a circular import:
// internal/audit imports store for Write, so store must not import audit for
// querying. Callers in cmd/ may convert to audit.AuditRecord as needed.
//
// JSON tags use snake_case so that `audit query --format json` emits the same
// field names that `audit verify` expects when it unmarshals into
// internal/audit.AuditRecord. Without matching tags, verify would see empty
// fields and report every record as TAMPERED.
type AuditRecord struct {
	// ID is the UUID v4 primary key.
	ID string `json:"id"`

	// SessionID is the session the tool call was made in.
	SessionID string `json:"session_id"`

	// Seq is the session-scoped monotonically increasing sequence number.
	Seq uint64 `json:"seq"`

	// AgentName is the "agent" JWT claim identifying the caller.
	AgentName string `json:"agent_name"`

	// ToolName is the name of the tool that was called.
	ToolName string `json:"tool_name"`

	// ArgumentsSHA256 is the hex-encoded SHA-256 of the raw arguments JSON.
	ArgumentsSHA256 string `json:"arguments_sha256"`

	// Decision is the enforcement outcome: allow, deny, shadow_deny, or escalate.
	Decision string `json:"decision"`

	// DecisionReason is the human-readable policy violation explanation.
	// Empty for allow decisions (stored as NULL). Omitted from JSON when empty
	// to match the omitempty behaviour on internal/audit.AuditRecord.
	DecisionReason string `json:"decision_reason,omitempty"`

	// PolicyFingerprint is the fingerprint of the policy active at decision time.
	PolicyFingerprint string `json:"policy_fingerprint"`

	// AgentJWTSHA256 is the hex-encoded SHA-256 of the Bearer token on the request.
	AgentJWTSHA256 string `json:"agent_jwt_sha256"`

	// ClientTraceID is the W3C traceparent or vendor trace ID from the inbound headers.
	// Empty when no trace header was present (stored as NULL). Omitted from JSON
	// when empty to match the omitempty behaviour on internal/audit.AuditRecord.
	ClientTraceID string `json:"client_trace_id,omitempty"`

	// RecordedAt is the wall-clock time the proxy produced this record, in Unix nanoseconds.
	RecordedAt int64 `json:"recorded_at"`

	// Signature is the base64-encoded Ed25519 signature over the canonical JSON
	// of all other fields.
	Signature string `json:"signature"`
}

// AuditFilter specifies optional query constraints for QueryAuditLog. All fields
// are optional; a zero AuditFilter returns all records. Multiple non-zero fields
// are combined with AND.
type AuditFilter struct {
	// SessionID filters by exact match on session_id. Empty = no filter.
	SessionID string

	// ToolName filters by exact match on tool_name. Empty = no filter.
	ToolName string

	// Decision filters by exact match on decision (allow, deny, shadow_deny, escalate).
	// Empty = no filter.
	Decision string

	// TraceID filters by exact match on client_trace_id. Empty = no filter.
	TraceID string

	// From restricts results to records with recorded_at >= From.UnixNano().
	// Zero value = no lower bound.
	From time.Time

	// To restricts results to records with recorded_at <= To.UnixNano().
	// Zero value = no upper bound.
	To time.Time
}

// QueryAuditLog returns audit records matching the given filters, ordered by
// recorded_at ASC. A zero AuditFilter returns all records. Multiple non-zero
// fields are combined with AND.
//
// Design: the WHERE clause is built dynamically by appending static SQL
// fragments alongside a parallel slice of bind parameters. All user-supplied
// values are bound via parameterised placeholders (?) — no string interpolation
// of values occurs — which prevents SQL injection.
func (s *Store) QueryAuditLog(filters AuditFilter) ([]AuditRecord, error) {
	q := `SELECT id, session_id, seq, agent_name, tool_name, arguments_sha256,
	             decision, decision_reason, policy_fingerprint, agent_jwt_sha256,
	             client_trace_id, recorded_at, signature
	      FROM audit_log
	      WHERE 1=1`
	var args []interface{}

	if filters.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, filters.SessionID)
	}
	if filters.ToolName != "" {
		q += ` AND tool_name = ?`
		args = append(args, filters.ToolName)
	}
	if filters.Decision != "" {
		q += ` AND decision = ?`
		args = append(args, filters.Decision)
	}
	if filters.TraceID != "" {
		q += ` AND client_trace_id = ?`
		args = append(args, filters.TraceID)
	}
	if !filters.From.IsZero() {
		q += ` AND recorded_at >= ?`
		args = append(args, filters.From.UnixNano())
	}
	if !filters.To.IsZero() {
		q += ` AND recorded_at <= ?`
		args = append(args, filters.To.UnixNano())
	}

	q += ` ORDER BY recorded_at ASC`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying audit log: %w", err)
	}
	defer rows.Close()

	records := []AuditRecord{}
	for rows.Next() {
		var r AuditRecord
		var decisionReason, clientTraceID *string
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.Seq, &r.AgentName, &r.ToolName, &r.ArgumentsSHA256,
			&r.Decision, &decisionReason, &r.PolicyFingerprint, &r.AgentJWTSHA256,
			&clientTraceID, &r.RecordedAt, &r.Signature,
		); err != nil {
			return nil, fmt.Errorf("scanning audit log record: %w", err)
		}
		if decisionReason != nil {
			r.DecisionReason = *decisionReason
		}
		if clientTraceID != nil {
			r.ClientTraceID = *clientTraceID
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating audit log query results: %w", err)
	}
	return records, nil
}

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
