package audit

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// fixtureRecords returns three deterministic audit records for use in report tests.
// The records cover the three primary decision outcomes: allow, deny, and escalate.
func fixtureRecords() []store.AuditRecord {
	return []store.AuditRecord{
		{
			ID:                "rec-report-001",
			SessionID:         "sess-report-fixture",
			Seq:               1,
			AgentName:         "compliance-agent",
			ToolName:          "read_document",
			ArgumentsSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Decision:          "allow",
			PolicyFingerprint: "fp-fixture-deadbeef",
			AgentJWTSHA256:    "jwt-sha-placeholder",
			RecordedAt:        time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC).UnixNano(),
			Signature:         "placeholder-sig",
		},
		{
			ID:                "rec-report-002",
			SessionID:         "sess-report-fixture",
			Seq:               2,
			AgentName:         "compliance-agent",
			ToolName:          "submit_report",
			ArgumentsSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Decision:          "deny",
			DecisionReason:    "sequence.only_after: review_complete not satisfied",
			PolicyFingerprint: "fp-fixture-deadbeef",
			AgentJWTSHA256:    "jwt-sha-placeholder",
			RecordedAt:        time.Date(2026, 3, 1, 10, 1, 0, 0, time.UTC).UnixNano(),
			Signature:         "placeholder-sig",
		},
		{
			ID:                "rec-report-003",
			SessionID:         "sess-report-fixture",
			Seq:               3,
			AgentName:         "compliance-agent",
			ToolName:          "wire_transfer",
			ArgumentsSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Decision:          "escalate",
			DecisionReason:    "escalation required: amount exceeds threshold",
			PolicyFingerprint: "fp-fixture-deadbeef",
			AgentJWTSHA256:    "jwt-sha-placeholder",
			RecordedAt:        time.Date(2026, 3, 1, 10, 2, 0, 0, time.UTC).UnixNano(),
			Signature:         "placeholder-sig",
		},
	}
}

// fixtureEscalations returns one approved escalation record matching the third
// fixture audit record (seq=3, tool=wire_transfer).
func fixtureEscalations() []store.Escalation {
	return []store.Escalation{
		{
			ID:         "esc-fixture-001",
			SessionID:  "sess-report-fixture",
			Seq:        3,
			ToolName:   "wire_transfer",
			Status:     "approved",
			Reason:     "reviewed and approved by compliance team",
			CreatedAt:  time.Date(2026, 3, 1, 10, 2, 0, 0, time.UTC).UnixNano(),
			ResolvedAt: time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC).UnixNano(),
		},
	}
}

// TestWriteReport_AllSectionsPresent verifies that the report output contains all
// six section headings, the evidence header fields, all tool names in the timeline,
// the correct record count, the escalation resolution status, and EU AI Act text.
func TestWriteReport_AllSectionsPresent(t *testing.T) {
	records := fixtureRecords()
	escalations := fixtureEscalations()

	var buf bytes.Buffer
	// Pass a non-nil keyLoadErr to skip verification — the attestation section
	// must still be rendered with a "verification skipped" note.
	keyErr := fmt.Errorf("key not available in test")
	err := writeReport(&buf,
		"evidence-uuid-test", "2026-03-01T10:10:00Z",
		"sess-report-fixture", "compliance-agent", "fp-fixture-deadbeef",
		records, escalations,
		nil, keyErr,
		nil, false,
	)
	if err != nil {
		t.Fatalf("writeReport: unexpected error: %v", err)
	}

	out := buf.String()

	// All six section headings must be present.
	for _, heading := range []string{
		"## 1. Evidence Header",
		"## 2. Policy Summary",
		"## 3. Execution Timeline",
		"## 4. Escalation Records",
		"## 5. Cryptographic Attestation",
		"## 6. Regulatory Notes",
	} {
		if !strings.Contains(out, heading) {
			t.Errorf("expected section heading %q in output, got:\n%s", heading, out)
		}
	}

	// Evidence header fields must all appear.
	for _, want := range []string{
		"evidence-uuid-test",
		"sess-report-fixture",
		"compliance-agent",
		"fp-fixture-deadbeef",
		"2026-03-01T10:10:00Z",
		"1.0", // schema version
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in evidence header, got:\n%s", want, out)
		}
	}

	// All three tool names must appear in the timeline.
	for _, tool := range []string{"read_document", "submit_report", "wire_transfer"} {
		if !strings.Contains(out, tool) {
			t.Errorf("expected tool %q in timeline, got:\n%s", tool, out)
		}
	}

	// Denial reason must appear.
	if !strings.Contains(out, "sequence.only_after: review_complete not satisfied") {
		t.Errorf("expected denial reason in timeline, got:\n%s", out)
	}

	// Attestation section must show total record count.
	if !strings.Contains(out, "**Total records:** 3") {
		t.Errorf("expected '**Total records:** 3' in attestation section, got:\n%s", out)
	}

	// Escalation record must show approval status.
	if !strings.Contains(out, "APPROVED") {
		t.Errorf("expected APPROVED status in escalation records, got:\n%s", out)
	}
	if !strings.Contains(out, "reviewed and approved by compliance team") {
		t.Errorf("expected operator note in escalation records, got:\n%s", out)
	}

	// Regulatory notes must cite EU AI Act.
	if !strings.Contains(out, "EU AI Act") {
		t.Errorf("expected EU AI Act reference in regulatory notes, got:\n%s", out)
	}
}

