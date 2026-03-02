//go:build integration

package engine_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
)

// policyDir is the path to the testdata policy fixtures relative to the
// package directory (internal/engine/). Go test always runs with the working
// directory set to the package directory.
const policyDir = "../../testdata/policies"

// buildPipeline constructs the full evaluation pipeline wired to the given store.
// This is the same ordering used by the proxy (MayUse → Budget → Taint →
// Sequence → RateLimit → Escalation).
func buildPipeline(st *store.Store) *engine.Pipeline {
	return engine.New(
		&engine.MayUseEvaluator{},
		&engine.BudgetEvaluator{},
		&engine.TaintEvaluator{},
		&engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.RateLimitEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.EscalationEvaluator{Store: &engine.StoreBackend{Store: st}},
	)
}

// runCall simulates a single tool call through the full evaluation pipeline,
// then persists the resulting session event and any taint or counter mutations
// to the store. It mirrors what the proxy handler does after calling
// pipeline.Evaluate. Returns the Decision for assertions.
//
// Design: this helper maintains sess in memory across calls so that the taint
// flag and call count are available to the next evaluator run without a round-
// trip to the database. Counter increments are also applied to sess.ToolCallCount
// and sess.EstimatedCostUSD in memory so BudgetEvaluator sees the correct state.
func runCall(
	t *testing.T,
	pipeline *engine.Pipeline,
	st *store.Store,
	sess *session.Session,
	pol *policy.Policy,
	toolName string,
	argsJSON string,
) engine.Decision {
	t.Helper()

	call := &engine.ToolCall{
		SessionID:   sess.ID,
		AgentName:   sess.AgentName,
		ToolName:    toolName,
		Arguments:   json.RawMessage(argsJSON),
		RequestedAt: time.Now(),
	}

	taintBefore := sess.Tainted
	decision := pipeline.Evaluate(context.Background(), call, sess, pol)

	// Append the session event so the SequenceEvaluator sees it on future calls.
	event := &store.SessionEvent{
		SessionID:     sess.ID,
		ToolName:      toolName,
		ArgumentsJSON: argsJSON,
		Decision:      string(decision.Action),
		PolicyRule:    decision.RuleID,
	}
	if err := st.AppendEvent(event); err != nil {
		t.Fatalf("runCall: appending session event for %q: %v", toolName, err)
	}

	// Persist taint mutation if the pipeline changed it.
	if sess.Tainted != taintBefore {
		if err := st.UpdateSessionTaint(sess.ID, sess.Tainted); err != nil {
			t.Fatalf("runCall: updating session taint: %v", err)
		}
	}

	// Increment in-memory and database counters for allowed calls so that the
	// BudgetEvaluator sees the correct consumed budget on the next call.
	if decision.Action == engine.Allow || decision.Action == engine.ShadowDeny {
		sess.ToolCallCount++
		sess.EstimatedCostUSD += 0.001
		if err := st.IncrementSessionCounters(sess.ID, 0.001); err != nil {
			t.Fatalf("runCall: incrementing session counters: %v", err)
		}
	}

	return decision
}

// newTestSession creates a session row in the store and returns the in-memory
// Session snapshot. The policy fingerprint is bound at session creation time,
// mirroring the proxy's implicit session creation on first tool call.
func newTestSession(t *testing.T, st *store.Store, pol *policy.Policy, sessionID, agentName string) *session.Session {
	t.Helper()
	if err := st.CreateSession(sessionID, agentName, pol.Fingerprint); err != nil {
		t.Fatalf("newTestSession: creating session %q: %v", sessionID, err)
	}
	return &session.Session{
		ID:                sessionID,
		AgentName:         agentName,
		PolicyFingerprint: pol.Fingerprint,
	}
}

// assertDecision fails the test if the decision's Action does not match wantAction.
func assertDecision(t *testing.T, label string, got engine.Decision, wantAction engine.Action) {
	t.Helper()
	if got.Action != wantAction {
		t.Errorf("%s: Action = %q, want %q (reason: %s)", label, got.Action, wantAction, got.Reason)
	}
}

