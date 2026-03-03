package audit

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internalaudit "github.com/mercator-hq/truebearing/internal/audit"
	"github.com/mercator-hq/truebearing/internal/identity"
	internalpolicy "github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// newReportCommand returns the `audit report` subcommand.
func newReportCommand() *cobra.Command {
	var (
		sessionID  string
		outputPath string
		keyPath    string
		policyPath string
	)

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a compliance evidence report for a session",
		Long: `Generate a human-readable Markdown compliance evidence report for a session.

The report includes six sections: evidence header, policy summary, execution
timeline, escalation records, cryptographic attestation, and regulatory notes.
It is designed to be handed directly to a compliance officer or regulator.

Examples:
  truebearing audit report --session sess-abc123
  truebearing audit report --session sess-abc123 --output evidence.md
  truebearing audit report --session sess-abc123 --policy policy.yaml --output evidence.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			return runReport(sessionID, keyPath, policyPath, outputPath, cmd)
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "session ID to generate the report for (required)")
	cmd.Flags().StringVar(&outputPath, "output", "", "write Markdown output to this file path (default: stdout)")
	cmd.Flags().StringVar(&keyPath, "key", defaultProxyPubKeyPath(), "path to Ed25519 public key for signature verification (.pub.pem)")
	cmd.Flags().StringVar(&policyPath, "policy", "", "optional path to the policy YAML file for including a policy summary")

	return cmd
}

// runReport implements the `audit report` subcommand. It loads session data,
// audit records, and escalations from the database, attempts to verify record
// signatures, optionally parses the policy file, and writes a Markdown report.
func runReport(sessionID, keyPath, policyPath, outputPath string, cmd *cobra.Command) error {
	dbPath := resolveQueryDBPath()
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database at %s: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	sess, err := st.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("looking up session %q: %w", sessionID, err)
	}

	records, err := st.QueryAuditLog(store.AuditFilter{SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("querying audit log for session %q: %w", sessionID, err)
	}

	escalations, err := st.GetEscalationsBySession(sessionID)
	if err != nil {
		return fmt.Errorf("querying escalations for session %q: %w", sessionID, err)
	}

	// Try to load the public key for cryptographic attestation. If loading
	// fails, the report is still generated but the attestation section notes
	// that verification was skipped.
	pubKey, keyLoadErr := identity.LoadPublicKey(keyPath)

	// Optionally parse the policy file for the policy summary section.
	var (
		pol                       *internalpolicy.Policy
		policyFingerprintMismatch bool
	)
	if policyPath != "" {
		pol, err = internalpolicy.ParseFile(policyPath)
		if err != nil {
			return fmt.Errorf("parsing policy file %s: %w", policyPath, err)
		}
		// Flag a mismatch so the report can warn the reader.
		if pol.Fingerprint != sess.PolicyFingerprint {
			policyFingerprintMismatch = true
		}
	}

	// Design: --output writes to a named file; omitting it writes to cmd's
	// stdout so the command is composable with shell redirection and pagers.
	var w io.Writer
	if outputPath != "" {
		f, ferr := os.Create(outputPath)
		if ferr != nil {
			return fmt.Errorf("creating output file %s: %w", outputPath, ferr)
		}
		defer func() { _ = f.Close() }()
		w = f
	} else {
		w = cmd.OutOrStdout()
	}

	evidenceID := uuid.New().String()
	generatedAt := time.Now().UTC().Format(time.RFC3339)

	return writeReport(w, evidenceID, generatedAt,
		sess.ID, sess.AgentName, sess.PolicyFingerprint,
		records, escalations,
		pubKey, keyLoadErr,
		pol, policyFingerprintMismatch,
	)
}

// writeReport renders the compliance evidence Markdown report to w.
// It emits six sections: evidence header, policy summary, execution timeline,
// escalation records, cryptographic attestation, and regulatory notes.
//
// pubKey may be nil (when keyLoadErr is non-nil); in that case signature
// verification is skipped and the attestation section notes the reason.
// pol may be nil (when --policy was not provided); the policy summary section
// then shows only the fingerprint recorded at session creation time.
func writeReport(
	w io.Writer,
	evidenceID, generatedAt string,
	sessionID, agentName, policyFingerprint string,
	records []store.AuditRecord,
	escalations []store.Escalation,
	pubKey ed25519.PublicKey,
	keyLoadErr error,
	pol *internalpolicy.Policy,
	policyFingerprintMismatch bool,
) error {
	fmt.Fprintln(w, "# Compliance Evidence Report")
	fmt.Fprintln(w)

	if err := writeEvidenceHeader(w, evidenceID, generatedAt, sessionID, agentName, policyFingerprint); err != nil {
		return err
	}
	if err := writePolicySummarySection(w, policyFingerprint, pol, policyFingerprintMismatch); err != nil {
		return err
	}

	// Build a seq→escalation lookup so the timeline can annotate escalated
	// events with their current resolution status without a second DB call.
	escBySeq := make(map[uint64]store.Escalation, len(escalations))
	for _, e := range escalations {
		escBySeq[e.Seq] = e
	}

	if err := writeTimelineSection(w, records, escBySeq); err != nil {
		return err
	}
	if err := writeEscalationSection(w, escalations); err != nil {
		return err
	}
	if err := writeAttestationSection(w, records, pubKey, keyLoadErr); err != nil {
		return err
	}
	writeRegulatoryNotes(w)
	return nil
}

// writeEvidenceHeader emits the "## 1. Evidence Header" section as a Markdown table.
func writeEvidenceHeader(w io.Writer, evidenceID, generatedAt, sessionID, agentName, policyFingerprint string) error {
	fmt.Fprintln(w, "## 1. Evidence Header")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Value |")
	fmt.Fprintln(w, "|---|---|")
	fmt.Fprintf(w, "| Evidence ID | `%s` |\n", evidenceID)
	fmt.Fprintln(w, "| Schema Version | 1.0 |")
	fmt.Fprintf(w, "| Generated At | %s |\n", generatedAt)
	fmt.Fprintf(w, "| Session ID | `%s` |\n", sessionID)
	fmt.Fprintf(w, "| Agent Name | %s |\n", agentName)
	fmt.Fprintf(w, "| Policy Fingerprint | `%s` |\n", policyFingerprint)
	fmt.Fprintln(w)
	return nil
}

// writePolicySummarySection emits the "## 2. Policy Summary" section.
// If pol is nil (no --policy flag given), it emits a fingerprint-only note.
// If the policy fingerprint does not match the session's fingerprint, a warning
// is displayed to alert the reader that the wrong policy file may have been supplied.
func writePolicySummarySection(w io.Writer, policyFingerprint string, pol *internalpolicy.Policy, fingerprintMismatch bool) error {
	fmt.Fprintln(w, "## 2. Policy Summary")
	fmt.Fprintln(w)

	if pol == nil {
		fmt.Fprintln(w, "_No policy file provided. Fingerprint recorded at session creation:_")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "    %s\n", policyFingerprint)
		fmt.Fprintln(w)
		return nil
	}

	if fingerprintMismatch {
		fmt.Fprintf(w, "> **Warning:** the supplied policy file fingerprint (`%s`) does not match "+
			"the session fingerprint (`%s`). This file may not be the policy version that governed "+
			"this session.\n", pol.Fingerprint, policyFingerprint)
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "```")
	writePolicyExplainBlock(w, pol)
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	return nil
}

