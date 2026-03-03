# TrueBearing

**Transparent MCP proxy with sequence-aware behavioral policy enforcement for autonomous AI agents.**

Permissions are point-in-time. Agent behavior is a sequence. TrueBearing enforces the sequence.

Every individual action your agent takes might be permitted. The failure mode that causes
irreversible damage — `read_credentials → open_connection → transmit_data` — is not a permission
failure. It is a behavioral sequence nobody declared forbidden. TrueBearing blocks it before
execution.

---

## What It Does

TrueBearing sits between your agent and its MCP tool server. It intercepts every tool call,
evaluates it against a YAML policy that is **sequence-aware** (it knows what happened before),
and enforces deterministically:

- **allow** — call proceeds to the upstream MCP server
- **deny** — call is blocked, a signed audit record is written, agent receives a structured error
- **escalate** — call is paused, a human operator approves or rejects it via CLI
- **shadow\_deny** — during onboarding: violation is logged but the call still proceeds

Every decision is signed with Ed25519 and written to an append-only audit log. The log is
replayable, tamper-evident, and queryable by session, tool, decision, or distributed trace ID.

---

## How TrueBearing Differs from Other Guardrail Tools

| | TrueBearing | Salus | LangChain Guardrails |
|---|---|---|---|
| Agent code changes required | None (proxy) | Yes (decorators) | Yes (middleware) |
| Policy lives outside agent | ✓ | ✗ | ✗ |
| Stateful sequence enforcement | ✓ | ✓ | ✗ |
| Tamper-evident audit trail | ✓ (Ed25519) | ✗ | ✗ |
| Works with existing agents | ✓ | ✗ | ✗ |
| EU AI Act evidence bundles | ✓ | ✗ | ✗ |
| Self-repair feedback to agent | ✓ | ✓ | ✗ |
| OpenAI / LangChain support | ✓ (proxy layer) | ✓ | ✓ |

If you control your agent code and want decorator-based enforcement with self-repair, Salus is
excellent. If you need policy enforcement without touching agent code, a tamper-evident audit trail
for regulators, or you're in a regulated industry where compliance teams define policy — that's
what TrueBearing is built for.

---

## Install

### Homebrew (macOS / Linux)

```sh
brew tap mercator-hq/truebearing https://github.com/mercator-hq/truebearing
brew install mercator-hq/truebearing/truebearing
```

### curl install script (macOS / Linux, no Go required)

```sh
curl -sSL https://raw.githubusercontent.com/mercator-hq/truebearing/master/scripts/install.sh | sh
```

Installs to `/usr/local/bin/truebearing`. Supported platforms: macOS arm64, macOS amd64,
Linux amd64, Linux arm64. Exits non-zero on unsupported platforms.

To install a specific version or a custom directory:

```sh
VERSION=0.1.0 INSTALL_DIR=$HOME/.local/bin \
  curl -sSL https://raw.githubusercontent.com/mercator-hq/truebearing/master/scripts/install.sh | sh
```

### From source (Go 1.22+)

```sh
CGO_ENABLED=0 go install github.com/mercator-hq/truebearing/cmd@latest
```

Or clone and build:

```sh
git clone https://github.com/mercator-hq/truebearing
cd truebearing
CGO_ENABLED=0 go build -o truebearing ./cmd
```

Verify:

```sh
truebearing --help
```

---

## Quick Start: Confirmed Blocked Call in Under 10 Minutes

Five steps from install to first enforced block:

