package audit_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/audit"
	"github.com/mercator-hq/truebearing/internal/store"
)

// newTestKeypair generates an Ed25519 keypair for use in tests. It fails
// the test immediately on any error.
func newTestKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}
	return pub, priv
}

// sampleRecord returns a populated AuditRecord suitable for signing tests.
// Signature is intentionally left empty — callers invoke Sign before Verify.
func sampleRecord() *audit.AuditRecord {
	return &audit.AuditRecord{
		ID:                "rec-0001",
		SessionID:         "sess-abc",
		Seq:               3,
		AgentName:         "payments-agent",
		ToolName:          "execute_wire_transfer",
		ArgumentsSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Decision:          "deny",
		DecisionReason:    "sequence.only_after: manager_approval not satisfied",
		PolicyFingerprint: "a8f9c2deadbeef",
		AgentJWTSHA256:    "f4d2000000000000000000000000000000000000000000000000000000000000",
		RecordedAt:        time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC).UnixNano(),
	}
}

// TestSignVerifyRoundTrip verifies that a signed record passes verification
// with the matching public key.
func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := newTestKeypair(t)
	rec := sampleRecord()

	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if rec.Signature == "" {
		t.Fatal("Sign: Signature is empty after signing")
	}
	if err := audit.Verify(rec, pub); err != nil {
		t.Errorf("Verify: unexpected error after valid sign: %v", err)
	}
}

// TestVerifyTamperedDecision verifies that flipping a byte in the Decision
// field causes Verify to return an error. This is the primary tamper-evidence
// guarantee.
func TestVerifyTamperedDecision(t *testing.T) {
	pub, priv := newTestKeypair(t)
	rec := sampleRecord()

	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Tamper: change the decision field after signing.
	rec.Decision = "allow"

	if err := audit.Verify(rec, pub); err == nil {
		t.Error("Verify: expected error for tampered Decision, got nil")
	}
}

// TestVerifyTamperedToolName verifies that changing the ToolName field after
// signing causes Verify to fail.
func TestVerifyTamperedToolName(t *testing.T) {
	pub, priv := newTestKeypair(t)
	rec := sampleRecord()

	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec.ToolName = "read_invoice"

	if err := audit.Verify(rec, pub); err == nil {
		t.Error("Verify: expected error for tampered ToolName, got nil")
	}
}

// TestVerifyTamperedSeq verifies that incrementing Seq after signing causes
// Verify to fail, proving sequence numbers are part of the signed payload.
func TestVerifyTamperedSeq(t *testing.T) {
	pub, priv := newTestKeypair(t)
	rec := sampleRecord()

	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec.Seq++

	if err := audit.Verify(rec, pub); err == nil {
		t.Error("Verify: expected error for tampered Seq, got nil")
	}
}

// TestVerifyWrongKey verifies that a signature produced with one private key
// fails verification against a different public key.
func TestVerifyWrongKey(t *testing.T) {
	_, priv := newTestKeypair(t)
	wrongPub, _ := newTestKeypair(t)
	rec := sampleRecord()

	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := audit.Verify(rec, wrongPub); err == nil {
		t.Error("Verify: expected error when verifying with wrong public key, got nil")
	}
}

// TestVerifyEmptySignature verifies that Verify returns an error immediately
// when Signature is empty, without attempting signature arithmetic.
func TestVerifyEmptySignature(t *testing.T) {
	pub, _ := newTestKeypair(t)
	rec := sampleRecord()
	// rec.Signature is empty — Sign has not been called.

	if err := audit.Verify(rec, pub); err == nil {
		t.Error("Verify: expected error for empty Signature, got nil")
	}
}

// TestCanonicalJSONIsStable verifies that signing the same record twice (with
// the same key) produces the same signature, proving that canonicalJSON is
// deterministic. It also verifies that Verify works on both copies.
func TestCanonicalJSONIsStable(t *testing.T) {
	pub, priv := newTestKeypair(t)

	rec1 := sampleRecord()
	rec2 := sampleRecord() // identical content

	if err := audit.Sign(rec1, priv); err != nil {
		t.Fatalf("Sign rec1: %v", err)
	}
	if err := audit.Sign(rec2, priv); err != nil {
		t.Fatalf("Sign rec2: %v", err)
	}

	// Both records must produce the same signature because canonical JSON is
	// deterministic over the same field values.
	if rec1.Signature != rec2.Signature {
		t.Errorf("signatures differ for identical records:\n  rec1: %s\n  rec2: %s",
			rec1.Signature, rec2.Signature)
	}

	if err := audit.Verify(rec1, pub); err != nil {
		t.Errorf("Verify rec1: %v", err)
	}
	if err := audit.Verify(rec2, pub); err != nil {
		t.Errorf("Verify rec2: %v", err)
	}
}

