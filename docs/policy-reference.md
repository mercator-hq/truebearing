# TrueBearing Policy Reference

This document is the authoritative reference for the TrueBearing policy DSL. It covers
every field, predicate, and configuration option available in a policy YAML file. A reader
with no prior TrueBearing knowledge should be able to author a valid, production-ready
policy using this document alone.

**Related documents:**
- [docs/demo-script.md](demo-script.md) — walkthrough of a complete onboarding session
- [docs/mvp-plan.md](mvp-plan.md) — architecture decisions and DSL rationale

---

## Table of Contents

1. [Top-Level Fields](#1-top-level-fields)
2. [Session Configuration](#2-session-configuration)
3. [Budget Configuration](#3-budget-configuration)
4. [Tool Whitelist — `may_use`](#4-tool-whitelist--may_use)
5. [Per-Tool Policies — `tools`](#5-per-tool-policies--tools)
   - [Enforcement Mode Override](#51-enforcement-mode-override)
   - [Sequence Predicates](#52-sequence-predicates)
   - [Taint System](#53-taint-system)
   - [Escalation Rules](#54-escalation-rules)
   - [Content Guards — `never_when`](#55-content-guards--never_when)
6. [Escalation Notification — `escalation`](#6-escalation-notification--escalation)
7. [Reserved Tool Names](#7-reserved-tool-names)
8. [Enforcement Mode Hierarchy](#8-enforcement-mode-hierarchy)
9. [Shadow Mode Onboarding Workflow](#9-shadow-mode-onboarding-workflow)
10. [Linter Rules Reference — L001–L019](#10-linter-rules-reference--l001l019)
11. [Complete Policy Example](#11-complete-policy-example)

---

## 1. Top-Level Fields

A policy file is a YAML document. The top-level keys are:

| Field | Type | Required | Default |
|---|---|---|---|
| `version` | `string` | Yes | — |
| `agent` | `string` | Yes | — |
| `enforcement_mode` | `string` | No | `shadow` (linter warns via L005) |
| `may_use` | `[]string` | Yes | — (linter errors via L001) |
| `session` | object | No | see §2 |
| `budget` | object | No | no limits (linter warns via L006) |
| `escalation` | object | No | stdout only |
| `tools` | map | No | `{}` (no per-tool rules) |

### `version`

The schema version of the policy file. Currently the only valid value is `"1"`. This field
is reserved for forward compatibility — future TrueBearing versions may introduce breaking
DSL changes under a new version number.

```yaml
version: "1"
agent: my-agent
enforcement_mode: block
may_use:
  - read_record
  - submit_record
budget:
  max_tool_calls: 50
session:
  max_history: 500
```

### `agent`

The logical name for the agent this policy governs. Stored in `sessions.agent_name` and
shown in audit records, `truebearing session list`, and `truebearing audit query` output.
Does not need to match any system identifier. Use kebab-case describing the agent's
purpose.

```yaml
agent: payments-agent      # financial workflow agent
# or:
agent: document-processor  # document review and submission agent
```

### `enforcement_mode`

Controls how the pipeline responds to policy violations globally.

| Value | Behaviour |
|---|---|
| `shadow` | Violations are **logged** as `shadow_deny` in the audit log. The tool call is **allowed through** to the upstream. Use during onboarding and tuning. |
| `block` | Violations produce a `deny` decision. The tool call is **blocked**. The upstream never receives the request. Use in production. |

When `enforcement_mode` is omitted, the pipeline defaults to `shadow` and the linter emits
`L005` (WARNING). Set it explicitly so the effective mode is unambiguous.

The linter also emits `L009` (INFO) as a reminder whenever `shadow` is active — this is
not an error, it is an intentional prompt to flip to `block` when ready.

Per-tool `enforcement_mode` overrides this global value. See
[§8 Enforcement Mode Hierarchy](#8-enforcement-mode-hierarchy).

---

## 2. Session Configuration

The `session:` block controls per-session lifetime and history limits.

```yaml
session:
  max_history: 1000
  max_duration_seconds: 3600
```

### `session.max_history`

| | |
|---|---|
| **Type** | `int` |
| **Default** | No limit configured (linter warns via L007) |
| **Minimum** | 1 |

The hard cap on the number of tool-call events stored per session. When reached, the
session is **permanently exhausted** — any new tool call returns an error demanding a new
session be started. Events are never evicted or overwritten. There is no ring buffer.

Setting this explicitly forces an operator to consider session lifecycle and storage
requirements. Recommended value: `500`–`1000` for most workflows.

```yaml
session:
  max_history: 500   # hard cap; session is dead when reached — start a new one
```

### `session.max_duration_seconds`

| | |
|---|---|
| **Type** | `int` |
| **Default** | `0` (no duration limit) |

Optional wall-clock lifetime for a session, in seconds. When the session's age exceeds
this value, subsequent tool calls are rejected. Zero means no limit is applied.

```yaml
session:
  max_history: 500
  max_duration_seconds: 7200   # 2-hour window; adjust to match your workflow cadence
```

---

## 3. Budget Configuration

The `budget:` block sets hard resource ceilings per session. At least one limit is
recommended — the linter warns (L006) if neither `max_tool_calls` nor `max_cost_usd` is
set.

```yaml
budget:
  max_tool_calls: 100
  max_cost_usd: 5.00
```

### `budget.max_tool_calls`

| | |
|---|---|
| **Type** | `int` |
| **Default** | `0` (no limit; linter warns via L006) |

The maximum number of tool calls allowed in a single session. When
`session.tool_call_count >= max_tool_calls`, the Budget evaluator denies the next call
with a `budget_exceeded` reason. This is the primary loop-detection mechanism for runaway
agents.

### `budget.max_cost_usd`

| | |
|---|---|
| **Type** | `float64` (USD) |
| **Default** | `0.0` (no limit; linter warns via L006) |

The maximum estimated cost allowed per session in US dollars. The proxy uses a flat
`$0.001` per-call estimate for MVP. When `session.estimated_cost_usd >= max_cost_usd`,
the next call is denied.

---

## 4. Tool Whitelist — `may_use`

| | |
|---|---|
| **Type** | `[]string` |
| **Required** | Yes (linter errors via L001 if missing or empty) |

The exhaustive whitelist of tool names this agent is permitted to call. Any tool **not**
in this list is denied before any other check runs, regardless of `enforcement_mode`.
The `may_use` check is the first and fastest stage in the evaluation pipeline.

```yaml
may_use:
  - read_record
  - validate_record
  - reviewer_approval
  - submit_record
  - run_clearance_check
  - read_external_content
  - check_escalation_status   # always include if any tool uses escalate_when
```

Rules enforced by the linter:
- Tools defined in `tools:` must also appear in `may_use` (L002).
- `only_after` predicates must only reference tools in `may_use` (L003).
- `never_after` predicates must only reference tools in `may_use` (L004).

Tools in `may_use` but not in `tools:` have no sequence, taint, or escalation rules —
they are allowed subject only to the budget and whitelist checks.

---

## 5. Per-Tool Policies — `tools`

The `tools:` block is a map from tool name to a set of constraints. Every key in `tools:`
must also appear in `may_use` (linter error L002 otherwise). A tool in `may_use` but
absent from `tools:` carries no additional rules.

```yaml
tools:
  submit_record:              # must also be in may_use
    enforcement_mode: block
    sequence:
      only_after:
        - validate_record
        - reviewer_approval
      never_after:
        - read_external_content
      requires_prior_n:
        tool: validate_record
        count: 1
    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 50000

  read_external_content:
    taint:
      applies: true
      label: external_content_read

  run_clearance_check:
    taint:
      clears: true

  read_record: {}             # no additional rules; entry is optional
```

---

### 5.1 Enforcement Mode Override

**Field:** `tools.<name>.enforcement_mode`

| | |
|---|---|
| **Type** | `string` |
| **Valid values** | `shadow`, `block` |
| **Default** | Inherits global `enforcement_mode` |

Overrides the global `enforcement_mode` for a single tool. The most common use case: run
the entire policy in `shadow` for safe onboarding while enforcing hard blocks on the
single highest-risk tool from day one.

```yaml
enforcement_mode: shadow     # global: log violations, allow through

tools:
  execute_payment:
    enforcement_mode: block  # this tool: always deny violations, even in global shadow
```

See [§8 Enforcement Mode Hierarchy](#8-enforcement-mode-hierarchy) for the full resolution
table.

---

### 5.2 Sequence Predicates

Sequence predicates live under `tools.<name>.sequence`. They evaluate the **session
history** — the ordered log of prior allowed and shadow-allowed tool calls — to determine
whether the current call is permitted.

**Design:** All three predicates run even when one fails. The deny reason includes all
violations so operators see every problem in a single request rather than discovering them
one at a time over multiple retries.

#### `sequence.only_after`

| | |
|---|---|
| **Type** | `[]string` |
| **Default** | `[]` (no prerequisites) |
| **Linter check** | L003 — referenced tools must be in `may_use` |

All tools listed here must appear in the session history (with decision `allow` or
`shadow_deny`) before this tool may be called. The check is satisfied when every listed
tool has appeared **at least once** in the session, in any order among themselves, before
the current call.

```yaml
tools:
  submit_record:
    sequence:
      only_after:
        - validate_record    # validation must have completed first
        - reviewer_approval  # a human or system approval must be on record
```

**Example denial:** Session history is `[validate_record]`. Agent calls `submit_record`.
Result: denied with reason
`sequence.only_after: reviewer_approval not satisfied (not in session history)`.

#### `sequence.never_after`

| | |
|---|---|
| **Type** | `[]string` |
| **Default** | `[]` (no blocking predecessors) |
| **Linter check** | L004 — referenced tools must be in `may_use` |

If **any** tool listed here appears anywhere in the session history, this tool is denied.
Applied regardless of when in the session the listed tool was called.

This predicate is the primary mechanism for **prompt-injection isolation**: if the agent
reads untrusted external content, block sensitive actions until a clearance step runs.

```yaml
tools:
  submit_record:
    sequence:
      never_after:
        - read_external_content   # block if untrusted content entered the session
```

**Example denial:** Session history includes `read_external_content`. Agent calls
`submit_record`. Result: denied regardless of what else has happened this session.

#### `sequence.requires_prior_n`

| | |
|---|---|
| **Type** | `{tool: string, count: int}` |
| **Default** | `null` (not configured) |
| **Linter check** | L010 — `count` must be ≥ 1 |

Requires a specific tool to have been called at least `count` times before this tool may
run. Useful for batch workflows where multiple rounds of validation must precede a single
action.

```yaml
tools:
  submit_batch:
    sequence:
      requires_prior_n:
        tool: validate_item
        count: 3   # at least 3 validations required before the batch submits
```

`count` must be a positive integer (≥ 1). Zero or negative values are an error (L010)
because they make the predicate trivially satisfied or semantically undefined.

---

### 5.3 Taint System

Taint is a session-wide boolean flag that signals the session context may have been
exposed to untrusted external input (prompt-injection payloads, unverified data sources,
etc.). It is set by `taint.applies` and cleared by `taint.clears`.

Taint interacts with `sequence.never_after`: if a taint-applying tool appears in a
target tool's `never_after` list, the target is blocked while that tool is in the session
history — which is exactly when the session is tainted.

**Lifecycle:**
1. Agent calls a taint-applying tool → call is **allowed** → taint flag set **after** the
   call completes.
2. Agent calls a tool with the taint-applying tool in its `never_after` → **denied**.
3. Agent calls a taint-clearing tool → call is **allowed** → taint flag cleared **after**
   the call completes.
4. The blocked tool from step 2 may now be called.

#### `taint.applies`

| | |
|---|---|
| **Type** | `bool` |
| **Default** | `false` |

When `true`, calling this tool sets the session taint flag. The call that triggers the
taint is itself allowed.

```yaml
tools:
  read_external_content:
    taint:
      applies: true
      label: external_content_read
```

#### `taint.label`

| | |
|---|---|
| **Type** | `string` |
| **Default** | `""` |

A human-readable name for the taint source. Appears in audit records,
`truebearing session inspect` output, and `truebearing policy explain` output. Has no
effect on evaluation logic. Use a descriptive, snake_case identifier.

#### `taint.clears`

| | |
|---|---|
| **Type** | `bool` |
| **Default** | `false` |

When `true`, calling this tool removes the taint flag if it is set. The call that clears
the taint is itself allowed. Once cleared, tools previously blocked by `never_after` on
the taint-applying tool become callable again.

```yaml
tools:
  run_clearance_check:
    taint:
      clears: true
```

**Warning — L011:** If at least one tool has `taint.applies: true` but no tool has
`taint.clears: true`, the session can never be untainted. Any tool blocked by the taint
will be permanently blocked for the rest of the session. The linter warns about this
configuration.

---

### 5.4 Escalation Rules

`escalate_when` triggers a human escalation when a tool is called with arguments that
satisfy the defined condition. The tool call **does not proceed** until a human approves
it via `truebearing escalation approve <id>`.

The proxy returns a synthetic success response to the agent containing the escalation ID
and instructions to poll `check_escalation_status`. This keeps the agent's run loop alive
without holding the HTTP connection open (LLM clients have 60–120 second timeouts; human
reviewers take minutes to hours).

#### `escalate_when.argument_path`

| | |
|---|---|
| **Type** | `string` (JSONPath expression) |
| **Required when** | `escalate_when` is configured |

A JSONPath expression applied to the tool call's JSON arguments to extract the value to
compare against `escalate_when.value`. Uses `$` root notation.

Examples:
- `$.amount_usd` — top-level `amount_usd` field
- `$.order.total` — nested field

```yaml
tools:
  execute_payment:
    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 10000
```

#### `escalate_when.operator`

| | |
|---|---|
| **Type** | `string` |
| **Required when** | `escalate_when` is configured |
| **Valid values** | `>`, `<`, `>=`, `<=`, `==`, `!=`, `contains`, `matches` |
| **Linter check** | L012 — unrecognised operator causes fail-closed on every call |

The comparison applied between the extracted argument value and `escalate_when.value`.

| Operator | Applies to | Escalates when |
|---|---|---|
| `>` | Numeric | extracted > value |
| `<` | Numeric | extracted < value |
| `>=` | Numeric | extracted ≥ value |
| `<=` | Numeric | extracted ≤ value |
| `==` | Numeric, String | extracted equals value |
| `!=` | Numeric, String | extracted does not equal value |
| `contains` | String | extracted string contains value string |
| `matches` | String | extracted string matches value as a regular expression |

#### `escalate_when.value`

| | |
|---|---|
| **Type** | `number` or `string` |
| **Required when** | `escalate_when` is configured |

The threshold to compare against the extracted argument. Use a number for numeric
operators (`>`, `<`, `>=`, `<=`) and a string for string operators (`contains`, `matches`).

**Previously-approved escalations:** If an escalation for this session, tool, and
argument hash has already been approved by an operator, the pipeline allows the call
without triggering a new escalation. The agent does not need to modify its behaviour.

---

### 5.5 Content Guards — `never_when`

`never_when` blocks a tool call when the tool's arguments satisfy one or more content-based
predicates. It inspects the **argument values at call time** — unlike sequence predicates, which
inspect session history. Use it to block calls whose payload violates a policy rule regardless of
what happened before.

#### `never_when` predicates

Each predicate in the list is an object with three fields:

| Field | Type | Description |
|---|---|---|
| `argument` | `string` | The key in the tool call's JSON arguments to inspect |
| `operator` | `string` | The comparison to apply — see operators table below |
| `value` | `string` | The comparand string |

**Supported operators:**

| Operator | Fires (denies) when |
|---|---|
| `is_external` | The argument string does **not** end with `value` (use `value` as the trusted-domain suffix, e.g. `@acme.com`) |
| `contains_pattern` | The argument string matches `value` as a Go regexp. Leading/trailing `/` delimiters (Perl/JS style) are stripped before compilation. |
| `equals` | The argument string equals `value` exactly |
| `not_equals` | The argument string does not equal `value` |

**Example — block emails sent outside the company domain:**

```yaml
tools:
  send_email:
    never_when_match: any
    never_when:
      - argument: recipient
        operator: is_external
        value: "@acme.com"
```

**Example — block on any of several forbidden patterns (OR logic):**

```yaml
tools:
  draft_message:
    never_when_match: any   # block if ANY predicate matches (default)
    never_when:
      - argument: body
        operator: contains_pattern
        value: "/ssn[:\s]\d{3}-\d{2}-\d{4}/"
      - argument: body
        operator: contains_pattern
        value: "/credit.card.number/i"
```

**Example — block only when BOTH conditions are true (AND logic):**

```yaml
tools:
  send_email:
    never_when_match: all   # block only when ALL predicates match simultaneously
    never_when:
      - argument: recipient
        operator: is_external
        value: "@acme.com"
      - argument: body
        operator: contains_pattern
        value: "/contract|agreement|nda/i"
```

#### `never_when_match`

Controls how multiple predicates in `never_when` are combined.

| Value | Behaviour |
|---|---|
| `any` | Block when **any single** predicate matches (OR logic). Default when `never_when_match` is absent. |
| `all` | Block only when **every** predicate matches simultaneously (AND logic). |

**Always set `never_when_match` explicitly when you have more than one predicate.** Omitting it
with multiple predicates triggers lint warning L019 and makes intent ambiguous to future readers.

**Denial response when triggered:**

```json
{
  "error": {
    "code": -32603,
    "message": "tool call denied",
    "data": {
      "decision": "deny",
      "reason": "content.never_when: predicate 0 matched (argument \"recipient\", operator \"is_external\")",
      "rule_id": "content.never_when"
    }
  }
}
```

---

## 6. Escalation Notification — `escalation`

The top-level `escalation:` block configures how TrueBearing notifies operators when an
escalation event is created. Without it, escalation events are written to stdout only.

The linter warns (L008) when a tool has `escalate_when` but no `webhook_url` is
configured, because stdout is rarely monitored in production.

### `escalation.webhook_url`

| | |
|---|---|
| **Type** | `string` (HTTP URL) |
| **Default** | `""` (stdout only) |

An HTTP endpoint to POST escalation notifications to. The payload is a JSON object
containing the escalation ID, tool name, session ID, and the arguments hash. Use this to
integrate with Slack, PagerDuty, or any webhook-compatible alerting system.

```yaml
escalation:
  webhook_url: "https://hooks.example.com/services/your-escalation-handler"
```

After receiving the webhook, operators approve or reject via the CLI:

```sh
truebearing escalation approve <escalation-id> --note "Verified with approver"
truebearing escalation reject  <escalation-id> --reason "Exceeds daily limit"
```

---

## 7. Reserved Tool Names

These tool names are synthesised by TrueBearing. Do not define them in `may_use` or
`tools:` with custom rules — TrueBearing handles them internally.

| Tool Name | Purpose |
|---|---|
| `check_escalation_status` | Injected into `tools/list` responses. Agents call this to poll whether a pending escalation was approved or rejected. Never forwarded to the upstream MCP server. |

Always include `check_escalation_status` in `may_use` when any tool has an `escalate_when`
rule. Without it, the agent cannot poll for the escalation decision and will receive a
whitelist denial instead.

---

## 8. Enforcement Mode Hierarchy

The effective enforcement mode for a given tool call is resolved by combining the global
`enforcement_mode` with the tool-level `enforcement_mode` override.

| Global `enforcement_mode` | Tool `enforcement_mode` | Effective mode |
|---|---|---|
| `shadow` | *(not set)* | `shadow` |
| `shadow` | `block` | `block` |
| `block` | *(not set)* | `block` |
| `block` | `shadow` | `shadow` |

**Common pattern — block a single high-risk tool in an otherwise-shadow policy:**

```yaml
enforcement_mode: shadow    # safe default for all tools during onboarding

tools:
  execute_payment:
    enforcement_mode: block  # always enforced hard, even while global mode is shadow
```

This lets you onboard incrementally: observe all violations in shadow, but guarantee that
the most dangerous irreversible action is enforced from day one.

---

## 9. Shadow Mode Onboarding Workflow

Shadow mode is the recommended path for adopting TrueBearing without disrupting an
existing agent workflow. Follow these steps.

**Step 1 — Author the policy**

Write a policy with `enforcement_mode: shadow`. Validate and explain it:

```sh
truebearing policy lint    my-agent.policy.yaml
truebearing policy explain my-agent.policy.yaml
```

Fix any linter errors before proceeding. Warnings are advisory but should be reviewed.

**Step 2 — Register the agent and start the proxy**

```sh
truebearing agent register my-agent --policy ./my-agent.policy.yaml
truebearing serve --upstream https://your-mcp-server --policy ./my-agent.policy.yaml
```

**Step 3 — Observe for at least one week**

Run your agent normally. All calls are forwarded to the upstream. Violations are recorded
as `shadow_deny` in the audit log. Review what would have been blocked:

```sh
truebearing audit query --decision shadow_deny
truebearing audit query --decision shadow_deny --tool execute_payment
```

**Step 4 — Tune the policy**

- If `shadow_deny` records include legitimate agent calls (false positives), adjust the
  policy to remove the incorrect constraint and repeat Step 3.
- If `shadow_deny` records correspond only to actual misbehaviour, the policy is correct.

**Step 5 — Flip to block**

Edit the policy file:

```yaml
enforcement_mode: block
```

Restart the proxy. From this point, violations are denied — the upstream never receives
the blocked call. The audit log continues to capture every decision with a full signature.

**Important — policy fingerprint binding:** When the policy file changes, the policy
fingerprint changes. Any session created under the old fingerprint receives a
`policy_changed` error on its next tool call. Agents must start a new session after a
policy update. This is by design — re-evaluating a live session under a different policy
would create an audit gap.

---

## 10. Linter Rules Reference — L001–L019

Run the linter with:

```sh
truebearing policy lint <policy-file>
```

Exit codes: `1` if any **ERROR** is present; `0` for **WARNING** and **INFO** only.

| Code | Severity | Condition | How to fix |
|---|---|---|---|
| `L001` | ERROR | `may_use` is empty or missing | Add a `may_use:` list with every tool the agent may call |
| `L002` | ERROR | A tool in `tools:` is not listed in `may_use` | Add the tool to `may_use`, or remove it from `tools:` |
| `L003` | ERROR | `only_after` references a tool not in `may_use` | Add the referenced tool to `may_use`, or correct the tool name |
| `L004` | ERROR | `never_after` references a tool not in `may_use` | Add the referenced tool to `may_use`, or correct the tool name |
| `L005` | WARNING | `enforcement_mode` is not set | Set `enforcement_mode: shadow` or `enforcement_mode: block` explicitly |
| `L006` | WARNING | No `budget:` block defined | Add `budget:` with `max_tool_calls` and/or `max_cost_usd` |
| `L007` | WARNING | `session.max_history` is not set | Add `session: max_history: <N>` |
| `L008` | WARNING | A tool has `escalate_when` but `escalation.webhook_url` is not configured | Add `escalation: webhook_url: <url>`, or accept stdout-only escalation events |
| `L009` | INFO | `enforcement_mode: shadow` is active | Reminder — change to `block` when ready for production enforcement |
| `L010` | ERROR | `requires_prior_n.count` is zero or negative | Set `count` to a positive integer (≥ 1) |
| `L011` | WARNING | A tool has `taint.applies: true` but no tool has `taint.clears: true` | Add `taint: clears: true` to a clearance tool, or accept permanent session taint |
| `L012` | ERROR | `escalate_when.operator` is not a valid operator | Use one of: `>`, `<`, `>=`, `<=`, `==`, `!=`, `contains`, `matches` |
| `L013` | ERROR | Circular `only_after` dependency detected | Break the cycle — if A requires B and B requires A, neither can ever be called |
| `L014` | ERROR | A `never_when` predicate uses an unrecognised operator | Use one of: `is_external`, `contains_pattern`, `equals`, `not_equals` |
| `L015` | ERROR | A `never_when` predicate uses `contains_pattern` with an invalid Go regexp | Fix the `value` regexp; wrap `/` delimiters are stripped before compilation |
| `L016` | WARNING | `session.require_env` is set | Register agents with `--env <value>`; agents without a matching `env` claim will be denied |
| `L017` | ERROR | `rate_limit.window_seconds` is zero or negative | Set `window_seconds` to a positive integer |
| `L018` | ERROR | `rate_limit.max_calls` is zero or negative | Set `max_calls` to a positive integer; use `may_use` exclusion to disable a tool entirely |
| `L019` | WARNING | `never_when` has multiple predicates but `never_when_match` is not set | Add `never_when_match: any` (OR) or `never_when_match: all` (AND) to make intent explicit |

### L013 — Circular Dependency in Detail

L013 performs a directed acyclic graph (DAG) analysis across all `only_after`
relationships. It detects cycles of any length: pairs (`A → B → A`), triples
(`A → B → C → A`), and longer chains.

When a cycle is found, the linter prints the full path:

```
L013 [ERROR] circular sequence dependency: tool-a → only_after → tool-b → only_after → tool-a
             This session can never satisfy both constraints simultaneously. The agent will permanently block.
```

To fix: identify which dependency is not actually required and remove it. The typical
cause is a tool that is both a prerequisite of and dependent on another tool in the same
workflow — which is a logical deadlock.

---

## 11. Complete Policy Example

The following policy demonstrates every DSL feature. It is a valid, complete policy that
passes `truebearing policy lint` with zero errors (only the L009 INFO reminder for shadow
mode and L008 are present, both of which are suppressed when `webhook_url` is set and
neither is an error).

```yaml
version: "1"
agent: workflow-agent
enforcement_mode: shadow

session:
  max_history: 500
  max_duration_seconds: 7200

budget:
  max_tool_calls: 100
  max_cost_usd: 5.00

escalation:
  webhook_url: "https://hooks.example.com/services/escalation-handler"

may_use:
  - read_record
  - validate_record
  - reviewer_approval
  - submit_record
  - read_external_content
  - run_clearance_check
  - check_escalation_status

tools:
  submit_record:
    enforcement_mode: block

    sequence:
      only_after:
        - validate_record
        - reviewer_approval
      never_after:
        - read_external_content
      requires_prior_n:
        tool: validate_record
        count: 1

    # Block if the record type is a draft from an untrusted source.
    # never_when_match: any fires when any single predicate matches (OR logic).
    never_when_match: any
    never_when:
      - argument: record_type
        operator: equals
        value: external_draft

    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 10000

  read_external_content:
    taint:
      applies: true
      label: external_content_read

  run_clearance_check:
    taint:
      clears: true

  read_record: {}
  validate_record: {}
  reviewer_approval: {}
```

**Verify:**

```sh
truebearing policy lint    complete-example.policy.yaml
truebearing policy explain complete-example.policy.yaml
```

**What this policy enforces:**

1. **Whitelist** — only the seven listed tools may be called; all others are denied immediately.
2. **Budget** — the session is capped at 100 tool calls and $5.00 estimated cost.
3. **Sequence guard** — `submit_record` requires `validate_record` and `reviewer_approval`
   to appear in the session history before it may run.
4. **Minimum-count guard** — `validate_record` must have been called at least once.
5. **Content guard** — `submit_record` is denied when `record_type` equals `external_draft`,
   regardless of session history. Uses `never_when_match: any` (OR logic, explicit).
6. **Prompt-injection isolation** — `submit_record` is blocked if `read_external_content`
   was called this session (until `run_clearance_check` clears the taint).
7. **Taint propagation** — reading external content taints the session; the clearance tool
   removes the taint.
8. **Human escalation** — any call to `submit_record` with `amount_usd > 10000` is paused
   for human approval regardless of whether the sequence is satisfied.
9. **Tool-level block** — `submit_record` violations are always hard-denied even while the
   global mode is `shadow`.
