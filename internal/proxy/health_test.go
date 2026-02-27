package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// parseHealthPolicy parses minimalTestPolicyYAML with the given sourcePath.
// sourcePath controls whether handleHealth will attempt an os.Stat check:
// an empty string skips the file check; a non-empty path is stat'd.
func parseHealthPolicy(t *testing.T, sourcePath string) *policy.Policy {
	t.Helper()
	pol, err := policy.ParseBytes([]byte(minimalTestPolicyYAML), sourcePath)
	if err != nil {
		t.Fatalf("parseHealthPolicy: %v", err)
	}
	return pol
}

// newHealthServer builds a Proxy backed by st and pol and returns a running
// httptest.Server. dbPath is passed through to New() for health response display.
func newHealthServer(t *testing.T, st *store.Store, pol *policy.Policy, dbPath string) *httptest.Server {
	t.Helper()
	upstream, err := url.Parse("http://localhost:9999")
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}
	p := New(upstream, st, pol, dbPath)
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// TestHealth_Healthy verifies that GET /health returns 200 with a correct JSON
// body when the database is reachable and the policy SourcePath is empty
// (which skips the file-accessibility check).
func TestHealth_Healthy(t *testing.T) {
	pol := parseHealthPolicy(t, "") // empty SourcePath → no file check
	st := store.NewTestDB(t)
	srv := newHealthServer(t, st, pol, "test-proxy.db")

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}

	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status: want ok, got %q", body.Status)
	}
	if body.ProxyVersion != proxyVersion {
		t.Errorf("proxy_version: want %q, got %q", proxyVersion, body.ProxyVersion)
	}
	if body.DBPath != "test-proxy.db" {
		t.Errorf("db_path: want test-proxy.db, got %q", body.DBPath)
	}
}

// TestHealth_NoJWTRequired verifies that GET /health is reachable without an
// Authorization header. The auth middleware must not apply to this route.
func TestHealth_NoJWTRequired(t *testing.T) {
	pol := parseHealthPolicy(t, "")
	st := store.NewTestDB(t)
	srv := newHealthServer(t, st, pol, "")

	// Send the request with no Authorization header at all.
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("GET /health must not require a JWT; got 401")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: want 200, got %d", resp.StatusCode)
	}
}

// TestHealth_Degraded_DBUnreachable verifies that GET /health returns 503 when
// the database connection is no longer alive.
func TestHealth_Degraded_DBUnreachable(t *testing.T) {
	pol := parseHealthPolicy(t, "")

	// Open a store directly — not via NewTestDB — so we can close it without
	// conflicting with NewTestDB's t.Cleanup, which calls t.Errorf on Close error.
	st, err := store.Open("file:proxyhealthdbdegraded?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	// Closing the store makes Ping() return an error, simulating a dead connection.
	if err := st.Close(); err != nil {
		t.Fatalf("closing store: %v", err)
	}

	upstream, _ := url.Parse("http://localhost:9999")
	p := New(upstream, st, pol, "")
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", resp.StatusCode)
	}

	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status: want degraded, got %q", body.Status)
	}
	if !strings.Contains(body.Reason, "database") {
		t.Errorf("reason must mention database, got %q", body.Reason)
	}
}

// TestHealth_Degraded_PolicyFileUnreadable verifies that GET /health returns 503
// when the policy SourcePath points to a file that no longer exists on disk.
func TestHealth_Degraded_PolicyFileUnreadable(t *testing.T) {
	// A non-existent path causes os.Stat to fail inside handleHealth.
	pol := parseHealthPolicy(t, "/nonexistent/path/policy.yaml")
	st := store.NewTestDB(t)
	srv := newHealthServer(t, st, pol, "")

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", resp.StatusCode)
	}

	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status: want degraded, got %q", body.Status)
	}
	if !strings.Contains(body.Reason, "policy") {
		t.Errorf("reason must mention policy, got %q", body.Reason)
	}
}
