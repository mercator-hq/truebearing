# Policy Pack: Life Sciences — Regulatory Submission Guard

This policy pack enforces the mandatory review chain for automated FDA regulatory
submission workflows. It is designed for agents that have write access to FDA electronic
submission portals (ESG, eSubmitter, eCTD gateway) or internal submission management systems.

---

## Policies in this pack

### `regulatory-submission.policy.yaml`

**Pattern:** Mandatory review chain with post-amendment block and unconditional human escalation.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `submit_to_fda` only after `generate_draft`, `internal_review`, `legal_sign_off` | Sequence | Submission cannot run without the full review chain |
| `submit_to_fda` blocked if `amend_document` was called in this session | Amendment block | Any amendment invalidates the current session's review chain |
| Every `submit_to_fda` call escalates to human | Escalation | All FDA submissions require human sign-off regardless of automation |
| `submit_to_fda` at tool-level `block` | Enforcement override | Always hard-enforced; shadow mode is not safe for a live portal |

**Risk mitigated:** Premature submission without documented review, submission of amended documents without re-review, automated submission without human final approval.

---

## Regulatory context

**21 CFR Part 11** (Electronic Records; Electronic Signatures) requires that electronic
records used in FDA-regulated activities are trustworthy, reliable, and equivalent to paper
records. Requirements include:

- **§11.10(e)** — Use of secure, computer-generated, time-stamped audit trails to independently record the date and time of operator entries and actions that create, modify, or delete electronic records.
- **§11.10(a)** — Systems must be validated to ensure accuracy, reliability, consistent intended performance, and the ability to discern invalid or altered records.
- **§11.50** — Electronic signatures must be linked to their respective electronic records.

TrueBearing's Ed25519-signed audit records satisfy the tamper-evident audit trail requirement
in §11.10(e). The sequence guard ensures that every submission record shows an unbroken
chain from draft creation through internal review to legal sign-off — the documentation
chain required for FDA inspection.

**Applicable submission types:** IND, NDA, ANDA, BLA, 510(k), PMA, eCTD, eCOPP.

---

## When to use each rule

### `only_after` — Mandatory review chain

Use when a submission is irreversible and must be preceded by a documented review sequence.
The three steps represent the minimum defensible review chain for a regulatory submission.

```yaml
sequence:
  only_after:
    - generate_draft
    - internal_review
    - legal_sign_off
```

### `never_after` — Post-amendment submission block

Use when any modification to the submission document should invalidate the current review
chain and require a fresh session. This prevents submission of a version that the review
team has not seen.

```yaml
submit_to_fda:
  sequence:
    never_after:
      - amend_document
```

The block is permanent for the session. A new session must be started after any amendment,
and the full review chain must be completed in that new session before submission.

### Unconditional escalation (`escalate_when` + `matches: ".+"`)

Use when every call to a tool must trigger human review, regardless of arguments. Pass a
field that is always present in the tool's arguments and match any non-empty value.

```yaml
escalate_when:
  argument_path: "$.submission_id"
  operator: "matches"
  value: ".+"
```

### `taint.applies` — Amendment state signal

The `amend_document` taint provides an actionable error message while the session is in
the amended state. It is complementary to the `never_after` block (which is the primary
enforcement). Both mechanisms together provide a clear diagnostic trail.

```yaml
amend_document:
  taint:
    applies: true
    label: draft_amended
```

---

## Adoption guide

1. **Copy** `regulatory-submission.policy.yaml` into your project directory.
2. **Rename tools** to match your actual system tool names (e.g., `create_ectd_draft`, `submit_esg`).
3. **Add intermediate review steps** to `only_after` if your SOP requires more than three steps.
4. **Confirm `submission_id`** is a field your tool always passes (required for unconditional escalation).
5. **Lint:** `truebearing policy lint ./regulatory-submission.policy.yaml`
6. **Explain:** `truebearing policy explain ./regulatory-submission.policy.yaml`
7. **Register:** `truebearing agent register regulatory-agent --policy ./regulatory-submission.policy.yaml`
8. **Serve:** `truebearing serve --upstream <your_mcp_url> --policy ./regulatory-submission.policy.yaml`
9. **Observe:** run in `shadow` mode against a test/staging submission portal. Review:
   ```
   truebearing audit query --decision shadow_deny
   ```
10. **Enforce:** flip `enforcement_mode: shadow` → `enforcement_mode: block` before connecting to production.

---

## Customisation examples

**Extended review chain for NDA submissions:**
```yaml
submit_to_fda:
  sequence:
    only_after:
      - generate_draft
      - biostatistics_review   # add domain-specific review steps
      - clinical_review
      - regulatory_strategy    # regulatory strategy sign-off
      - internal_review
      - legal_sign_off
```

**Allow amendments before sign-off (remove post-amendment block):**

If your workflow requires amendments during the review process (before legal_sign_off),
remove the `never_after: [amend_document]` rule and instead add `legal_sign_off` as the
last `only_after` prerequisite. The sequence guard ensures sign-off is the final step:

```yaml
submit_to_fda:
  sequence:
    only_after:
      - generate_draft
      - internal_review
      - legal_sign_off   # must be the last step called before submission
    # never_after: [amend_document]  # removed — amendments allowed before sign-off
```

**Multiple submission types with different escalation thresholds:**
```yaml
submit_to_fda:
  escalate_when:
    argument_path: "$.submission_type"
    operator: "=="
    value: "NDA"  # escalate only NDA submissions; IND submissions auto-proceed
```
