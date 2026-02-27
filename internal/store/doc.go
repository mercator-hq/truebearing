// Package store owns the SQLite schema, migrations, and all database access methods.
//
// It is the only package that issues SQL queries. All other packages call store methods;
// they never call db.Query or db.Exec directly.
//
// Invariants:
//  1. audit_log is append-only — no UPDATE or DELETE ever touches it.
//  2. session_events.seq is monotonically increasing per session and never reused.
//  3. Every Open call sets PRAGMA journal_mode=WAL, foreign_keys=ON, synchronous=NORMAL.
//  4. All queries use parameterised placeholders — no string interpolation in SQL.
package store
