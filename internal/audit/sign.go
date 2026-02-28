package audit

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Sign computes the canonical JSON of record (all fields except Signature),
// signs the payload with privateKey, and stores the base64-encoded 64-byte
// Ed25519 signature in record.Signature. Sign may be called only once per
// record; calling it again overwrites the previous signature.
//
// Canonical JSON is produced by encoding/json over a map[string]any, which
// sorts keys alphabetically. This is deterministic across machines and Go
// versions. See canonicalJSON for the full field list.
func Sign(record *AuditRecord, privateKey ed25519.PrivateKey) error {
	payload, err := canonicalJSON(record)
	if err != nil {
		return fmt.Errorf("computing canonical JSON for record %s: %w", record.ID, err)
	}
	sig := ed25519.Sign(privateKey, payload)
	record.Signature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// Verify reconstructs the canonical JSON of record (all fields except
// Signature), decodes the base64 Signature field, and verifies the Ed25519
// signature against publicKey. Returns a non-nil error if the signature is
// missing, malformed, or does not match.
func Verify(record *AuditRecord, publicKey ed25519.PublicKey) error {
	if record.Signature == "" {
		return fmt.Errorf("record %s has no signature", record.ID)
	}
	payload, err := canonicalJSON(record)
	if err != nil {
		return fmt.Errorf("computing canonical JSON for record %s: %w", record.ID, err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(record.Signature)
	if err != nil {
		return fmt.Errorf("decoding signature for record %s: %w", record.ID, err)
	}
	if !ed25519.Verify(publicKey, payload, sigBytes) {
		return fmt.Errorf("signature verification failed for record %s", record.ID)
	}
	return nil
}

// canonicalJSON produces a deterministic, sorted-key JSON encoding of all
// AuditRecord fields except Signature. encoding/json marshals map[string]any
// with keys in alphabetical order, making the output stable regardless of
// struct field declaration order or map insertion order.
//
// Design: map[string]any is used rather than an auxiliary struct whose fields
// happen to be alphabetically ordered, because the sorted-key guarantee for
// maps is documented behaviour in encoding/json, while the struct field
// ordering in JSON output is implementation-defined and could change. The audit
// package is not in the hot evaluation path, so the allocation cost is
// acceptable.
//
// ClientTraceID and DecisionReason are only included in the map when non-empty
// so that their omission does not change the canonical form for records that
// predate a field being added. This matches the json:"...,omitempty" tags on
// the struct fields.
func canonicalJSON(r *AuditRecord) ([]byte, error) {
	m := map[string]any{
		"agent_jwt_sha256":   r.AgentJWTSHA256,
		"agent_name":         r.AgentName,
		"arguments_sha256":   r.ArgumentsSHA256,
		"decision":           r.Decision,
		"id":                 r.ID,
		"policy_fingerprint": r.PolicyFingerprint,
		"recorded_at":        r.RecordedAt,
		"seq":                r.Seq,
		"session_id":         r.SessionID,
		"tool_name":          r.ToolName,
	}
	if r.ClientTraceID != "" {
		m["client_trace_id"] = r.ClientTraceID
	}
	if r.DecisionReason != "" {
		m["decision_reason"] = r.DecisionReason
	}
	return json.Marshal(m)
}