// TestSignVerifyWithClientTraceID verifies that a record that includes the
// optional ClientTraceID field signs and verifies correctly, and that
// tampering with it causes verification to fail.
func TestSignVerifyWithClientTraceID(t *testing.T) {
	pub, priv := newTestKeypair(t)
	rec := sampleRecord()
	rec.ClientTraceID = "traceparent=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := audit.Verify(rec, pub); err != nil {
		t.Errorf("Verify with ClientTraceID: %v", err)
	}

	// Tamper: clear the trace ID after signing.
	rec.ClientTraceID = ""
	if err := audit.Verify(rec, pub); err == nil {
		t.Error("Verify: expected error after clearing ClientTraceID, got nil")
	}
}

// TestWriteInsertsRecord verifies that Write successfully inserts a signed
// AuditRecord into the audit_log table and that no error is returned.
// It uses a real in-memory SQLite database via store.NewTestDB.
func TestWriteInsertsRecord(t *testing.T) {
	_, priv := newTestKeypair(t)
	st := store.NewTestDB(t)

	// Create the required parent session (foreign key on session_events, though
	// audit_log itself has no FK constraint on session_id — the session is
	// created here for future-proofing and realistic test setup).
	if err := st.CreateSession("sess-abc", "payments-agent", "fp-001"); err != nil {
		t.Fatalf("creating test session: %v", err)
	}

	rec := sampleRecord()
	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := audit.Write(rec, st); err != nil {
		t.Errorf("Write: unexpected error: %v", err)
	}
}

// TestWriteDuplicateIDFails verifies that inserting two records with the same
// ID returns an error, enforcing the PRIMARY KEY constraint on audit_log.id
// and proving the append-only invariant does not silently overwrite.
func TestWriteDuplicateIDFails(t *testing.T) {
	_, priv := newTestKeypair(t)
	st := store.NewTestDB(t)

	if err := st.CreateSession("sess-abc", "payments-agent", "fp-001"); err != nil {
		t.Fatalf("creating test session: %v", err)
	}

	rec := sampleRecord()
	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := audit.Write(rec, st); err != nil {
		t.Fatalf("Write (first): unexpected error: %v", err)
	}

	// Second insert with the same ID must fail.
	rec2 := sampleRecord() // same ID "rec-0001"
	if err := audit.Sign(rec2, priv); err != nil {
		t.Fatalf("Sign rec2: %v", err)
	}
	if err := audit.Write(rec2, st); err == nil {
		t.Error("Write (duplicate): expected error for duplicate ID, got nil")
	}
}

// TestWriteWithOptionalFields verifies that Write succeeds when ClientTraceID
// and DecisionReason are empty (they are stored as NULL in the database).
func TestWriteWithOptionalFields(t *testing.T) {
	_, priv := newTestKeypair(t)
	st := store.NewTestDB(t)

	if err := st.CreateSession("sess-xyz", "billing-agent", "fp-002"); err != nil {
		t.Fatalf("creating test session: %v", err)
	}

	rec := &audit.AuditRecord{
		ID:                "rec-allow-001",
		SessionID:         "sess-xyz",
		Seq:               1,
		AgentName:         "billing-agent",
		ToolName:          "read_invoice",
		ArgumentsSHA256:   "abc123",
		Decision:          "allow",
		DecisionReason:    "", // empty for allow
		PolicyFingerprint: "fp-002",
		AgentJWTSHA256:    "jwt-hash-001",
		ClientTraceID:     "", // not present in this request
		RecordedAt:        time.Now().UnixNano(),
	}
	if err := audit.Sign(rec, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := audit.Write(rec, st); err != nil {
		t.Errorf("Write with empty optional fields: unexpected error: %v", err)
	}
}
