package store

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// testDBCounter is incremented atomically for each NewTestDB call to produce
// a unique named in-memory database per test, preventing state leakage between
// parallel tests that share the same process.
var testDBCounter uint64

// NewTestDB creates an isolated in-memory SQLite database for use in tests.
// The schema is applied and cleanup (Close) is registered via t.Cleanup.
//
// Each call produces a distinct named database so that parallel tests cannot
// observe each other's data. The cache=shared URI parameter ensures the
// database remains alive for the lifetime of the Store connection pool rather
// than being torn down after the first connection closes.
func NewTestDB(t *testing.T) *Store {
	t.Helper()

	n := atomic.AddUint64(&testDBCounter, 1)
	dsn := fmt.Sprintf("file:testdb%d?mode=memory&cache=shared", n)

	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("NewTestDB: opening test database %q: %v", dsn, err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("NewTestDB cleanup: closing test database: %v", err)
		}
	})

	return s
}
