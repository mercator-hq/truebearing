# Policy Pack: Legal Ops — Privileged Document Guard

This policy pack enforces attorney-client privilege protection for automated legal document
review workflows. It is designed for agents that have access to matter management systems,
document repositories, and external communication tools.

---

## Policies in this pack

### `privileged-document-guard.policy.yaml`

**Pattern:** Privilege taint with compliance gate before external transmission, plus active-litigation escalation.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `transmit_to_external` blocked if `read_privileged_document` was called | Taint guard | Privileged content cannot be sent externally without privilege review |
| `generate_summary` blocked if `read_privileged_document` was called | Contamination guard | AI-generated summaries may reflect privileged content |
| `run_privilege_review` clears the privilege taint | Compliance gate | Attorney clearance before any outbound action |
| `transmit_to_external` on `active_litigation` matters escalates | Escalation | Human attorney review for live litigation matters |
| `transmit_to_external` at tool-level `block` | Enforcement override | Always hard-enforced even in global shadow mode |

**Risk mitigated:** Inadvertent privilege waiver, transmission of attorney work product without authorisation, AI-generated summary exposure during active litigation.

---

## Privilege context

Attorney-client privilege protects confidential communications made for the purpose of
obtaining or providing legal advice. Privilege is waived by **voluntary disclosure** to
third parties — including by an autonomous agent acting on behalf of the client.

In automated workflows, privilege is at risk when:
- The agent reads a privileged document and then routes content to an external email or API
- The agent generates a summary of a privileged document and transmits it
- The agent routes matter communications to opposing counsel or third parties during litigation

This policy enforces the review gate structurally: the agent **cannot** transmit externally
or generate summaries while the session has read privileged content. It must run
`run_privilege_review` first.

**Important:** This policy is one technical control in a defence-in-depth programme.
It does not replace attorney supervision, a formal privilege log, or a litigation hold
programme. Consult your General Counsel.

---

## When to use each rule

### Privilege taint (`taint.applies`)

Apply to every tool that reads documents marked as privileged in your matter management
system: attorney-client communications, work product, legal strategy memos, draft pleadings,
or any document withheld from discovery on privilege grounds.

```yaml
read_privileged_document:
  taint:
    applies: true
    label: privileged_document_accessed
```

### Exfiltration block (`never_after` + privilege taint)

Apply to every tool that sends content externally: email, API call to opposing counsel,
document portal upload, Slack message, or any outbound communication channel.

```yaml
transmit_to_external:
  sequence:
    never_after:
      - read_privileged_document
```

Also apply to AI summary generation — a summary can reproduce or reflect privileged
content even if the source document is not directly attached.

```yaml
generate_summary:
  sequence:
    never_after:
      - read_privileged_document
```

### Privilege review gate (`taint.clears`)

Apply to the single tool that performs the privilege assessment. This tool is the only
path to unblocking transmission after privileged content was read.

```yaml
run_privilege_review:
  taint:
    clears: true
```

### Active litigation escalation (`escalate_when`)

Apply to transmission tools for matters with active litigation status. The matter status
should be a field in the tool's arguments — adjust the field name to match your system.

```yaml
escalate_when:
  argument_path: "$.matter_status"
  operator: "=="
  value: "active_litigation"
```

---

## Adoption guide

1. **Copy** `privileged-document-guard.policy.yaml` into your project directory.
2. **Rename tools** to match your actual tool names.
3. **Identify all outbound tools** and add `never_after: [read_privileged_document]` to each.
4. **Confirm your matter status field name** matches the `escalate_when.argument_path`.
5. **Lint:** `truebearing policy lint ./privileged-document-guard.policy.yaml`
6. **Explain:** `truebearing policy explain ./privileged-document-guard.policy.yaml`
7. **Register:** `truebearing agent register legal-ops-agent --policy ./privileged-document-guard.policy.yaml`
8. **Observe:** run in `shadow` mode. Review violations:
   ```
   truebearing audit query --decision shadow_deny
   ```
9. **Enforce:** flip `enforcement_mode: shadow` → `enforcement_mode: block` on transmission tools.

---

## Customisation examples

**Multiple transmission channels — block all while tainted:**
```yaml
may_use:
  - send_email
  - upload_to_portal
  - post_to_slack
  - run_privilege_review
  - read_privileged_document
  - check_escalation_status

tools:
  send_email:
    sequence:
      never_after:
        - read_privileged_document
  upload_to_portal:
    sequence:
      never_after:
        - read_privileged_document
  post_to_slack:
    sequence:
      never_after:
        - read_privileged_document
```

**Escalate all transmissions, not just active litigation:**
```yaml
transmit_to_external:
  escalate_when:
    argument_path: "$.recipient_type"
    operator: "=="
    value: "external"
```
