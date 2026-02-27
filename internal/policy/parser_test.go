package policy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mercator-hq/truebearing/internal/policy"
)

// fullDSLPolicy is the complete example from mvp-plan.md §6.1.
// It exercises every field in the Policy DSL: global enforcement_mode,
// session limits, budget, may_use, tool-level enforcement_mode, sequence
// predicates (only_after, never_after, requires_prior_n), taint rules
// (applies, label, clears), and escalation rules.
const fullDSLPolicy = `
version: "1"
agent: finance-bot
enforcement_mode: shadow

session:
  max_history: 1000
  max_duration_seconds: 3600

budget:
  max_tool_calls: 50
  max_cost_usd: 5.00

may_use:
  - read_invoice
  - verify_invoice
  - manager_approval
  - execute_wire_transfer
  - read_external_email
  - run_compliance_scan
  - check_escalation_status

tools:
  execute_wire_transfer:
    enforcement_mode: block
    sequence:
      only_after:
        - verify_invoice
        - manager_approval
      never_after:
        - read_external_email
      requires_prior_n:
        tool: verify_invoice
        count: 1
    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 10000

  read_external_email:
    taint:
      applies: true
      label: "external_email_read"

  run_compliance_scan:
    taint:
      clears: true

  read_invoice: {}
  verify_invoice: {}
  manager_approval: {}
`

func TestParseBytes_FullDSLExample(t *testing.T) {
	p, err := policy.ParseBytes([]byte(fullDSLPolicy), "test")
	if err != nil {
		t.Fatalf("ParseBytes returned unexpected error: %v", err)
	}

	// Top-level fields.
	if p.Version != "1" {
		t.Errorf("Version = %q, want %q", p.Version, "1")
	}
	if p.Agent != "finance-bot" {
		t.Errorf("Agent = %q, want %q", p.Agent, "finance-bot")
	}
	if p.EnforcementMode != policy.EnforcementShadow {
		t.Errorf("EnforcementMode = %q, want %q", p.EnforcementMode, policy.EnforcementShadow)
	}

	// Session limits.
	if p.Session.MaxHistory != 1000 {
		t.Errorf("Session.MaxHistory = %d, want 1000", p.Session.MaxHistory)
	}
	if p.Session.MaxDurationSeconds != 3600 {
		t.Errorf("Session.MaxDurationSeconds = %d, want 3600", p.Session.MaxDurationSeconds)
	}

	// Budget.
	if p.Budget.MaxToolCalls != 50 {
		t.Errorf("Budget.MaxToolCalls = %d, want 50", p.Budget.MaxToolCalls)
	}
	if p.Budget.MaxCostUSD != 5.00 {
		t.Errorf("Budget.MaxCostUSD = %v, want 5.00", p.Budget.MaxCostUSD)
	}

	// MayUse list (order matters — preserves YAML declaration order).
	wantMayUse := []string{
		"read_invoice", "verify_invoice", "manager_approval",
		"execute_wire_transfer", "read_external_email",
		"run_compliance_scan", "check_escalation_status",
	}
	if len(p.MayUse) != len(wantMayUse) {
		t.Fatalf("len(MayUse) = %d, want %d", len(p.MayUse), len(wantMayUse))
	}
	for i, want := range wantMayUse {
		if p.MayUse[i] != want {
			t.Errorf("MayUse[%d] = %q, want %q", i, p.MayUse[i], want)
		}
	}

	// Tools map has the expected number of entries.
	if len(p.Tools) != 6 {
		t.Errorf("len(Tools) = %d, want 6", len(p.Tools))
	}

	// --- execute_wire_transfer ---
	ewt, ok := p.Tools["execute_wire_transfer"]
	if !ok {
		t.Fatal("Tools missing key \"execute_wire_transfer\"")
	}
	if ewt.EnforcementMode != policy.EnforcementBlock {
		t.Errorf("execute_wire_transfer.EnforcementMode = %q, want %q",
			ewt.EnforcementMode, policy.EnforcementBlock)
	}

	// Sequence: only_after.
	wantOnlyAfter := []string{"verify_invoice", "manager_approval"}
	if len(ewt.Sequence.OnlyAfter) != len(wantOnlyAfter) {
		t.Fatalf("OnlyAfter len = %d, want %d", len(ewt.Sequence.OnlyAfter), len(wantOnlyAfter))
	}
	for i, want := range wantOnlyAfter {
		if ewt.Sequence.OnlyAfter[i] != want {
			t.Errorf("OnlyAfter[%d] = %q, want %q", i, ewt.Sequence.OnlyAfter[i], want)
		}
	}

	// Sequence: never_after.
	if len(ewt.Sequence.NeverAfter) != 1 || ewt.Sequence.NeverAfter[0] != "read_external_email" {
		t.Errorf("NeverAfter = %v, want [read_external_email]", ewt.Sequence.NeverAfter)
	}

	// Sequence: requires_prior_n.
	if ewt.Sequence.RequiresPriorN == nil {
		t.Fatal("RequiresPriorN is nil, want non-nil")
	}
	if ewt.Sequence.RequiresPriorN.Tool != "verify_invoice" {
		t.Errorf("RequiresPriorN.Tool = %q, want %q", ewt.Sequence.RequiresPriorN.Tool, "verify_invoice")
	}
	if ewt.Sequence.RequiresPriorN.Count != 1 {
		t.Errorf("RequiresPriorN.Count = %d, want 1", ewt.Sequence.RequiresPriorN.Count)
	}

	// Escalation rule.
	if ewt.EscalateWhen == nil {
		t.Fatal("EscalateWhen is nil, want non-nil")
	}
	if ewt.EscalateWhen.ArgumentPath != "$.amount_usd" {
		t.Errorf("EscalateWhen.ArgumentPath = %q, want %q", ewt.EscalateWhen.ArgumentPath, "$.amount_usd")
	}
	if ewt.EscalateWhen.Operator != ">" {
		t.Errorf("EscalateWhen.Operator = %q, want %q", ewt.EscalateWhen.Operator, ">")
	}
	// yaml.v3 unmarshals a YAML integer literal into int when the target type
	// is interface{}.
	if ewt.EscalateWhen.Value != 10000 {
		t.Errorf("EscalateWhen.Value = %v (%T), want 10000 (int)", ewt.EscalateWhen.Value, ewt.EscalateWhen.Value)
	}

	// --- read_external_email: taint applies ---
	ree, ok := p.Tools["read_external_email"]
	if !ok {
		t.Fatal("Tools missing key \"read_external_email\"")
	}
	if !ree.Taint.Applies {
		t.Error("read_external_email.Taint.Applies = false, want true")
	}
	if ree.Taint.Label != "external_email_read" {
		t.Errorf("read_external_email.Taint.Label = %q, want %q", ree.Taint.Label, "external_email_read")
	}

	// --- run_compliance_scan: taint clears ---
	rcs, ok := p.Tools["run_compliance_scan"]
	if !ok {
		t.Fatal("Tools missing key \"run_compliance_scan\"")
	}
	if !rcs.Taint.Clears {
		t.Error("run_compliance_scan.Taint.Clears = false, want true")
	}

	// Fingerprint must be set and be a valid 64-char hex SHA-256.
	if p.Fingerprint == "" {
		t.Error("Fingerprint is empty, want 64-char hex string")
	}
	if len(p.Fingerprint) != 64 {
		t.Errorf("len(Fingerprint) = %d, want 64", len(p.Fingerprint))
	}

	// ShortFingerprint is the first 8 characters.
	if p.ShortFingerprint() != p.Fingerprint[:8] {
		t.Errorf("ShortFingerprint() = %q, want first 8 chars of Fingerprint %q",
			p.ShortFingerprint(), p.Fingerprint[:8])
	}

	// SourcePath is set from the sourcePath argument.
	if p.SourcePath != "test" {
		t.Errorf("SourcePath = %q, want %q", p.SourcePath, "test")
	}
}

