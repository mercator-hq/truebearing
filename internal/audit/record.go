package audit

// AuditRecord is a single tamper-evident entry in the audit_log table.
// Every tool call decision — allow, deny, shadow_deny, or escalate — produces
// exactly one AuditRecord. The Signature field is an Ed25519 signature over
// the canonical JSON of all other fields, computed by Sign.
//
// The struct maps directly to the audit_log schema, with two additions over
// the base schema columns: AgentName and DecisionReason, which carry operator-
// visible context that is both persisted and included in the signed payload.
//
// Invariant: Signature must be non-empty before Write is called. Call Sign
// first, then Write. A record with an empty Signature will fail verification.
type AuditRecord struct {
	// ID is a UUID v4 that uniquely identifies this record across all sessions.
	ID string `json:"id"`

	// SessionID is the X-TrueBearing-Session-ID the tool call arrived with.
	SessionID string `json:"session_id"`

	// Seq is the session-scoped monotonically increasing sequence number
	// copied from the corresponding session_events row.
	Seq uint64 `json:"seq"`

	// AgentName is the "agent" claim from the validated JWT that made this call.
	AgentName string `json:"agent_name"`

	// ToolName is the "name" field from the MCP tools/call params.
	ToolName string `json:"tool_name"`

	// ArgumentsSHA256 is the hex-encoded SHA-256 of the raw arguments JSON.
	// The raw arguments are never stored here — only the hash.
	// See CLAUDE.md §8 security invariant 4.
	ArgumentsSHA256 string `json:"arguments_sha256"`

	// Decision is the enforcement outcome: allow, deny, shadow_deny, or escalate.
	Decision string `json:"decision"`

	// DecisionReason is the human-readable policy violation explanation.
	// Empty for allow decisions.
	DecisionReason string `json:"decision_reason,omitempty"`

	// PolicyFingerprint is the SHA-256 fingerprint of the policy that was
	// active when this decision was made. It binds the audit record to the
	// exact policy version evaluated.
	PolicyFingerprint string `json:"policy_fingerprint"`

	// AgentJWTSHA256 is the hex-encoded SHA-256 of the raw Bearer token
	// presented on this request. It allows auditors to correlate records
	// with a specific credential issuance without exposing the JWT itself.
	AgentJWTSHA256 string `json:"agent_jwt_sha256"`

	// ClientTraceID is the W3C traceparent or vendor trace ID extracted from
	// the inbound request headers (e.g. traceparent, x-datadog-trace-id).
	// It is omitted from the JSON payload when empty.
	// See mvp-plan.md §9.1a for the full header priority list.
	ClientTraceID string `json:"client_trace_id,omitempty"`

	// RecordedAt is the wall-clock time the proxy produced this record,
	// in Unix nanoseconds.
	RecordedAt int64 `json:"recorded_at"`

	// Signature is the base64-encoded Ed25519 signature over the canonical
	// JSON of all other fields. Set by Sign; verified by Verify.
	Signature string `json:"signature"`
}
