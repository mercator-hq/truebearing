package audit

import pkgaudit "github.com/mercator-hq/truebearing/pkg/audit"

// AuditRecord is a type alias for pkg/audit.AuditRecord. It is the single
// tamper-evident entry type used throughout the signing, verification, and
// write paths in this package.
//
// The canonical field definitions and JSON tags live in pkg/audit.AuditRecord.
// This alias exists so that callers in internal/ and cmd/ can reference
// audit.AuditRecord without importing pkg/audit directly.
//
// Invariant: Signature must be non-empty before Write is called. Call Sign
// first, then Write. A record with an empty Signature will fail verification.
type AuditRecord = pkgaudit.AuditRecord