// TestPaymentSequenceGuard verifies that the sequence guard for a payment
// workflow is enforced correctly. The only_after predicates require
// verify_invoice and manager_approval to appear in session history before
// execute_wire_transfer is permitted.
//
// Pattern: fintech-payment-sequence.policy.yaml
// Tool under test: execute_wire_transfer
func TestPaymentSequenceGuard(t *testing.T) {
	pol, err := policy.ParseFile(filepath.Join(policyDir, "fintech-payment-sequence.policy.yaml"))
	if err != nil {
		t.Fatalf("loading policy: %v", err)
	}
	st := store.NewTestDB(t)
	pipeline := buildPipeline(st)
	sess := newTestSession(t, st, pol, "sess-payment-guard", "payments-agent")

	// Step 1: execute_wire_transfer without any prior calls — denied because
	// neither verify_invoice nor manager_approval appears in history.
	// Note: execute_wire_transfer has tool-level enforcement_mode: block, so
	// the pipeline denies rather than shadow-denying even though global mode is shadow.
	d := runCall(t, pipeline, st, sess, pol, "execute_wire_transfer", `{"amount_usd":5000}`)
	assertDecision(t, "step 1 — no prereqs", d, engine.Deny)

	// Step 2: verify_invoice — satisfies one of the two only_after requirements.
	d = runCall(t, pipeline, st, sess, pol, "verify_invoice", `{}`)
	assertDecision(t, "step 2 — verify_invoice", d, engine.Allow)

	// Step 3: execute_wire_transfer again — still denied because manager_approval
	// has not been called yet.
	d = runCall(t, pipeline, st, sess, pol, "execute_wire_transfer", `{"amount_usd":5000}`)
	assertDecision(t, "step 3 — missing manager_approval", d, engine.Deny)

	// Step 4: manager_approval — satisfies the second only_after requirement.
	d = runCall(t, pipeline, st, sess, pol, "manager_approval", `{}`)
	assertDecision(t, "step 4 — manager_approval", d, engine.Allow)

	// Step 5: execute_wire_transfer with amount below the $10,000 escalation
	// threshold — both only_after prerequisites are now satisfied and the
	// requires_prior_n (verify_invoice ≥ 1) is also satisfied. Allowed.
	d = runCall(t, pipeline, st, sess, pol, "execute_wire_transfer", `{"amount_usd":5000}`)
	assertDecision(t, "step 5 — prereqs satisfied, amount below threshold", d, engine.Allow)

	// Step 6: execute_wire_transfer with amount above the $10,000 escalation
	// threshold — the escalation rule triggers.
	d = runCall(t, pipeline, st, sess, pol, "execute_wire_transfer", `{"amount_usd":15000}`)
	assertDecision(t, "step 6 — amount above escalation threshold", d, engine.Escalate)
}

// TestPHITaintPropagation verifies that the taint mechanism blocks submission
// tools after protected health information is read, and that the taint evaluator
// fires before the sequence evaluator in the pipeline ordering.
//
// Pattern: healthcare-phi-taint.policy.yaml
// Tool under test: submit_claim (blocked by taint from read_phi)
func TestPHITaintPropagation(t *testing.T) {
	pol, err := policy.ParseFile(filepath.Join(policyDir, "healthcare-phi-taint.policy.yaml"))
	if err != nil {
		t.Fatalf("loading policy: %v", err)
	}
	st := store.NewTestDB(t)
	pipeline := buildPipeline(st)
	sess := newTestSession(t, st, pol, "sess-phi-taint", "billing-agent")

	// Step 1: verify_eligibility — no constraints, allowed.
	d := runCall(t, pipeline, st, sess, pol, "verify_eligibility", `{}`)
	assertDecision(t, "step 1 — verify_eligibility", d, engine.Allow)

	// Step 2: read_patient_record — no constraints, allowed.
	d = runCall(t, pipeline, st, sess, pol, "read_patient_record", `{}`)
	assertDecision(t, "step 2 — read_patient_record", d, engine.Allow)

	// Step 3: submit_claim — only_after prerequisites satisfied, session not
	// tainted yet, amount below $5,000 escalation threshold. Allowed.
	d = runCall(t, pipeline, st, sess, pol, "submit_claim", `{"claim_amount_usd":100}`)
	assertDecision(t, "step 3 — prereqs satisfied, no taint", d, engine.Allow)

	// Step 4: read_phi — applies taint to the session. The call itself is
	// allowed; the taint takes effect for subsequent calls.
	d = runCall(t, pipeline, st, sess, pol, "read_phi", `{}`)
	assertDecision(t, "step 4 — read_phi taints session", d, engine.Allow)
	if !sess.Tainted {
		t.Error("step 4: sess.Tainted should be true after read_phi was allowed")
	}

	// Step 5: submit_claim while session is tainted — the TaintEvaluator (stage 3)
	// fires before the SequenceEvaluator (stage 4) and denies because submit_claim
	// has never_after: [read_phi] and read_phi is a taint-applying tool.
	d = runCall(t, pipeline, st, sess, pol, "submit_claim", `{"claim_amount_usd":100}`)
	assertDecision(t, "step 5 — taint blocks submit_claim", d, engine.Deny)
	if d.RuleID != "taint.session_tainted" {
		t.Errorf("step 5: RuleID = %q, want %q", d.RuleID, "taint.session_tainted")
	}

	// Step 6: run_compliance_scan — clears the session taint.
	d = runCall(t, pipeline, st, sess, pol, "run_compliance_scan", `{}`)
	assertDecision(t, "step 6 — run_compliance_scan clears taint", d, engine.Allow)
	if sess.Tainted {
		t.Error("step 6: sess.Tainted should be false after run_compliance_scan")
	}

	// Step 7: submit_claim after taint clearance — the TaintEvaluator now passes
	// (session not tainted), but the SequenceEvaluator denies because read_phi
	// is in the immutable session history and its never_after guard is permanent.
	// Design: taint.clears unblocks the TaintEvaluator path only; the sequence
	// never_after guard records the history-based permanent constraint.
	d = runCall(t, pipeline, st, sess, pol, "submit_claim", `{"claim_amount_usd":100}`)
	assertDecision(t, "step 7 — sequence never_after fires after taint cleared", d, engine.Deny)
	if d.RuleID != "sequence" {
		t.Errorf("step 7: RuleID = %q, want %q (SequenceEvaluator should deny)", d.RuleID, "sequence")
	}
}

