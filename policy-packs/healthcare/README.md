# Policy Pack: Healthcare — HIPAA PHI Guard

This policy pack enforces the HIPAA minimum-necessary standard for automated healthcare
billing and claims workflows. It is designed for agents that access patient health records,
verify insurance eligibility, and submit claims to payers or clearinghouses.

---

## Policies in this pack

### `hipaa-phi-guard.policy.yaml`

**Pattern:** PHI taint with compliance gate before any outbound submission.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `submit_claim` requires `verify_eligibility` + `read_patient_record` first | Sequence | Submission cannot run without both prerequisites |
| `submit_claim` blocked if `read_phi` was called | Exfiltration block | PHI cannot be transmitted before compliance clearance |
| `export_report` blocked if `read_phi` was called | Exfiltration block | Same guard for report exports |
| `run_compliance_scan` clears the PHI taint | Compliance gate | Certifies minimum-necessary before proceeding |
| Claims above $5,000 escalate to human | Escalation | Human review on high-value claims |
| Global `enforcement_mode: block` | Hard enforcement | PHI exposure is not a shadow-mode scenario |

**Risk mitigated:** PHI over-disclosure (minimum-necessary violation), claim submission before eligibility is verified, high-value automated claim fraud.

---

## HIPAA context

The HIPAA Privacy Rule minimum-necessary standard (45 CFR §164.502(b)) requires that when
PHI is disclosed, covered entities must make reasonable efforts to limit the disclosure to
the minimum necessary to accomplish the intended purpose.

In automated agent workflows, this rule is violated when:
- The agent reads a full patient record but only needs one field for the claim
- The agent submits a claim immediately after reading PHI without verifying what it is including
- The agent exports a report containing PHI without a minimum-necessary review step

This policy enforces the review gate structurally: the agent **cannot** submit or export
while the session is PHI-tainted. It must run `run_compliance_scan` first.

**Important:** This policy is one technical control in a defence-in-depth programme.
It does not replace a formal HIPAA compliance assessment, BAA review, or security risk analysis.

---

## When to use each rule

### PHI taint (`taint.applies`)

Apply to every tool that reads individually identifiable health information: patient records,
diagnosis codes, treatment history, insurance member IDs, or any HL7/FHIR resources
that map to a real person.

```yaml
read_phi:
  taint:
    applies: true
    label: phi_accessed
```

### Exfiltration block (`never_after` + PHI taint)

Apply to every tool that sends data externally: claim submission, report export, email,
fax, API call to a payer. The block prevents any outbound action while the session is
carrying potentially over-broad PHI.

```yaml
submit_claim:
  sequence:
    never_after:
      - read_phi
```

### Compliance gate (`taint.clears`)

Apply to the single tool that performs the minimum-necessary review. This tool is the
only path to unblocking the exfiltration-blocked tools. It must be purpose-built to
actually verify the data scope — TrueBearing enforces that the tool was called, but the
compliance check itself is the tool's responsibility.

```yaml
run_compliance_scan:
  taint:
    clears: true
```

### High-value escalation (`escalate_when`)

Apply to submission tools where claim value correlates with fraud risk. Adjust the
threshold based on your organisation's manual review policy.

```yaml
escalate_when:
  argument_path: "$.claim_amount_usd"
  operator: ">"
  value: 5000
```

---

## Adoption guide

1. **Copy** `hipaa-phi-guard.policy.yaml` into your project directory.
2. **Rename tools** to match your actual tool names (e.g., `pull_ehr_record`, `send_837`).
3. **Add all PHI-reading tools** to the `taint.applies` pattern. If in doubt, taint it.
4. **Add all outbound tools** to the `never_after: [read_phi]` pattern.
5. **Lint:** `truebearing policy lint ./hipaa-phi-guard.policy.yaml`
6. **Explain:** `truebearing policy explain ./hipaa-phi-guard.policy.yaml`
7. **Register:** `truebearing agent register billing-agent --policy ./hipaa-phi-guard.policy.yaml`
8. **Serve:** `truebearing serve --upstream <your_mcp_url> --policy ./hipaa-phi-guard.policy.yaml`

**Do not run in shadow mode for PHI workflows.** Unlike fintech workflows where shadow mode
is a safe onboarding step, PHI disclosure violations occur the moment data is transmitted —
shadow mode would allow them. Start with `enforcement_mode: block` and use a test environment
to validate the policy before connecting to a production payer endpoint.

---

## Customisation examples

**Multiple PHI sources, one gate:**
```yaml
may_use:
  - read_ehr
  - read_lab_results
  - read_imaging_report
  - run_compliance_scan
  - submit_claim
  - check_escalation_status

tools:
  read_ehr:
    taint:
      applies: true
      label: phi_accessed
  read_lab_results:
    taint:
      applies: true
      label: phi_accessed
  read_imaging_report:
    taint:
      applies: true
      label: phi_accessed
  run_compliance_scan:
    taint:
      clears: true
  submit_claim:
    sequence:
      never_after:
        - read_ehr
        - read_lab_results
        - read_imaging_report
```

**Require eligibility before any PHI access:**
```yaml
read_phi:
  sequence:
    only_after:
      - verify_eligibility
```