// writePolicyExplainBlock renders a plain-English policy summary into w, mirroring
// the output of `policy explain`. It is duplicated here (rather than importing
// cmd/policy) because printExplain is unexported and cmd packages must not import
// each other.
func writePolicyExplainBlock(w io.Writer, p *internalpolicy.Policy) {
	fmt.Fprintf(w, "Agent: %s\n", p.Agent)
	fmt.Fprintf(w, "Mode: %s\n", reportDescribeMode(p.EnforcementMode))
	fmt.Fprintf(w, "Allowed tools (%d): %s\n", len(p.MayUse), strings.Join(p.MayUse, ", "))
	fmt.Fprintf(w, "Budget: %s\n", reportDescribeBudget(p.Budget))

	toolNames := reportSortedKeys(p.Tools)

	var seqLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if len(tp.Sequence.OnlyAfter) > 0 {
			seqLines = append(seqLines, fmt.Sprintf(
				"  %s: may only run after [%s]",
				name, strings.Join(tp.Sequence.OnlyAfter, ", "),
			))
		}
		for _, blocked := range tp.Sequence.NeverAfter {
			seqLines = append(seqLines, fmt.Sprintf(
				"  %s: blocked if %s was called this session",
				name, blocked,
			))
		}
		if tp.Sequence.RequiresPriorN != nil {
			seqLines = append(seqLines, fmt.Sprintf(
				"  %s: requires %s called at least %d time(s)",
				name, tp.Sequence.RequiresPriorN.Tool, tp.Sequence.RequiresPriorN.Count,
			))
		}
	}
	if len(seqLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Sequence guards:")
		for _, line := range seqLines {
			fmt.Fprintln(w, line)
		}
	}

	var taintLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if tp.Taint.Applies {
			label := ""
			if tp.Taint.Label != "" {
				label = fmt.Sprintf(" (label: %s)", tp.Taint.Label)
			}
			taintLines = append(taintLines, fmt.Sprintf("  %s: taints the session%s", name, label))
		}
		if tp.Taint.Clears {
			taintLines = append(taintLines, fmt.Sprintf("  %s: clears the taint", name))
		}
	}
	if len(taintLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Taint rules:")
		for _, line := range taintLines {
			fmt.Fprintln(w, line)
		}
	}

	var escLines []string
	for _, name := range toolNames {
		tp := p.Tools[name]
		if tp.EscalateWhen != nil {
			path := strings.TrimPrefix(tp.EscalateWhen.ArgumentPath, "$.")
			escLines = append(escLines, fmt.Sprintf(
				"  %s: escalate to human if %s %s %v",
				name, path, tp.EscalateWhen.Operator, tp.EscalateWhen.Value,
			))
		}
	}
	if len(escLines) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Escalation rules:")
		for _, line := range escLines {
			fmt.Fprintln(w, line)
		}
	}
}

