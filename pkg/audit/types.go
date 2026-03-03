package audit

// AuditRecord is a single tamper-evident entry in the audit_log table.
// Every tool call decision — allow, deny, shadow_deny, or escalate — produces
// exactly one AuditRecord. The Signature field is an Ed25519 signature over
// the canonical JSON of all other fields, computed by internal/audit.Sign.
//
// This is the canonical struct. internal/audit and internal/store both alias
// this type so that a schema change here is the only edit required — no
// parallel struct updates, no manual field-copy converters.
//
// JSON tags use snake_case throughout so that JSONL output from
// `audit query --format json` can be piped directly into `audit verify`
// without field name translation.
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

	// DelegationChain records the delegation path when a child agent makes a
	// tool call. Format: "parent → child" for one level of delegation. Empty
	// for root agents (no parent). Omitted from the signed JSON payload when
	// empty so that records predating Task 12.2 are not affected.
	DelegationChain string `json:"delegation_chain,omitempty"`

	// RecordedAt is the wall-clock time the proxy produced this record,
	// in Unix nanoseconds.
	RecordedAt int64 `json:"recorded_at"`

	// Signature is the base64-encoded Ed25519 signature over the canonical
	// JSON of all other fields. Set by internal/audit.Sign; verified by
	// internal/audit.Verify.
	Signature string `json:"signature"`
}
