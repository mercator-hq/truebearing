// Package identity owns Ed25519 keypair generation and storage, and JWT minting and validation.
//
// It does not own session state or database access (see package store).
//
// Invariant: ValidateAgentJWT takes an explicit publicKey argument — key lookup from the
// database is the caller's responsibility. This keeps validation purely testable.
package identity