// writeTimelineSection emits the "## 3. Execution Timeline" section as a Markdown table.
// Escalated events are annotated with the current resolution status from escBySeq.
func writeTimelineSection(w io.Writer, records []store.AuditRecord, escBySeq map[uint64]store.Escalation) error {
	fmt.Fprintln(w, "## 3. Execution Timeline")
	fmt.Fprintln(w)

	if len(records) == 0 {
		fmt.Fprintln(w, "_No tool calls recorded for this session._")
		fmt.Fprintln(w)
		return nil
	}

	fmt.Fprintln(w, "| Seq | Timestamp | Tool | Decision | Reason Code | Escalation Status |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for _, r := range records {
		ts := time.Unix(0, r.RecordedAt).UTC().Format(time.RFC3339)
		reason := r.DecisionReason
		if reason == "" {
			reason = "—"
		}
		escStatus := "—"
		if r.Decision == "escalate" {
			if esc, ok := escBySeq[r.Seq]; ok {
				escStatus = strings.ToUpper(esc.Status)
			} else {
				escStatus = "PENDING"
			}
		}
		fmt.Fprintf(w, "| %d | %s | `%s` | **%s** | %s | %s |\n",
			r.Seq, ts, r.ToolName, strings.ToUpper(r.Decision), reason, escStatus)
	}
	fmt.Fprintln(w)
	return nil
}

// writeEscalationSection emits the "## 4. Escalation Records" section.
// The Escalation struct stores only the operator note (Reason); no approver JWT hash
// is recorded in the current schema version. This field is noted in the column header.
func writeEscalationSection(w io.Writer, escalations []store.Escalation) error {
	fmt.Fprintln(w, "## 4. Escalation Records")
	fmt.Fprintln(w)

	if len(escalations) == 0 {
		fmt.Fprintln(w, "_No escalations recorded for this session._")
		fmt.Fprintln(w)
		return nil
	}

	fmt.Fprintln(w, "| ID | Tool | Seq | Status | Operator Note | Created At | Resolved At |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, e := range escalations {
		createdAt := time.Unix(0, e.CreatedAt).UTC().Format(time.RFC3339)
		resolvedAt := "—"
		if e.ResolvedAt != 0 {
			resolvedAt = time.Unix(0, e.ResolvedAt).UTC().Format(time.RFC3339)
		}
		note := e.Reason
		if note == "" {
			note = "—"
		}
		fmt.Fprintf(w, "| `%.8s` | `%s` | %d | **%s** | %s | %s | %s |\n",
			e.ID, e.ToolName, e.Seq, strings.ToUpper(e.Status), note, createdAt, resolvedAt)
	}
	fmt.Fprintln(w)
	return nil
}

// writeAttestationSection emits the "## 5. Cryptographic Attestation" section.
// It converts each store.AuditRecord to an internalaudit.AuditRecord and calls
// internalaudit.Verify. If pubKey is nil (keyLoadErr is non-nil), verification is
// skipped and the section notes why.
func writeAttestationSection(w io.Writer, records []store.AuditRecord, pubKey ed25519.PublicKey, keyLoadErr error) error {
	fmt.Fprintln(w, "## 5. Cryptographic Attestation")
	fmt.Fprintln(w)

	if keyLoadErr != nil {
		fmt.Fprintf(w, "- **Verification skipped:** public key could not be loaded (%s)\n", keyLoadErr)
		fmt.Fprintf(w, "- **Total records:** %d\n", len(records))
		fmt.Fprintln(w)
		return nil
	}

	var okCount, tamperedCount int
	for _, r := range records {
		ar := storeRecordToAuditRecord(r)
		if verr := internalaudit.Verify(&ar, pubKey); verr != nil {
			tamperedCount++
		} else {
			okCount++
		}
	}

	fmt.Fprintf(w, "- **Total records:** %d\n", len(records))
	fmt.Fprintf(w, "- **Verified OK:** %d\n", okCount)
	fmt.Fprintf(w, "- **TAMPERED:** %d\n", tamperedCount)
	if tamperedCount > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "> **WARNING:** One or more audit records failed signature verification. "+
			"The integrity of this evidence package cannot be guaranteed.")
	}
	fmt.Fprintln(w)
	return nil
}