// TestClaimsSequentialGuard verifies that the requires_prior_n predicate blocks
// claim approval until the quality check tool has been called the required
// number of times.
//
// Pattern: insurance-claims-sequence.policy.yaml
// Tool under test: approve_claim (requires run_quality_check called ≥ 2 times)
func TestClaimsSequentialGuard(t *testing.T) {
	pol, err := policy.ParseFile(filepath.Join(policyDir, "insurance-claims-sequence.policy.yaml"))
	if err != nil {
		t.Fatalf("loading policy: %v", err)
	}
	st := store.NewTestDB(t)
	pipeline := buildPipeline(st)
	sess := newTestSession(t, st, pol, "sess-claims-guard", "claims-agent")

	// Build up the only_after prerequisite chain: ingest → fraud → adjudicate.
	for _, tool := range []string{"ingest_claim", "fraud_check", "adjudicate_claim"} {
		d := runCall(t, pipeline, st, sess, pol, tool, `{}`)
		assertDecision(t, "prereq "+tool, d, engine.Allow)
	}

	// Step 4: approve_claim with zero quality checks — denied by requires_prior_n.
	d := runCall(t, pipeline, st, sess, pol, "approve_claim", `{"payout_usd":1000}`)
	assertDecision(t, "step 4 — zero quality checks", d, engine.Deny)
	if d.RuleID != "sequence" {
		t.Errorf("step 4: RuleID = %q, want %q", d.RuleID, "sequence")
	}

	// Step 5: one quality check — still one below the required minimum of two.
	d = runCall(t, pipeline, st, sess, pol, "run_quality_check", `{}`)
	assertDecision(t, "step 5 — first quality check", d, engine.Allow)

	// Step 6: approve_claim with one quality check — still denied (count=1 < 2).
	d = runCall(t, pipeline, st, sess, pol, "approve_claim", `{"payout_usd":1000}`)
	assertDecision(t, "step 6 — one quality check insufficient", d, engine.Deny)

	// Step 7: second quality check — satisfies requires_prior_n.count = 2.
	d = runCall(t, pipeline, st, sess, pol, "run_quality_check", `{}`)
	assertDecision(t, "step 7 — second quality check", d, engine.Allow)

	// Step 8: approve_claim with payout below the $25,000 escalation threshold —
	// all sequence predicates are now satisfied. Allowed.
	d = runCall(t, pipeline, st, sess, pol, "approve_claim", `{"payout_usd":1000}`)
	assertDecision(t, "step 8 — two quality checks, low payout", d, engine.Allow)

	// Step 9: approve_claim with payout above the $25,000 escalation threshold —
	// sequence is satisfied but the escalation rule triggers.
	d = runCall(t, pipeline, st, sess, pol, "approve_claim", `{"payout_usd":30000}`)
	assertDecision(t, "step 9 — payout above escalation threshold", d, engine.Escalate)
}

