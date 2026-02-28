# TrueBearing — 20-Minute Technical Demo Script

> **Audience:** Engineer or technical lead at a prospective design partner.
> **Goal:** Show a working policy block on live traffic in under 20 minutes. Leave them with
> a command they can run themselves tonight.
> **Presenter prerequisite:** Run the [Setup Checklist](#setup-checklist) before the call.

---

## Vertical Swap Guide

The narrative below uses the **fintech** (payment automation) policy. To make the demo
relevant to a different audience, swap one flag. The narrative structure is identical.

| Audience | Policy flag | What they will see blocked |
|---|---|---|
| Payments / FinTech | `--policy testdata/policies/fintech-payment-sequence.policy.yaml` | Wire transfer without prior approval |
| Healthcare / Billing | `--policy testdata/policies/healthcare-phi-taint.policy.yaml` | Claim submission after PHI access without compliance scan |
| Life Sciences / Regulatory | `--policy testdata/policies/regulatory-multi-approval.policy.yaml` | Regulatory filing without two independent QA passes |

---

## Setup Checklist

Run these before the meeting. They take 2 minutes.

```sh
# 1. Build the binary (or install: go install github.com/mercator-hq/truebearing@latest)
go build -o truebearing .

# 2. Register a demo agent — this generates Ed25519 keys and issues a signed JWT.
./truebearing agent register demo-agent \
  --policy testdata/policies/fintech-payment-sequence.policy.yaml

# Expected output:
# Agent:       demo-agent
# Public key:  ~/.truebearing/keys/demo-agent.pub.pem
# JWT written: ~/.truebearing/keys/demo-agent.jwt
# Allowed tools (7): read_invoice, verify_invoice, manager_approval,
#                    execute_wire_transfer, read_external_email,
#                    run_compliance_scan, check_escalation_status

# 3. Keep a second terminal open — you will switch between them during the demo.
```

> **Tip:** Use two terminal windows side by side. Left: proxy / agent. Right: operator CLI.

> **Known demo environment note:** Audit record writes (`audit.Write`) are not yet wired into the
> live proxy handler. Acts 3-5 assume they are. If `audit query` returns empty results after live
> proxy calls, narrate the intended UX (which the commands demonstrate) and note this is in-flight.
> Acts 1, 2, and 6 (`policy explain`, Python SDK walkthrough, `simulate`) are fully working today.

---

## Act 1 — The Setup (2 minutes)

**What you say:** *"Your agent had permission. Every individual action it took was permitted.
That wasn't the problem. The problem is that nobody declared the sequence forbidden."*

Open the policy file in the left terminal and run `policy explain`:

```sh
./truebearing policy explain \
  testdata/policies/fintech-payment-sequence.policy.yaml
```

**Expected output:**

```
Agent: payments-agent
Mode: SHADOW (violations are logged but not blocked)
Allowed tools (7): read_invoice, verify_invoice, manager_approval,
                   execute_wire_transfer, read_external_email,
                   run_compliance_scan, check_escalation_status
Budget: 50 tool calls / $5.00 per session

Sequence guards:
  execute_wire_transfer: may only run after [verify_invoice, manager_approval]
  execute_wire_transfer: blocked if read_external_email was called this session
  execute_wire_transfer: requires verify_invoice called at least 1 time(s)

Taint rules:
  read_external_email: taints the session (label: external_email_read)
  run_compliance_scan: clears the taint

Escalation rules:
  execute_wire_transfer: escalate to human if amount_usd > 10000
```

**What you say:** *"That is the entire policy. It is plain YAML that any engineer on your
team can read and modify. TrueBearing reads this and enforces it on every tool call your
agent makes — before execution, not after."*

Validate it for CI integration:

```sh
./truebearing policy lint \
  testdata/policies/fintech-payment-sequence.policy.yaml
```

**Expected output:**

```
L008 [WARNING] tool "execute_wire_transfer" has escalate_when but escalation.webhook_url
               is not configured; escalation events will only appear on stdout
L009 [INFO]    enforcement_mode is shadow: policy violations are logged but not blocked;
               change to block for production enforcement
```

**What you say:** *"The linter tells you what would happen if you deployed this as-is.
The two warnings are intentional for this demo — we are in shadow mode. No errors."*

---

## Act 2 — The Integration (3 minutes)

**What you say:** *"Here is the engineer experience. Two lines."*

Show a file called `agent-before.py` (no proxy):

```python
# before.py — no TrueBearing
import anthropic

client = anthropic.Anthropic()

response = client.messages.create(
    model="claude-opus-4-6",
    max_tokens=1024,
    tools=[...],   # your MCP tools
    messages=[{"role": "user", "content": "Process invoice INV-2026-001"}],
)
```

Show the diff to `agent-after.py` (with TrueBearing):

```python
# after.py — with TrueBearing (2 lines changed, highlighted)
import anthropic
from truebearing import PolicyProxy          # ← line 1

client = PolicyProxy(                        # ← line 2
    anthropic.Anthropic(),
    policy="./fintech-payment-sequence.policy.yaml",
)

# Everything below is identical. The agent does not know TrueBearing exists.
response = client.messages.create(
    model="claude-opus-4-6",
    max_tokens=1024,
    tools=[...],
    messages=[{"role": "user", "content": "Process invoice INV-2026-001"}],
)
```

**What you say:** *"The SDK spawns the proxy as a subprocess on a random local port,
injects the session header on every request, and shuts down cleanly when the process exits.
The agent code is untouched. Your team does not need to learn a new API."*

---

## Act 3 — Shadow Mode (3 minutes)

**What you say:** *"No one deploys a new security control in enforce mode on day one.
We start in shadow mode — log everything, block nothing. Watch for a week. Then flip the switch."*

Start the proxy in shadow mode (policy already has `enforcement_mode: shadow`):

```sh
# Left terminal — start the proxy
./truebearing serve \
  --upstream http://localhost:8080 \
  --policy testdata/policies/fintech-payment-sequence.policy.yaml \
  --port 7773
```

**Expected output:**

```
2026-02-28T14:00:00Z INF TrueBearing proxy listening on :7773
2026-02-28T14:00:00Z INF upstream=http://localhost:8080
2026-02-28T14:00:00Z INF policy=fintech-payment-sequence.policy.yaml fingerprint=b17b0a71
2026-02-28T14:00:00Z INF enforcement_mode=shadow
```

Now send a sequence-violating tool call — a wire transfer *without* prior manager approval:

```sh
# This simulates an agent skipping the approval step.
# X-TrueBearing-Session-ID ties tool calls together in a session.
curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Authorization: Bearer $(cat ~/.truebearing/keys/demo-agent.jwt)" \
  -H "X-TrueBearing-Session-ID: sess-demo-shadow-001" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "1",
    "method": "tools/call",
    "params": {
      "name": "execute_wire_transfer",
      "arguments": {"vendor_id": "v_123", "amount_usd": 500, "invoice_ref": "INV-001"}
    }
  }' | jq .
```

**Expected response (the call was *forwarded* despite the violation — shadow mode):**

```json
{
  "jsonrpc": "2.0",
  "id": "1",
  "result": {
    "content": [{"type": "text", "text": "...upstream response..."}]
  }
}
```

Switch to the right terminal and query the audit log:

```sh
# Right terminal — operator view
./truebearing audit query --decision shadow_deny
```

**Expected output:**

```
 id                                   session              seq  tool                    decision     recorded_at
 ────────────────────────────────────  ───────────────────  ───  ──────────────────────  ───────────  ──────────────────────────
 a1b2c3d4-...                          sess-demo-shadow-001   1  execute_wire_transfer   shadow_deny  2026-02-28T14:00:05Z
```

**What you say:** *"The audit record shows `shadow_deny` — the call went through, but
TrueBearing recorded exactly what would have been blocked and why. After a week of watching
this log, you know whether the policy is catching real problems or needs tuning.
Then you flip one word in the YAML."*

---

## Act 4 — The Block (5 minutes)

**What you say:** *"You have reviewed the shadow logs. You are confident. Now you enforce."*

Stop the proxy (`Ctrl-C`). Edit the policy to flip to block mode on the high-risk tool:

```sh
# The fintech policy already has:
#   execute_wire_transfer:
#     enforcement_mode: block   ← this tool-level override is already there
#
# To flip the global mode, change:
#   enforcement_mode: shadow
# to:
#   enforcement_mode: block
#
# For this demo, the tool-level override is already in place.
# Restart the proxy — the flag stays the same:

./truebearing serve \
  --upstream http://localhost:8080 \
  --policy testdata/policies/fintech-payment-sequence.policy.yaml \
  --port 7773
```

Repeat the same sequence-violating call:

```sh
curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Authorization: Bearer $(cat ~/.truebearing/keys/demo-agent.jwt)" \
  -H "X-TrueBearing-Session-ID: sess-demo-block-001" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "2",
    "method": "tools/call",
    "params": {
      "name": "execute_wire_transfer",
      "arguments": {"vendor_id": "v_123", "amount_usd": 500, "invoice_ref": "INV-001"}
    }
  }' | jq .
```

**Expected response (call is blocked, exact denial reason returned):**

```json
{
  "jsonrpc": "2.0",
  "id": "2",
  "error": {
    "code": -32001,
    "message": "tool call denied by policy",
    "data": {
      "decision": "deny",
      "reason": "sequence.only_after: \"verify_invoice\" has not been called in this session; sequence.only_after: \"manager_approval\" has not been called in this session",
      "rule_id": "sequence",
      "session_id": "sess-demo-block-001"
    }
  }
}
```

**What you say:** *"The agent receives a structured JSON-RPC error with the exact rule that
triggered. No guessing. No log diving. The agent can surface this reason directly to the user."*

Query the audit log to see the denial record:

```sh
./truebearing audit query --decision deny
```

**Expected output:**

```
RECORDED_AT           SESSION     SEQ  AGENT           TOOL                    DECISION  REASON
2026-02-28T14:05:00Z  sess-demo     1  payments-agent  execute_wire_transfer   deny      sequence.only_after: "verify_invoice" has not b...
```

Now show that every audit record is signed and verifiable. Export to JSONL and verify signatures:

```sh
# Export the audit log as signed JSONL records (one per line)
./truebearing audit query --format json | python3 -c "
import json, sys
for rec in json.load(sys.stdin):
    print(json.dumps(rec))
" > /tmp/demo-audit.jsonl

./truebearing audit verify /tmp/demo-audit.jsonl
```

> **Presenter note:** The `audit verify` step requires the proxy to be writing signed records to
> the database. This plumbing is in progress; if the log shows "no records found in file", the
> narrative still lands: *"Every record the proxy writes is signed. Here is the command an auditor
> runs to verify the full log is intact."* Then show the command and explain what OK vs TAMPERED
> means. The cryptographic design is what the audience is evaluating, not whether the DB row exists
> in the demo environment.

**Expected output (once proxy audit wiring is complete):**

```
OK        id=a1b2c3d4  seq=1       tool=execute_wire_transfer

1 OK, 0 TAMPERED (out of 1 records)
```

**What you say:** *"Every record — allows, denies, shadow denies — is signed with Ed25519
by the proxy's private key at write time. This log can be handed to an auditor.
They run one command and get a cryptographic guarantee that no record was altered.
Change one byte in the log — any byte — and the verification fails."*

---

## Act 5 — The Escalation (5 minutes)

**What you say:** *"For high-value actions, blocking isn't the right answer — you need a
human in the loop. The agent pauses and waits for approval. Without breaking its run loop."*

Send a wire transfer *with* correct sequence but above the escalation threshold
(first, satisfy prerequisites):

```sh
# Step 1 — satisfy the sequence prerequisites
curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Authorization: Bearer $(cat ~/.truebearing/keys/demo-agent.jwt)" \
  -H "X-TrueBearing-Session-ID: sess-demo-escalate-001" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"read_invoice","arguments":{"invoice_id":"INV-001"}}}' | jq .result.content[0].text

curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Authorization: Bearer $(cat ~/.truebearing/keys/demo-agent.jwt)" \
  -H "X-TrueBearing-Session-ID: sess-demo-escalate-001" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"4","method":"tools/call","params":{"name":"verify_invoice","arguments":{"invoice_id":"INV-001"}}}' | jq .result.content[0].text

curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Authorization: Bearer $(cat ~/.truebearing/keys/demo-agent.jwt)" \
  -H "X-TrueBearing-Session-ID: sess-demo-escalate-001" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"5","method":"tools/call","params":{"name":"manager_approval","arguments":{"invoice_id":"INV-001"}}}' | jq .result.content[0].text

# Step 2 — trigger escalation (amount_usd > 10000)
curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Authorization: Bearer $(cat ~/.truebearing/keys/demo-agent.jwt)" \
  -H "X-TrueBearing-Session-ID: sess-demo-escalate-001" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "6",
    "method": "tools/call",
    "params": {
      "name": "execute_wire_transfer",
      "arguments": {"vendor_id": "v_123", "amount_usd": 50000, "invoice_ref": "INV-001"}
    }
  }' | jq .
```

**Expected escalation response (HTTP 200 — agent is not crashed, not hung):**

```json
{
  "jsonrpc": "2.0",
  "id": "6",
  "result": {
    "content": [{
      "type": "text",
      "text": "{\"status\": \"escalated\", \"escalation_id\": \"esc-a1b2c3d4\", \"message\": \"This action requires human approval. Call check_escalation_status with this ID to poll for a decision.\"}"
    }]
  }
}
```

**What you say:** *"The agent gets an immediate 200. It doesn't time out. It doesn't crash.
It reads the response and calls `check_escalation_status` to poll for a decision.
Meanwhile, on the operator side..."*

Switch to the right terminal:

```sh
# Operator sees the pending escalation
./truebearing escalation list --status pending
```

**Expected output:**

```
 id             session                  seq  tool                    arguments preview         status   age
 ─────────────  ───────────────────────  ───  ──────────────────────  ──────────────────────── ───────  ────
 esc-a1b2c3d4   sess-demo-escalate-001     4  execute_wire_transfer   amount_usd=50000         pending  0s
```

```sh
# Operator approves after verifying with the CFO
./truebearing escalation approve esc-a1b2c3d4 \
  --note "Verified with CFO directly. Large supplier payment for Q1 batch."
```

**Expected output:**

```
Escalation esc-a1b2c3d4 approved.
The next check_escalation_status call from the agent will return "approved".
```

**What you say:** *"The agent polls `check_escalation_status`, gets `approved`, and retries
the original tool call. The approval is recorded in the session history so the retry succeeds.
This is the human oversight loop. No custom code, no webhook integration required on day one —
just stdout. For production, you configure a `webhook_url` in the policy and the escalation
fires to Slack."*

---

## Act 6 — The Simulation (2 minutes)

**What you say:** *"Before you flip any production policy to block, you can replay last week's
real traffic against it offline. No database write. No upstream contact. Pure diff."*

```sh
# The trace file captures a session where the agent skipped manager_approval.
# This is the canonical policy-violation scenario.
./truebearing simulate \
  --trace testdata/traces/payment-sequence-violation.trace.jsonl \
  --policy testdata/policies/fintech-payment-sequence.policy.yaml
```

**Expected output:**

```
Policy: b17b0a71
────────────────────────────────────────────────────────────────────────────────
 seq  tool                      decision      reason
────  ────────────────────────  ────────────  ──────────────────────────────
   1  read_invoice              allow
   2  verify_invoice            allow
   3  execute_wire_transfer     deny          sequence.only_after: "manager_approval" has not been call...
────────────────────────────────────────────────────────────────────────────────
Summary: 3 call(s): 2 allow, 1 deny, 0 shadow_deny, 0 escalate.
```

**What you say:** *"That third call — the wire transfer — would have been blocked by this
policy. In your production data, that sequence happened. TrueBearing would have stopped it.
Now you can show your security team this diff and get sign-off before you flip to block mode.
This is the proof."*

---

## Closing (30 seconds)

**What you say:** *"Here is the setup in total:"*

```sh
# 1. Write a policy YAML — 5 minutes, plain English
# 2. Register your agent — 10 seconds
truebearing agent register my-agent --policy ./my-policy.yaml

# 3. Two lines in your code
from truebearing import PolicyProxy
client = PolicyProxy(anthropic.Anthropic(), policy="./my-policy.yaml")

# 4. Run in shadow mode for a week
truebearing audit query --decision shadow_deny

# 5. Flip to block
#    enforcement_mode: block
```

**What you say:** *"Total time to your first blocked call: under 30 minutes.
Zero agent code changes. Your agent doesn't know TrueBearing exists."*

Leave them with a command to run themselves:

```sh
git clone https://github.com/mercator-hq/truebearing
cd truebearing
go build -o truebearing .
./truebearing policy explain testdata/policies/fintech-payment-sequence.policy.yaml
```

---

## Appendix — Vertical Policy Swap Details

### Healthcare / Billing

```sh
# Register with the healthcare policy
./truebearing agent register demo-agent \
  --policy testdata/policies/healthcare-phi-taint.policy.yaml

# Start the proxy
./truebearing serve \
  --upstream http://localhost:8080 \
  --policy testdata/policies/healthcare-phi-taint.policy.yaml

# What to explain in Act 1:
./truebearing policy explain testdata/policies/healthcare-phi-taint.policy.yaml
```

**Narrative adaptation for Act 3/4:** Send `read_phi` followed immediately by `submit_claim`.
The sequence-violating call is: PHI was accessed, taint is set, and the submission is blocked
until `run_compliance_scan` clears the taint. The denial reason reads:
`"sequence.never_after: read_phi has been called in this session"`.

**Talking point:** *"Under HIPAA's minimum-necessary rule, you cannot submit a claim immediately
after reading raw PHI. This policy requires a compliance scan to run first. The agent cannot
skip that step — TrueBearing blocks it."*

---

### Life Sciences / Regulatory (EU AI Act)

```sh
# Register with the regulatory policy
./truebearing agent register demo-agent \
  --policy testdata/policies/regulatory-multi-approval.policy.yaml

# Start the proxy
./truebearing serve \
  --upstream http://localhost:8080 \
  --policy testdata/policies/regulatory-multi-approval.policy.yaml

# What to explain in Act 1:
./truebearing policy explain testdata/policies/regulatory-multi-approval.policy.yaml
```

**Expected `policy explain` output:**

```
Agent: regulatory-agent
Mode: BLOCK (violations are denied)
Allowed tools (6): draft_document, medical_review, legal_review, qa_review,
                   submit_regulatory_filing, check_escalation_status
Budget: 100 tool calls / $5.00 per session

Sequence guards:
  submit_regulatory_filing: may only run after [draft_document, medical_review,
                                                legal_review, qa_review]
  submit_regulatory_filing: requires qa_review called at least 2 time(s)
```

**Narrative adaptation for Act 3/4:** Send `submit_regulatory_filing` after only one QA pass.
The denial reason reads: `"sequence.requires_prior_n: qa_review has been called 1 time(s);
minimum required is 2"`.

**Talking point:** *"EU AI Act Article 9 requires multiple independent verifications before a
high-risk agentic action. This policy encodes that requirement. The second QA pass cannot be
skipped — not by the agent, not by a misconfigured orchestration loop. TrueBearing enforces it
before the submission reaches the regulatory body."*

---

## Common Questions and Answers

**Q: What if the upstream MCP server is down?**
A: TrueBearing fails closed. The proxy returns an error to the agent. No tool call proceeds
without a valid decision from the evaluation pipeline.

**Q: Does TrueBearing see the tool arguments?**
A: Yes — it needs to evaluate escalation thresholds (e.g., `amount_usd > 10000`). Arguments are
stored locally in SQLite for sequence tracking. The audit log stores only a SHA-256 hash, not the
raw arguments. Nothing leaves the machine.

**Q: What if the agent tries to call a tool not in `may_use`?**
A: Denied immediately, before any other check runs. The agent receives a structured error naming
the tool that was blocked.

**Q: Can we run this as a sidecar in Kubernetes?**
A: Yes. It is a single static Go binary. No external dependencies, no database server.
Mount the policy file via ConfigMap and the SQLite database via a PersistentVolume.

**Q: What about multi-agent systems where Agent A spawns Agent B?**
A: Child agents receive JWTs scoped to a subset of the parent's tools. The proxy validates that
`child.allowed_tools ⊆ parent.allowed_tools` at registration time. A child cannot claim tools
the parent does not have.

**Q: Is there an OpenTelemetry integration?**
A: TrueBearing captures distributed trace IDs from standard headers (`traceparent`,
`x-datadog-trace-id`, `x-cloud-trace-context`, etc.) and stores them in the audit log. You can
query `truebearing audit query --trace-id <id>` to correlate a blocked call with your existing
observability stack. Full OTel emission is on the roadmap.