// writeRegulatoryNotes emits the "## 6. Regulatory Notes" section with boilerplate
// text suitable for inclusion in a regulatory submission. Fill-in-the-blank fields
// are marked with [FILL IN ...] placeholders for the compliance officer.
func writeRegulatoryNotes(w io.Writer) {
	fmt.Fprintln(w, "## 6. Regulatory Notes")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This compliance evidence report documents the behavioral boundaries and human oversight")
	fmt.Fprintln(w, "controls applied to an AI agent operating under TrueBearing policy enforcement.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pursuant to **EU AI Act Article 9** (Risk Management System) and related obligations:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "- The AI system identified above operated under documented behavioral policy constraints")
	fmt.Fprintln(w, "  throughout this session.")
	fmt.Fprintln(w, "- All tool invocations and their outcomes are recorded in a tamper-evident, cryptographically")
	fmt.Fprintln(w, "  signed audit log.")
	fmt.Fprintln(w, "- Human oversight was applied at all escalation points as shown in Section 4 above.")
	fmt.Fprintln(w, "- The organisation deploying this AI system: **[FILL IN ORGANISATION NAME]**")
	fmt.Fprintln(w, "- The AI system classification under EU AI Act Annex III: **[FILL IN SYSTEM CLASSIFICATION]**")
	fmt.Fprintln(w, "- The responsible human overseer role: **[FILL IN ROLE/CONTACT]**")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "_This document was generated automatically by TrueBearing. It must be reviewed by the")
	fmt.Fprintln(w, "organisation's compliance officer before submission to any regulatory authority._")
}

// storeRecordToAuditRecord converts a store.AuditRecord to an internalaudit.AuditRecord
// for Ed25519 signature verification. The two types carry identical fields but live in
// separate packages to avoid a circular import (internal/audit imports store for Write;
// store must not import internal/audit for querying).
func storeRecordToAuditRecord(r store.AuditRecord) internalaudit.AuditRecord {
	return internalaudit.AuditRecord{
		ID:                r.ID,
		SessionID:         r.SessionID,
		Seq:               r.Seq,
		AgentName:         r.AgentName,
		ToolName:          r.ToolName,
		ArgumentsSHA256:   r.ArgumentsSHA256,
		Decision:          r.Decision,
		DecisionReason:    r.DecisionReason,
		PolicyFingerprint: r.PolicyFingerprint,
		AgentJWTSHA256:    r.AgentJWTSHA256,
		ClientTraceID:     r.ClientTraceID,
		DelegationChain:   r.DelegationChain,
		RecordedAt:        r.RecordedAt,
		Signature:         r.Signature,
	}
}

// reportDescribeMode converts an EnforcementMode to a human-readable label.
// Duplicated from cmd/policy/explain.go (unexported) to avoid a cross-package
// dependency between cmd/audit and cmd/policy.
func reportDescribeMode(mode internalpolicy.EnforcementMode) string {
	switch mode {
	case internalpolicy.EnforcementBlock:
		return "BLOCK (violations are denied)"
	case internalpolicy.EnforcementShadow:
		return "SHADOW (violations are logged but not blocked)"
	default:
		return "SHADOW (default; violations are logged but not blocked)"
	}
}

// reportDescribeBudget formats a BudgetPolicy as a human-readable string.
// Duplicated from cmd/policy/explain.go (unexported) to avoid a cross-package
// dependency between cmd/audit and cmd/policy.
func reportDescribeBudget(b internalpolicy.BudgetPolicy) string {
	hasCalls := b.MaxToolCalls > 0
	hasCost := b.MaxCostUSD > 0
	switch {
	case hasCalls && hasCost:
		return fmt.Sprintf("%d tool calls / $%.2f per session", b.MaxToolCalls, b.MaxCostUSD)
	case hasCalls:
		return fmt.Sprintf("%d tool calls per session", b.MaxToolCalls)
	case hasCost:
		return fmt.Sprintf("$%.2f per session", b.MaxCostUSD)
	default:
		return "(not configured)"
	}
}

// reportSortedKeys returns the tool names from the policy map in alphabetical order.
// Sorted output ensures the policy summary section is stable across runs.
func reportSortedKeys(tools map[string]internalpolicy.ToolPolicy) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
