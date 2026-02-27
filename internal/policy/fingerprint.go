package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Fingerprint computes a deterministic SHA-256 hash of p, stores the full
// 64-character hex string in p.Fingerprint, and returns it.
//
// The hash is computed over the canonical JSON encoding of the policy struct.
// Go's encoding/json sorts map keys alphabetically and encodes struct fields in
// definition order, producing a deterministic byte sequence for any given
// policy value.
//
// Design: raw YAML bytes are intentionally not used for fingerprinting. Two
// policy files that differ only in whitespace, comments, or YAML key ordering
// are semantically identical and must produce the same fingerprint. This
// property is required for Fix 3 (policy binding at session creation): an
// operator reformatting their policy file must not inadvertently invalidate
// running sessions.
//
// The Fingerprint and SourcePath fields carry json:"-" tags and are excluded
// from the hash. Fingerprint is excluded because it is the hash output itself;
// SourcePath is excluded because it is a local filesystem path that varies
// across machines and must not affect the policy identity.
//
// This function is called by ParseBytes immediately after unmarshalling.
// Callers outside the policy package should not call Fingerprint directly.
func Fingerprint(p *Policy) (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshalling policy for fingerprinting: %w", err)
	}
	sum := sha256.Sum256(data)
	full := fmt.Sprintf("%x", sum[:])
	p.Fingerprint = full
	return full, nil
}