// TestPrivilegedDocumentExfiltrationGuard verifies that reading a privileged
// document taints the session and blocks all outbound transmission tools until
// explicit clearance is obtained via the privilege review gate.
//
// Pattern: legal-exfiltration-guard.policy.yaml
// Tools under test: send_document_external, send_email (both blocked by taint)
func TestPrivilegedDocumentExfiltrationGuard(t *testing.T) {
	pol, err := policy.ParseFile(filepath.Join(policyDir, "legal-exfiltration-guard.policy.yaml"))
	if err != nil {
		t.Fatalf("loading policy: %v", err)
	}
	st := store.NewTestDB(t)
	pipeline := buildPipeline(st)
	sess := newTestSession(t, st, pol, "sess-exfil-guard", "legal-agent")

	// Step 1: read_document (non-privileged) — no constraints, allowed.
	d := runCall(t, pipeline, st, sess, pol, "read_document", `{}`)
	assertDecision(t, "step 1 — read_document (non-privileged)", d, engine.Allow)

	// Step 2: send_document_external before any privileged read — the taint is
	// not set and the never_after check passes (read_privileged_document not in history).
	// The sequence guard only fires once a privileged document has been read.
	d = runCall(t, pipeline, st, sess, pol, "send_document_external", `{}`)
	assertDecision(t, "step 2 — send before privileged read", d, engine.Allow)

	// Step 3: read_privileged_document — applies taint. Call itself is allowed.
	d = runCall(t, pipeline, st, sess, pol, "read_privileged_document", `{}`)
	assertDecision(t, "step 3 — read_privileged_document taints session", d, engine.Allow)
	if !sess.Tainted {
		t.Error("step 3: sess.Tainted should be true after read_privileged_document")
	}

	// Step 4: send_document_external while tainted — TaintEvaluator denies because
	// send_document_external has never_after: [read_privileged_document] and
	// read_privileged_document is a taint-applying tool.
	d = runCall(t, pipeline, st, sess, pol, "send_document_external", `{}`)
	assertDecision(t, "step 4 — external send blocked by taint", d, engine.Deny)
	if d.RuleID != "taint.session_tainted" {
		t.Errorf("step 4: RuleID = %q, want %q", d.RuleID, "taint.session_tainted")
	}

	// Step 5: send_email while tainted — same guard applies to email.
	d = runCall(t, pipeline, st, sess, pol, "send_email", `{}`)
	assertDecision(t, "step 5 — email blocked by taint", d, engine.Deny)

	// Step 6: run_privilege_review — clears the session taint.
	d = runCall(t, pipeline, st, sess, pol, "run_privilege_review", `{}`)
	assertDecision(t, "step 6 — privilege review clears taint", d, engine.Allow)
	if sess.Tainted {
		t.Error("step 6: sess.Tainted should be false after run_privilege_review")
	}

	// Step 7: send_document_external after taint clearance — TaintEvaluator passes
	// but SequenceEvaluator denies because read_privileged_document is still in
	// the immutable session history (never_after is a permanent guard).
	d = runCall(t, pipeline, st, sess, pol, "send_document_external", `{}`)
	assertDecision(t, "step 7 — sequence never_after fires after taint cleared", d, engine.Deny)
	if d.RuleID != "sequence" {
		t.Errorf("step 7: RuleID = %q, want %q", d.RuleID, "sequence")
	}
}

// TestMultiApprovalRegulatory verifies that a regulatory filing tool cannot be
// called until the full review pipeline has run AND the qa_review tool has been
// called at least twice (two independent quality assurance passes).
//
// Pattern: regulatory-multi-approval.policy.yaml
// Tool under test: submit_regulatory_filing
func TestMultiApprovalRegulatory(t *testing.T) {
	pol, err := policy.ParseFile(filepath.Join(policyDir, "regulatory-multi-approval.policy.yaml"))
	if err != nil {
		t.Fatalf("loading policy: %v", err)
	}
	st := store.NewTestDB(t)
	pipeline := buildPipeline(st)
	sess := newTestSession(t, st, pol, "sess-regulatory", "regulatory-agent")

	// Step 1: submit_regulatory_filing without any prior steps — denied.
	d := runCall(t, pipeline, st, sess, pol, "submit_regulatory_filing", `{}`)
	assertDecision(t, "step 1 — no prereqs", d, engine.Deny)

	// Step 2–4: complete the four required review disciplines.
	for _, tool := range []string{"draft_document", "medical_review", "legal_review"} {
		d := runCall(t, pipeline, st, sess, pol, tool, `{}`)
		assertDecision(t, "prereq "+tool, d, engine.Allow)
	}

	// Step 5: first qa_review — satisfies only_after for qa_review but not
	// requires_prior_n.count = 2.
	d = runCall(t, pipeline, st, sess, pol, "qa_review", `{}`)
	assertDecision(t, "step 5 — first qa_review", d, engine.Allow)

	// Step 6: submit_regulatory_filing with only one qa_review — denied because
	// requires_prior_n requires two calls.
	d = runCall(t, pipeline, st, sess, pol, "submit_regulatory_filing", `{}`)
	assertDecision(t, "step 6 — one qa_review insufficient", d, engine.Deny)
	if d.RuleID != "sequence" {
		t.Errorf("step 6: RuleID = %q, want %q", d.RuleID, "sequence")
	}

	// Step 7: second qa_review — now requires_prior_n is satisfied (count = 2).
	d = runCall(t, pipeline, st, sess, pol, "qa_review", `{}`)
	assertDecision(t, "step 7 — second qa_review", d, engine.Allow)

	// Step 8: submit_regulatory_filing — all four review disciplines have run and
	// qa_review has been called twice. Allowed.
	d = runCall(t, pipeline, st, sess, pol, "submit_regulatory_filing", `{}`)
	assertDecision(t, "step 8 — full pipeline complete, two QA passes", d, engine.Allow)
}