func TestParseBytes_MinimalPolicy(t *testing.T) {
	const minimal = `
version: "1"
agent: payments-agent
may_use:
  - submit_payment
  - check_escalation_status
`
	p, err := policy.ParseBytes([]byte(minimal), "minimal")
	if err != nil {
		t.Fatalf("ParseBytes returned unexpected error: %v", err)
	}
	if p.Version != "1" {
		t.Errorf("Version = %q, want %q", p.Version, "1")
	}
	if p.Agent != "payments-agent" {
		t.Errorf("Agent = %q, want %q", p.Agent, "payments-agent")
	}

	// Normalization: nil tools map becomes an empty map.
	if p.Tools == nil {
		t.Error("Tools is nil after normalization, want empty map (not nil)")
	}

	// Fingerprint is set.
	if p.Fingerprint == "" {
		t.Error("Fingerprint is empty")
	}
}

func TestParseBytes_MalformedYAML(t *testing.T) {
	// An unclosed flow sequence is definitively invalid YAML. The parser must
	// return an error and must never panic.
	_, err := policy.ParseBytes([]byte("key: [unclosed"), "malformed")
	if err == nil {
		t.Error("ParseBytes returned nil error for malformed YAML, want error")
	}
}

func TestParseBytes_MissingVersion(t *testing.T) {
	const noVersion = `
agent: payments-agent
may_use:
  - submit_payment
`
	_, err := policy.ParseBytes([]byte(noVersion), "no-version.yaml")
	if err == nil {
		t.Fatal("ParseBytes returned nil error for policy missing version, want error")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error %q does not mention missing field name \"version\"", err.Error())
	}
}

