# Policy Pack: Fintech — Payments Safety

This policy pack enforces the sequential approval pattern for financial payment automation.
It is designed for agents that have write access to a payment processor, ERP, or banking API.

---

## Policies in this pack

### `payments-safety.policy.yaml`

**Pattern:** Sequential approval guard with prompt-injection isolation and high-value escalation.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `execute_payment` only after `verify_invoice` and `manager_approval` | Sequence | Payment cannot run without both prerequisites |
| `execute_payment` blocked if `read_external_email` was called | Taint guard | Isolates session from prompt-injection via email |
| `run_compliance_scan` clears the email taint | Compliance gate | Explicit clearance before proceeding after external content |
| Payments above $10,000 escalate to human | Escalation | Human sign-off on large transfers |
| At least 1 `verify_invoice` call required | Count guard | Prevents payment on unverified invoice |
| `execute_payment` at tool-level `block` | Enforcement override | Always enforced even in global shadow mode |

**Risk mitigated:** Double-payment, payment on unverified invoice, prompt-injection via external email, unauthorised high-value transfer.

---

## When to use each rule

### `only_after` — Sequential prerequisite chain

Use when an action is irreversible and must be preceded by a defined set of steps.
Any tool that moves money, deletes data, or sends external communications should have
an `only_after` guard specifying what must be confirmed first.

```yaml
sequence:
  only_after:
    - verify_invoice
    - manager_approval
```

### `never_after` + `taint.applies` — Prompt-injection isolation

Use when your agent ingests content from untrusted external sources (email, webhooks,
third-party feeds). Reading untrusted content taints the session. Any high-risk action
protected by `never_after` is blocked until a compliance gate clears the taint.

```yaml
read_external_email:
  taint:
    applies: true
    label: external_email_read

execute_payment:
  sequence:
    never_after:
      - read_external_email
```

### `taint.clears` — Compliance gate

Use to explicitly clear a taint after a verification or review step. The gate tool
is always called by the agent before the blocked tool is retried.

```yaml
run_compliance_scan:
  taint:
    clears: true
```

### `escalate_when` — High-value human escalation

Use when the risk of an automated decision scales with argument values (amount, count,
impact). The agent is paused and the operator is notified. The agent polls
`check_escalation_status` until the operator approves or rejects.

```yaml
escalate_when:
  argument_path: "$.amount_usd"
  operator: ">"
  value: 10000
```

### `requires_prior_n` — Minimum call count

Use when a prerequisite must have run a minimum number of times before the guarded tool
is permitted. Common in batch workflows where one verification per item is required.

```yaml
requires_prior_n:
  tool: verify_invoice
  count: 1
```

### Tool-level `enforcement_mode: block`

Use on any single tool that executes an irreversible real-world action, even when the
global policy is `shadow`. Shadow mode is only appropriate for observation — never for
the execution step itself.

```yaml
execute_payment:
  enforcement_mode: block
```

---

## Adoption guide

1. **Copy** `payments-safety.policy.yaml` into your project directory.
2. **Rename tools** to match your actual tool names.
3. **Adjust thresholds** — escalation value, budget limits, session duration.
4. **Lint:** `truebearing policy lint ./payments-safety.policy.yaml`
5. **Explain:** `truebearing policy explain ./payments-safety.policy.yaml`
6. **Register:** `truebearing agent register payments-agent --policy ./payments-safety.policy.yaml`
7. **Serve:** `truebearing serve --upstream <your_mcp_url> --policy ./payments-safety.policy.yaml`
8. **Observe:** run in `shadow` mode for 1–2 weeks. Review violations:
   ```
   truebearing audit query --decision shadow_deny
   ```
9. **Enforce:** flip `enforcement_mode: shadow` → `enforcement_mode: block` and redeploy.

---

## Customisation examples

**Multiple approvers required:**
```yaml
execute_payment:
  sequence:
    only_after:
      - first_approver
      - second_approver  # add as many as your dual-control policy requires
```

**Batch payment — N invoices verified before paying:**
```yaml
execute_payment:
  sequence:
    requires_prior_n:
      tool: verify_invoice
      count: 5  # must verify 5 invoices before executing any payment in the session
```

**Tighter budget for high-frequency agents:**
```yaml
budget:
  max_tool_calls: 20
  max_cost_usd: 0.50
```