// TestWriteReport_NoRecordsNoEscalations verifies that the report renders correctly
// for an empty session: all six sections are present, the timeline and escalation
// sections show their "no records" messages, and the record count is 0.
func TestWriteReport_NoRecordsNoEscalations(t *testing.T) {
	var buf bytes.Buffer
	keyErr := fmt.Errorf("no key in test")
	err := writeReport(&buf,
		"evidence-empty", "2026-03-01T00:00:00Z",
		"sess-empty", "no-agent", "fp-empty",
		nil, nil,
		nil, keyErr,
		nil, false,
	)
	if err != nil {
		t.Fatalf("writeReport with no records: unexpected error: %v", err)
	}

	out := buf.String()

	// All six sections must still be present.
	for _, heading := range []string{
		"## 1. Evidence Header",
		"## 2. Policy Summary",
		"## 3. Execution Timeline",
		"## 4. Escalation Records",
		"## 5. Cryptographic Attestation",
		"## 6. Regulatory Notes",
	} {
		if !strings.Contains(out, heading) {
			t.Errorf("expected section %q in empty-session output, got:\n%s", heading, out)
		}
	}

	// Empty-state messages must appear in the appropriate sections.
	if !strings.Contains(out, "No tool calls recorded") {
		t.Errorf("expected 'No tool calls recorded' in timeline, got:\n%s", out)
	}
	if !strings.Contains(out, "No escalations recorded") {
		t.Errorf("expected 'No escalations recorded' in escalation section, got:\n%s", out)
	}

	// Record count in attestation must be 0.
	if !strings.Contains(out, "**Total records:** 0") {
		t.Errorf("expected '**Total records:** 0' in attestation section, got:\n%s", out)
	}
}

// TestWriteReport_AttestationWithRealKey verifies the attestation section when a
// real Ed25519 key is provided. The fixture records carry placeholder (invalid)
// signatures, so all records must be counted as TAMPERED and the WARNING must appear.
func TestWriteReport_AttestationWithRealKey(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}

	records := fixtureRecords()

	var buf bytes.Buffer
	if err := writeReport(&buf,
		"ev-tampered", "2026-03-01T00:00:00Z",
		"sess-tampered", "agent", "fp-tampered",
		records, nil,
		pubKey, nil, // key loaded successfully; signatures will fail
		nil, false,
	); err != nil {
		t.Fatalf("writeReport: unexpected error: %v", err)
	}

	out := buf.String()

	// All three records carry invalid signatures, so TAMPERED must be 3.
	if !strings.Contains(out, "**TAMPERED:** 3") {
		t.Errorf("expected '**TAMPERED:** 3' in attestation, got:\n%s", out)
	}
	if !strings.Contains(out, "**Verified OK:** 0") {
		t.Errorf("expected '**Verified OK:** 0' in attestation, got:\n%s", out)
	}
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING in attestation section for tampered records, got:\n%s", out)
	}
}

// TestWriteReport_EvidenceHeaderFieldOrder verifies that the evidence header
// contains the five required fields from the task specification in the report.
func TestWriteReport_EvidenceHeaderFieldOrder(t *testing.T) {
	var buf bytes.Buffer
	err := writeReport(&buf,
		"fixed-uuid-1234", "2026-01-15T09:00:00Z",
		"sess-hdr-test", "hdr-agent", "fp-hdr-deadbeef",
		nil, nil,
		nil, fmt.Errorf("no key"),
		nil, false,
	)
	if err != nil {
		t.Fatalf("writeReport: unexpected error: %v", err)
	}

	out := buf.String()

	// All five required header fields from TODO.md §15.2 must be present.
	for _, field := range []string{
		"Evidence ID",
		"Schema Version",
		"Generated At",
		"Session ID",
		"Agent Name",
		"Policy Fingerprint",
	} {
		if !strings.Contains(out, field) {
			t.Errorf("expected field label %q in evidence header, got:\n%s", field, out)
		}
	}
}

// TestWriteReport_PolicySummaryFingerprintOnly verifies the "no policy file"
// code path emits the fingerprint and a descriptive note.
func TestWriteReport_PolicySummaryFingerprintOnly(t *testing.T) {
	var buf bytes.Buffer
	err := writeReport(&buf,
		"ev-fp-only", "2026-01-15T09:00:00Z",
		"sess-fp-only", "fp-agent", "fp-the-fingerprint-value",
		nil, nil,
		nil, fmt.Errorf("no key"),
		nil, false, // pol is nil
	)
	if err != nil {
		t.Fatalf("writeReport: unexpected error: %v", err)
	}

	out := buf.String()

	if !strings.Contains(out, "No policy file provided") {
		t.Errorf("expected 'No policy file provided' note, got:\n%s", out)
	}
	if !strings.Contains(out, "fp-the-fingerprint-value") {
		t.Errorf("expected fingerprint value in policy summary section, got:\n%s", out)
	}
}

// TestWriteReport_PendingEscalationStatus verifies that an escalated event without
// a resolved escalation record is annotated as PENDING in the timeline.
func TestWriteReport_PendingEscalationStatus(t *testing.T) {
	records := []store.AuditRecord{
		{
			ID:         "rec-esc-pending",
			SessionID:  "sess-esc-pending",
			Seq:        1,
			ToolName:   "risky_tool",
			Decision:   "escalate",
			RecordedAt: time.Now().UnixNano(),
			Signature:  "placeholder",
		},
	}

	var buf bytes.Buffer
	err := writeReport(&buf,
		"ev-pending", "2026-01-15T09:00:00Z",
		"sess-esc-pending", "agent", "fp",
		records, nil, // no resolved escalation record
		nil, fmt.Errorf("no key"),
		nil, false,
	)
	if err != nil {
		t.Fatalf("writeReport: unexpected error: %v", err)
	}

	out := buf.String()

	if !strings.Contains(out, "PENDING") {
		t.Errorf("expected PENDING escalation status in timeline, got:\n%s", out)
	}
}