func TestParseBytes_MissingAgent(t *testing.T) {
	const noAgent = `
version: "1"
may_use:
  - submit_payment
`
	_, err := policy.ParseBytes([]byte(noAgent), "no-agent.yaml")
	if err == nil {
		t.Fatal("ParseBytes returned nil error for policy missing agent, want error")
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("error %q does not mention missing field name \"agent\"", err.Error())
	}
}

func TestParseFile_FileNotFound(t *testing.T) {
	_, err := policy.ParseFile("/nonexistent/path/truebearing.policy.yaml")
	if err == nil {
		t.Error("ParseFile returned nil error for nonexistent file, want error")
	}
}

func TestParseFile_ReadsFromDisk(t *testing.T) {
	const content = `
version: "1"
agent: claims-agent
may_use:
  - ingest_claim
`
	dir := t.TempDir()
	path := filepath.Join(dir, "claims.policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := policy.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}
	if p.Agent != "claims-agent" {
		t.Errorf("Agent = %q, want %q", p.Agent, "claims-agent")
	}
	if p.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", p.SourcePath, path)
	}
}

func TestFingerprint_StableAcrossWhitespace(t *testing.T) {
	// These two YAML strings have identical semantic content but different
	// whitespace and blank lines. The fingerprint must be identical.
	const policy1 = `
version: "1"
agent: payments-agent
enforcement_mode: shadow

may_use:
  - tool_a
  - tool_b
`
	const policy2 = `version: "1"
agent: payments-agent
enforcement_mode: shadow
may_use:
  - tool_a
  - tool_b`

	p1, err := policy.ParseBytes([]byte(policy1), "p1")
	if err != nil {
		t.Fatalf("ParseBytes(policy1): %v", err)
	}
	p2, err := policy.ParseBytes([]byte(policy2), "p2")
	if err != nil {
		t.Fatalf("ParseBytes(policy2): %v", err)
	}
	if p1.Fingerprint != p2.Fingerprint {
		t.Errorf("fingerprints differ for identical content:\n  p1=%s\n  p2=%s",
			p1.Fingerprint, p2.Fingerprint)
	}
}

func TestFingerprint_DifferentForDifferentContent(t *testing.T) {
	const pA = `
version: "1"
agent: agent-alpha
may_use:
  - tool_a
`
	const pB = `
version: "1"
agent: agent-beta
may_use:
  - tool_a
`
	pa, err := policy.ParseBytes([]byte(pA), "a")
	if err != nil {
		t.Fatalf("ParseBytes(pA): %v", err)
	}
	pb, err := policy.ParseBytes([]byte(pB), "b")
	if err != nil {
		t.Fatalf("ParseBytes(pB): %v", err)
	}
	if pa.Fingerprint == pb.Fingerprint {
		t.Error("fingerprints are equal for policies with different agent names, want different fingerprints")
	}
}

func TestFingerprint_ExcludesSourcePath(t *testing.T) {
	// Two files with the same content loaded from different paths must produce
	// the same fingerprint. SourcePath is a local filesystem artifact and must
	// not influence the policy identity.
	const content = `
version: "1"
agent: legal-agent
may_use:
  - review_document
`
	p1, err := policy.ParseBytes([]byte(content), "path/one/legal.policy.yaml")
	if err != nil {
		t.Fatalf("ParseBytes(p1): %v", err)
	}
	p2, err := policy.ParseBytes([]byte(content), "path/two/legal.policy.yaml")
	if err != nil {
		t.Fatalf("ParseBytes(p2): %v", err)
	}
	if p1.Fingerprint != p2.Fingerprint {
		t.Errorf("fingerprints differ for same content at different paths:\n  p1=%s\n  p2=%s",
			p1.Fingerprint, p2.Fingerprint)
	}
}

func TestNormalize_NilSlicesBeforeFingerprint(t *testing.T) {
	// A policy that omits may_use and tools must normalize those to empty
	// collections so the fingerprint is identical to a policy that explicitly
	// writes may_use: [] and tools: {}.
	const omitted = `
version: "1"
agent: data-agent
`
	const explicit = `
version: "1"
agent: data-agent
may_use: []
tools: {}
`
	po, err := policy.ParseBytes([]byte(omitted), "omitted")
	if err != nil {
		t.Fatalf("ParseBytes(omitted): %v", err)
	}
	pe, err := policy.ParseBytes([]byte(explicit), "explicit")
	if err != nil {
		t.Fatalf("ParseBytes(explicit): %v", err)
	}
	if po.Fingerprint != pe.Fingerprint {
		t.Errorf("fingerprints differ between omitted and explicit empty collections:\n  omitted=%s\n  explicit=%s",
			po.Fingerprint, pe.Fingerprint)
	}
	// Both should have non-nil, empty MayUse.
	if po.MayUse == nil {
		t.Error("MayUse is nil after normalization, want empty slice")
	}
	if len(po.MayUse) != 0 {
		t.Errorf("len(MayUse) = %d, want 0", len(po.MayUse))
	}
}
