package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store holds the open database connection. It is the only type in this codebase
// that issues SQL queries.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path, sets the required PRAGMAs,
// and applies the schema migration. It returns an initialised Store or an error.
//
// The path may be a file path or a SQLite URI (e.g. "file::memory:?cache=shared"
// for an in-memory database in tests). Open sets PRAGMA journal_mode=WAL,
// foreign_keys=ON, and synchronous=NORMAL on every open. These settings are
// non-optional and are required for correctness and consistency with the store
// invariants documented in CLAUDE.md §7.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %s: %w", path, err)
	}

	// Limit to one open connection. SQLite write locking is connection-scoped;
	// allowing more than one connection in the pool risks "database is locked"
	// errors on concurrent writes even with WAL mode. For the MVP a single
	// connection is correct. Post-MVP: revisit once concurrent write patterns
	// are measured.
	db.SetMaxOpenConns(1)

	// WAL mode permits concurrent reads during writes and improves write throughput.
	// foreign_keys=ON enforces referential integrity declared in the schema.
	// synchronous=NORMAL is safe with WAL (the journal flush protects against
	// corruption) and is faster than FULL.
	pragmas := []struct {
		stmt string
		name string
	}{
		{"PRAGMA journal_mode=WAL", "journal_mode=WAL"},
		{"PRAGMA foreign_keys=ON", "foreign_keys=ON"},
		{"PRAGMA synchronous=NORMAL", "synchronous=NORMAL"},
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p.stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("setting %s: %w", p.name, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("applying schema migration: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
