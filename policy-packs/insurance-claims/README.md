# Policy Pack: Insurance Claims — Claims Processing Guard

This policy pack enforces the sequential approval pattern for automated insurance claims
processing. It is designed for agents that have write access to a claims management system,
adjudication engine, or payment trigger.

---

## Policies in this pack

### `claims-processing.policy.yaml`

**Pattern:** Sequential approval guard with PII taint and high-value escalation.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `approve_claim` only after `verify_policy`, `assess_damage`, `check_fraud_signals` | Sequence | Approval cannot run without all three prerequisites |
| `approve_claim` blocked if `read_claimant_pii` was called | Taint guard | PII taint blocks approval until compliance check clears it |
| `run_compliance_check` clears the PII taint | Compliance gate | Minimum-necessary certification before proceeding |
| At least 1 `check_fraud_signals` call required | Count guard | Prevents approval on unscreened claim |
| Claims above $25,000 escalate to human | Escalation | Human adjudicator on large-loss claims |
| `approve_claim` at tool-level `block` | Enforcement override | Always hard-enforced even in global shadow mode |

**Risk mitigated:** Fraudulent claim approval, approval without policy verification, PII over-disclosure, automated large-loss decisions without human oversight.

---

## Regulatory context

Most US state insurance regulations require documented, sequential claim handling. TrueBearing's
audit log provides Ed25519-signed, tamper-evident evidence that every approved claim passed
through the required sequence — without requiring custom audit infrastructure.

Key regulatory references:
- **NY Insurance Law §2601** — unfair claims settlement practices: requires prompt investigation based on all available information
- **CA Insurance Code §790.03** — requires objective claim investigation completed as promptly as possible
- **NAIC Unfair Claims Settlement Practices Model Act** — baseline adopted by most states

---

## When to use each rule

### `only_after` — Sequential prerequisite chain

Use when an approval is irreversible and must be preceded by a defined verification set.
The three prerequisites in this pack form the minimum defensible documentation chain.

```yaml
sequence:
  only_after:
    - verify_policy
    - assess_damage
    - check_fraud_signals
```

### `never_after` + `taint.applies` — PII isolation

Use when your agent accesses personally identifiable information. The PII taint ensures
that the agent cannot approve a claim in the same session where it read raw PII without
first running a compliance check to certify minimum-necessary use.

```yaml
read_claimant_pii:
  taint:
    applies: true
    label: claimant_pii_accessed

approve_claim:
  sequence:
    never_after:
      - read_claimant_pii
```

### `taint.clears` — Compliance gate

The compliance check is the only tool that can clear the PII taint. It forces the agent
to explicitly certify data scope before any approval or transmission occurs.

```yaml
run_compliance_check:
  taint:
    clears: true
```

### `requires_prior_n` — Minimum fraud check count

Use when a prerequisite must have run a minimum number of times before the guarded action
is permitted. For batch claims workflows where one fraud check per claim is required:

```yaml
requires_prior_n:
  tool: check_fraud_signals
  count: 1
```

### `escalate_when` — Large-loss human adjudication

Use when the risk of an automated decision scales with claim value. The agent is paused
and the operator is notified. The agent polls `check_escalation_status` until the
operator approves or rejects.

```yaml
escalate_when:
  argument_path: "$.claim_amount_usd"
  operator: ">"
  value: 25000
```

---

## Adoption guide

1. **Copy** `claims-processing.policy.yaml` into your project directory.
2. **Rename tools** to match your actual tool names.
3. **Adjust thresholds** — escalation value, budget limits, session duration.
4. **Lint:** `truebearing policy lint ./claims-processing.policy.yaml`
5. **Explain:** `truebearing policy explain ./claims-processing.policy.yaml`
6. **Register:** `truebearing agent register claims-agent --policy ./claims-processing.policy.yaml`
7. **Serve:** `truebearing serve --upstream <your_mcp_url> --policy ./claims-processing.policy.yaml`
8. **Observe:** run in `shadow` mode for 1–2 weeks. Review violations:
   ```
   truebearing audit query --decision shadow_deny
   ```
9. **Enforce:** flip `enforcement_mode: shadow` → `enforcement_mode: block` and redeploy.

---

## Customisation examples

**Multiple fraud-check methods required before approval:**
```yaml
approve_claim:
  sequence:
    only_after:
      - verify_policy
      - assess_damage
      - check_fraud_signals
      - verify_bank_account  # add additional verification steps as needed
```

**Batch claims — require N fraud checks before any approval:**
```yaml
approve_claim:
  sequence:
    requires_prior_n:
      tool: check_fraud_signals
      count: 3  # must screen 3 claims before any single approval in the session
```

**Lower escalation threshold for high-fraud verticals:**
```yaml
approve_claim:
  escalate_when:
    argument_path: "$.claim_amount_usd"
    operator: ">"
    value: 5000
```
