// Package audit defines the canonical AuditRecord type shared across the
// internal/audit (signing), internal/store (persistence), and cmd/ (CLI) layers.
//
// It does not own signing, verification, database access, or CLI output —
// those concerns live in internal/audit and internal/store respectively.
//
// Invariant: AuditRecord is the single source of truth for field names and
// JSON tags. Adding or removing fields here is the only place that needs
// changing; all three layers pick up the change automatically via type aliases.
package audit