1. **Install** — `brew install mercator-hq/truebearing/truebearing` (or `go install` — see [Install](#install) above)
2. **Scaffold** — `truebearing init` generates a starter policy interactively (takes ~2 minutes)
3. **Register** — `truebearing agent register <name> --policy ./truebearing.policy.yaml`
4. **Serve** — `truebearing serve --upstream <mcp-url> --policy ./truebearing.policy.yaml`
5. **Connect** — two lines of Python (see [Python SDK](#python-sdk-2-line-integration) below)

Steps 1–2 are one-time setup. Steps 3–5 repeat for each agent or project.

---

### 1. Write or scaffold a policy

**Quickest path — interactive wizard:**

```sh
truebearing init
```

Answers five questions and writes `truebearing.policy.yaml` in shadow mode. No DSL knowledge
required. The generated file passes `truebearing policy lint` with zero errors.

**Or write it manually.** Create `my-policy.yaml`:

```yaml
version: "1"
agent: data-agent
enforcement_mode: shadow   # logs violations but allows calls — safe for onboarding

budget:
  max_tool_calls: 50
  max_cost_usd: 5.00

may_use:
  - read_record
  - verify_record
  - submit_record
  - check_escalation_status

tools:
  submit_record:
    enforcement_mode: block   # always block on this tool regardless of global shadow mode

    sequence:
      only_after:
        - verify_record       # submit_record may only run after verify_record has been called
```

This policy enforces a single invariant: `submit_record` may never execute unless `verify_record`
has already run in this session. The policy is in shadow mode globally, but `submit_record` is
always blocked — making it safe to observe `read_record` and `verify_record` in shadow mode
while hard-blocking any attempt to skip the verification step.

Validate the policy syntax:

```sh
truebearing policy validate my-policy.yaml
# OK: my-policy.yaml is valid

truebearing policy lint my-policy.yaml
# (no errors)
```

See what it enforces in plain English:

```sh
truebearing policy explain my-policy.yaml
```

```
Agent: data-agent
Mode: SHADOW (violations are logged but calls proceed)
Allowed tools (4): read_record, verify_record, submit_record, check_escalation_status
Budget: 50 tool calls / $5.00 per session

Sequence guards:
  submit_record: may only run after [verify_record]

Tool-level enforcement overrides:
  submit_record: BLOCK (overrides global shadow mode)
```

### 2. Register your agent

```sh
truebearing agent register data-agent --policy my-policy.yaml
```

```
Agent:          data-agent
Public key:     /Users/you/.truebearing/keys/data-agent.pub.pem
JWT written to: /Users/you/.truebearing/keys/data-agent.jwt
Allowed tools (4 from policy may_use): [read_record, verify_record, submit_record, check_escalation_status]

To use:
  export TRUEBEARING_AGENT_JWT=$(cat /Users/you/.truebearing/keys/data-agent.jwt)
  OR pass --agent-jwt flag to your client
```

Export the JWT so it is available for the next steps:

```sh
export TRUEBEARING_AGENT_JWT=$(cat ~/.truebearing/keys/data-agent.jwt)
```

### 3. Start the proxy

```sh
truebearing serve --upstream https://your-mcp-server.example.com --policy my-policy.yaml
```

```
TrueBearing proxy
  listening on  :7773
  upstream      https://your-mcp-server.example.com
  policy        my-policy.yaml  (a8f9c2...)
  db            /Users/you/.truebearing/truebearing.db
```

The proxy is now intercepting all MCP tool calls on `http://localhost:7773`.

> **No upstream server?** For testing, you can point `--upstream` at any HTTP server that returns
> a valid JSON-RPC 2.0 response. The proxy evaluates policy and blocks denied calls before they
> ever reach the upstream, so you will see denials even if the upstream is unreachable.

### 4. Make a tool call

Every `tools/call` request to the proxy requires two headers:

- `Authorization: Bearer <jwt>` — your agent's signed JWT
- `X-TrueBearing-Session-ID: <uuid>` — a stable session identifier for this agent run

**An allowed call** (`verify_record` — no sequence guard on this tool):

```sh
SESSION_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')

curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TRUEBEARING_AGENT_JWT" \
  -H "X-TrueBearing-Session-ID: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-1",
    "method": "tools/call",
    "params": {
      "name": "verify_record",
      "arguments": {"record_id": "rec-001"}
    }
  }'
```

The proxy evaluates the policy, allows the call, forwards it to the upstream, and streams the
upstream response back. A signed audit record is written for this `allow` decision.

**A denied call** (`submit_record` — called without `verify_record` in session history):

Open a **new terminal** (a fresh session ID means no history):

```sh
NEW_SESSION=$(uuidgen | tr '[:upper:]' '[:lower:]')

curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TRUEBEARING_AGENT_JWT" \
  -H "X-TrueBearing-Session-ID: $NEW_SESSION" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-2",
    "method": "tools/call",
    "params": {
      "name": "submit_record",
      "arguments": {"record_id": "rec-001"}
    }
  }'
```

Response:

```json
{
  "jsonrpc": "2.0",
  "id": "req-2",
  "error": {
    "code": -32603,
    "message": "tool call denied",
    "data": {
      "decision": "deny",
      "reason": "sequence.only_after: verify_record not satisfied (not in session history)",
      "rule_id": "sequence.only_after",
      "session_id": "<your-session-id>"
    }
  }
}
```

The call is blocked. No request is forwarded to the upstream. A signed audit record is written
for this `deny` decision.

**An allowed call after the prerequisite** (same session, `verify_record` already in history):

```sh
# First call in the same session:
curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TRUEBEARING_AGENT_JWT" \
  -H "X-TrueBearing-Session-ID: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":"req-3","method":"tools/call","params":{"name":"verify_record","arguments":{"record_id":"rec-001"}}}'

# Now submit_record is allowed in that same session:
curl -s -X POST http://localhost:7773/mcp/v1 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TRUEBEARING_AGENT_JWT" \
  -H "X-TrueBearing-Session-ID: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":"req-4","method":"tools/call","params":{"name":"submit_record","arguments":{"record_id":"rec-001"}}}'
```

### 5. Check the audit log

Every decision is written to the local SQLite database and can be queried at any time:

```sh
truebearing audit query
```

```
RECORDED_AT            SESSION   SEQ  AGENT       TOOL           DECISION  REASON
2026-02-28T14:01:10Z   a1b2c3d4    1  data-agent  verify_record  allow
2026-02-28T14:01:15Z   e5f6a7b8    1  data-agent  submit_record  deny      sequence.only_after: verify_record not s...
2026-02-28T14:01:22Z   a1b2c3d4    2  data-agent  submit_record  allow
```

Filter by decision to review only violations:

```sh
truebearing audit query --decision deny
truebearing audit query --decision shadow_deny    # violations that were logged but allowed
```

Filter by session to see the full history of one agent run:

```sh
truebearing audit query --session <session-id>
```

Verify the cryptographic signatures on every record:

```sh
truebearing audit verify audit.jsonl
```

---

## Python SDK (2-line integration)

> **Prerequisites:** complete steps 1–4 of the [Quick Start](#quick-start-confirmed-blocked-call-in-under-10-minutes)
> above before running the code below. A running proxy (`truebearing serve`) and a registered
> agent JWT are required. The SDK spawns the proxy automatically when given a `policy:` path,
> but the binary must be installed and the agent must be registered first. Takes ~10 minutes
> on a clean machine.

```sh
pip install truebearing
```

```python
from truebearing import PolicyProxy
import anthropic

# Before: client = anthropic.Anthropic()
# After (2 lines):
from truebearing import PolicyProxy
client = PolicyProxy(anthropic.Anthropic(), policy='./my-policy.yaml')

# Nothing else changes. TrueBearing spawns the proxy subprocess automatically,
# injects the session ID and JWT into every request, and enforces the policy.
```

See [sdks/python/](sdks/python/) for the full Python SDK documentation.

## Node.js SDK

```sh
npm install @mercator/truebearing
```

```typescript
import { PolicyProxy } from '@mercator/truebearing';
import Anthropic from '@anthropic-ai/sdk';

const client = new PolicyProxy(new Anthropic(), {
  policy: './my-policy.yaml'
});
```

See [sdks/node/](sdks/node/) for the full Node.js SDK documentation.

---

## Key CLI Commands

| Command | Description |
|---|---|
| `truebearing serve --upstream <url> --policy <file>` | Start the proxy |
| `truebearing agent register <name> --policy <file>` | Issue credentials for an agent |
| `truebearing agent list` | Show all registered agents |
| `truebearing policy validate <file>` | Parse and validate a policy YAML |
| `truebearing policy lint <file>` | Warn on common policy mistakes |
| `truebearing policy explain <file>` | Plain-English summary of what a policy enforces |
| `truebearing policy diff <old> <new>` | Show what changed between two policy versions |
| `truebearing audit query` | Query the audit log (filterable by session, tool, decision, trace ID) |
| `truebearing audit verify <file>` | Verify Ed25519 signatures on an exported audit log |
| `truebearing session list` | Show all active sessions |
| `truebearing session inspect <id>` | Full tool call history for one session |
| `truebearing session terminate <id>` | Force-expire a session |
| `truebearing escalation list` | List pending escalations |
| `truebearing escalation approve <id>` | Approve a pending escalation |
| `truebearing escalation reject <id>` | Reject a pending escalation |
| `truebearing simulate --trace <file> --policy <file>` | Replay a session trace against a policy (offline, no DB writes) |

---

## Onboarding Path (Recommended)

The safest way to deploy TrueBearing is shadow mode first:

1. **Write your policy with `enforcement_mode: shadow`** — violations are logged but calls proceed.
   High-risk tools can still set `enforcement_mode: block` individually.

2. **Run your agent for a week** — normal production traffic flows uninterrupted.

3. **Review what would have been blocked:**
   ```sh
   truebearing audit query --decision shadow_deny
   ```

4. **Adjust the policy** if any legitimate call would have been wrongly denied.

5. **Flip to block mode when you're confident:**
   ```yaml
   enforcement_mode: block
   ```

This workflow means you can deploy TrueBearing on day one with zero risk of disruption, and
move to full enforcement once you have evidence that the policy captures your intent correctly.

---

## Policy Packs

Reusable policy templates for common patterns are in [policy-packs/](policy-packs/):

- [policy-packs/fintech/](policy-packs/fintech/) — payment automation, wire transfer guards
- [policy-packs/healthcare/](policy-packs/healthcare/) — PHI taint tracking, claims submission guards
- [policy-packs/devops/](policy-packs/devops/) — deployment sequence guards, environment isolation

---

## Next Steps

- **[docs/policy-reference.md](docs/policy-reference.md)** — complete DSL specification with all
  predicates (`only_after`, `never_after`, `requires_prior_n`, `taint`, `escalate_when`, `never_when`)
- **[docs/demo-script.md](docs/demo-script.md)** — step-by-step 20-minute demo script for
  technical evaluations
- **[docs/integrations/openai.md](docs/integrations/openai.md)** — using TrueBearing with OpenAI clients
- **[docs/integrations/langchain.md](docs/integrations/langchain.md)** — using TrueBearing with LangChain
- **[docs/integrations/langraph.md](docs/integrations/langraph.md)** — using TrueBearing with LangGraph
- **[testdata/policies/](testdata/policies/)** — example policy files for fintech, healthcare,
  insurance, legal, and regulatory workflows

---

## Security Model

- **Agent identity** is Ed25519-signed JWT — not a header, not a config value. A valid-looking
  but unsigned token is treated identically to a missing token: `401 Unauthorized`.
- **Audit records** are signed at write time and verifiable offline. A tampered record is
  detectable without access to the original database.
- **Fail closed.** If TrueBearing cannot evaluate a request (database error, policy parse error,
  JWT validation failure), it blocks the call. It never defaults to allow under uncertainty.
- **Policy is bound at session creation.** If the policy file changes after a session starts,
  subsequent calls to that session return a `policy_changed` error until a new session is opened.
  No silent re-evaluation under a different policy.

---

## License

Apache 2.0. See [LICENSE](LICENSE).
