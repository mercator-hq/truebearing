// Package policy owns YAML parsing, type definitions, linting, and fingerprinting for TrueBearing policies.
//
// It does not own evaluation (see package engine) or persistence (see package store).
//
// Invariant: ParseFile and ParseBytes always set the Fingerprint field on the returned Policy.
// Callers must not compute or override the fingerprint themselves.
package policy
