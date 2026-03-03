# Policy Pack: Healthcare Billing — HIPAA Billing Guard

This policy pack enforces the HIPAA minimum-necessary standard and multi-step PHI access
authorisation for automated healthcare billing workflows. It extends the base HIPAA PHI
guard pattern with a `requires_prior_n` predicate that creates a documented, machine-verifiable
authorisation chain for every PHI access event.

It is designed for agents that access patient health records, verify insurance eligibility,
and submit claims to payers or clearinghouses.

---

## Policies in this pack

### `hipaa-billing-guard.policy.yaml`

**Pattern:** Multi-step PHI access authorisation with compliance gate before any outbound submission.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `read_phi` requires `verify_member_identity` + `confirm_treatment_relationship` first | Sequence | PHI cannot be accessed without documented authorisation chain |
| At least 1 `verify_member_identity` required before `read_phi` | Count guard | Prevents PHI access without identity verification |
| `submit_claim` requires `read_eligibility` first | Sequence | Submission cannot run without eligibility check |
| `submit_claim` blocked if `read_phi` was called | Exfiltration block | PHI cannot be transmitted before compliance clearance |
| `export_report` blocked if `read_phi` was called | Exfiltration block | Same guard for report exports |
| `run_compliance_scan` clears the PHI taint | Compliance gate | Certifies minimum-necessary before proceeding |
| Claims above $5,000 escalate to human | Escalation | Human review on high-value claims |
| Global `enforcement_mode: block` | Hard enforcement | PHI exposure is not a shadow-mode scenario |

**Risk mitigated:** PHI over-disclosure (§164.502(b) minimum-necessary violation), PHI access without documented authorisation chain (§164.312(a)(1)), incomplete audit trail (§164.312(b)), high-value automated claim fraud.

---

## HIPAA context

### The two HIPAA risks this policy addresses

**Risk 1 — Minimum-necessary violation (§164.502(b)):** The agent reads more PHI than is
needed for the claim and then submits the full PHI set to the clearinghouse. This policy
enforces a compliance scan before any submission — the agent must certify data scope before
any PHI leaves the system.

**Risk 2 — Insufficient access control audit trail (§164.312):** The agent accesses PHI
without a documented, multi-step authorisation chain. A single logged access event does not
satisfy the §164.312(a)(1) requirement that access is "granted only to those who have been
granted access rights." This policy requires two distinct authorisation steps before PHI
access and records each step in the tamper-evident audit log.

### What the TrueBearing audit log provides

Each TrueBearing audit record is signed with Ed25519 and includes the tool name, session
ID, agent identity, decision, and argument hash. The sequence of records for a session
demonstrates that:
1. `verify_member_identity` was called and allowed
2. `confirm_treatment_relationship` was called and allowed
3. `read_phi` was then called and allowed (the authorisation chain is complete)
4. `run_compliance_scan` was called and allowed (minimum-necessary certified)
5. `submit_claim` was called and allowed (no PHI taint at time of submission)

This chain is the electronic audit trail required by §164.312(b).

**Important:** This policy is one technical control in a defence-in-depth programme.
It does not replace a formal HIPAA risk analysis, BAA review, or security risk management
programme. Consult your Privacy Officer and Security Officer.

---

## When to use each rule

### Multi-step PHI access authorisation (`only_after` + `requires_prior_n`)

Use when PHI access must be preceded by a documented authorisation chain. The combination
of `only_after` (both steps must have run) and `requires_prior_n` (at least N times)
protects against batch workflows where one authorisation step ran for a different member.

```yaml
read_phi:
  sequence:
    only_after:
      - verify_member_identity
      - confirm_treatment_relationship
    requires_prior_n:
      tool: verify_member_identity
      count: 1
```

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
fax, API call to a payer or clearinghouse. Block all outbound actions while the session
carries potentially over-broad PHI.

```yaml
submit_claim:
  sequence:
    never_after:
      - read_phi
```

### Compliance gate (`taint.clears`)

Apply to the single tool that performs the minimum-necessary review and certifies data
scope. This is the only path to unblocking the exfiltration-blocked tools.

```yaml
run_compliance_scan:
  taint:
    clears: true
```

---

## Adoption guide

1. **Copy** `hipaa-billing-guard.policy.yaml` into your project directory.
2. **Rename tools** to match your actual tool names (e.g., `pull_ehr_record`, `send_837_claim`).
3. **Add all PHI-reading tools** to the `taint.applies` pattern. If in doubt, taint it.
4. **Add all outbound tools** to the `never_after: [read_phi]` pattern.
5. **Increase `requires_prior_n` count** if your workflow requires multiple identity checks.
6. **Lint:** `truebearing policy lint ./hipaa-billing-guard.policy.yaml`
7. **Explain:** `truebearing policy explain ./hipaa-billing-guard.policy.yaml`
8. **Register:** `truebearing agent register billing-agent --policy ./hipaa-billing-guard.policy.yaml`
9. **Serve:** `truebearing serve --upstream <your_mcp_url> --policy ./hipaa-billing-guard.policy.yaml`

**Do not run in shadow mode for PHI workflows.** PHI disclosure violations occur the
moment data is transmitted — shadow mode would allow them. Start with `enforcement_mode:
block` and use a test environment to validate the policy before connecting to a production
payer endpoint.

---

## Customisation examples

**Multiple PHI sources, each requiring authorisation:**
```yaml
may_use:
  - verify_member_identity
  - confirm_treatment_relationship
  - read_ehr
  - read_lab_results
  - read_imaging_report
  - run_compliance_scan
  - submit_claim
  - check_escalation_status

tools:
  read_ehr:
    sequence:
      only_after:
        - verify_member_identity
        - confirm_treatment_relationship
    taint:
      applies: true
      label: phi_accessed
  read_lab_results:
    sequence:
      only_after:
        - verify_member_identity
        - confirm_treatment_relationship
    taint:
      applies: true
      label: phi_accessed
  submit_claim:
    sequence:
      never_after:
        - read_ehr
        - read_lab_results
        - read_imaging_report
```

**Require two identity verification calls (dual-factor):**
```yaml
read_phi:
  sequence:
    requires_prior_n:
      tool: verify_member_identity
      count: 2  # two distinct identity checks before PHI access
```
