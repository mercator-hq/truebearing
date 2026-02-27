// Package audit owns AuditRecord construction, Ed25519 signing, verification, and writing
// audit records to the append-only audit_log table.
//
// It does not own the database schema or query methods (see package store).
//
// Invariant: audit_log is strictly append-only. No UPDATE or DELETE may ever touch it.
// Any code path that produces a non-nil AuditRecord must call audit.Write exactly once.
package audit
