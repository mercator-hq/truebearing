# TrueBearing MVP — Engineering Build Plan

> **Product:** TrueBearing by Mercator  
> **Version:** 1.0 — Engineering North Star  
> **Status:** Pre-build. This document governs every design decision until closed beta.  
> **Principle:** If an operator cannot configure, simulate, or observe it via a CLI command, the feature does not exist yet.

---

## Table of Contents

1. [What We Are Building and Why](#1-what-we-are-building-and-why)
2. [Core Engineering Philosophy](#2-core-engineering-philosophy)
3. [Architectural Thumb Rules](#3-architectural-thumb-rules)
4. [The Build Order](#4-the-build-order)
5. [Phase 1 — CLI Skeleton & Cryptographic Identity](#5-phase-1--cli-skeleton--cryptographic-identity)
6. [Phase 2 — Policy DSL & Parser](#6-phase-2--policy-dsl--parser)
7. [Phase 3 — Wire Protocol & MCP Proxy Shell](#7-phase-3--wire-protocol--mcp-proxy-shell)
8. [Phase 4 — The Evaluation Engine](#8-phase-4--the-evaluation-engine)
9. [Phase 5 — Evidence, Audit & Simulation DX](#9-phase-5--evidence-audit--simulation-dx)
10. [Phase 6 — SDKs & Integration Story](#10-phase-6--sdks--integration-story)
11. [Data Models & Schemas](#11-data-models--schemas)
12. [The Policy DSL — Full Specification](#12-the-policy-dsl--full-specification)
13. [The CLI Surface — Full Specification](#13-the-cli-surface--full-specification)
14. [The Three POC Fixes (Non-Negotiable)](#14-the-three-poc-fixes-non-negotiable)
15. [Production Deployment Model](#15-production-deployment-model)
16. [The Integration Story (The 2-Line Promise)](#16-the-integration-story-the-2-line-promise)
17. [Design Partner Use Cases](#17-design-partner-use-cases)
18. [Strategic Blind Spots & Additions](#18-strategic-blind-spots--additions)
19. [Testing & Quality Strategy](#19-testing--quality-strategy)
20. [Moat & IP Architecture](#20-moat--ip-architecture)
21. [Post-MVP Roadmap Signals](#21-post-mvp-roadmap-signals)

---

## 1. What We Are Building and Why

### The Problem in One Sentence

Permissions are point-in-time. Agent behavior is a sequence. Every tool in existence answers the wrong question.

### The Canonical Failure Mode

The Replit incident (July 2025): an agent was authorized to be in production. Every individual action it took was permitted. No permission check could have stopped it — because the failure was not a permissions failure. It was a behavioral sequence nobody had declared forbidden.

```
read_credentials       → permitted
open_external_conn     → permitted
transmit_data          → permitted
read → open → transmit → CATASTROPHIC. UNRECOVERABLE. UNGOVERNED.
```

OPA, RBAC, IAM all ask: *can entity X access resource Y, right now?* That is a useful question. It is the wrong question for agents.

### What TrueBearing Is

A transparent MCP proxy that:
- Intercepts every tool call before execution
- Evaluates it against a YAML policy that is **sequence-aware** — it knows what happened before
- Enforces deterministically: `allow / deny / escalate-to-human`
- Propagates taint when untrusted content enters the session
- Tracks budget (call count + cost) with hard circuit breakers
- Produces a signed, replayable audit record of every decision

TrueBearing is **not** observability. Observability sees what happened. TrueBearing **blocks before execution**.

### The Academic Foundation (Our Moat's Intellectual Basis)

- **Google DeepMind CaMeL paper (April 2025):** proved the architecture — taint tracking plus policy gating evaluated against execution sequences. The paper's stated limitation: users must define the security policies. We build the language and runtime.
- **PCAS paper (arXiv 2602.16708, Feb 2026):** formally identified the gap: *"Existing approaches fall short on expressiveness (supporting complex policies over interaction history), dependency tracking, and deterministic enforcement."* Published nine days before our pitch. Nobody has built the tool yet.

---

## 2. Core Engineering Philosophy

### Three Pillars

**1. Lightning Fast**  
Sub-5ms evaluation latency on the policy pipeline. Agents are already slow; the proxy adds zero perceptible overhead. This is achieved through compiled policy evaluation, an in-memory session cache backed by SQLite WAL mode, and a pure Go evaluation pipeline with no reflection or dynamic dispatch in the hot path.

**2. Cryptographically Honest**  
Agent identity is Ed25519-signed JWT — not header-based, not config-based. Audit logs are signed at write time and verifiable offline. A session is permanently bound to the policy fingerprint that was active when it was created. If the policy changes, the session must be renewed. There are no soft failures.

**3. CLI-First**  
The CLI is the product interface, not a convenience wrapper. Every design decision about what the system can do must be driven by what operators need to express and observe. Code is not written for a feature until there is a CLI command that invokes it. The CLI surface is specified in full in this document before a single line of engine code is written.

---

## 3. Architectural Thumb Rules

### Rule 1: Fail Closed, Always

If the SQLite file is locked — block. If the policy YAML is malformed — block. If the JWT is missing — 401. If the policy fingerprint changes mid-session — hard error, demand session renewal. **Silent failures are critical security vulnerabilities.** TrueBearing enforces at the point of execution; if it cannot evaluate, it cannot allow.

### Rule 2: No Domain Logic in Go

The engine knows absolutely nothing about FinTech, Healthcare, or DevOps. The Go engine understands only primitives: `strings`, `booleans`, `uint64 sequences`, `taint state`, `JSON paths`, and `budget counters`. Domain logic lives 100% inside the operator's YAML policy file. A fintech engineer and a DevOps engineer must both be able to write policies that feel natural without us having built anything domain-specific.

### Rule 3: Simplicity Over Cleverness in State

**Do not evict session history.** Use strictly monotonic `uint64` sequence numbers. If a session hits the hard history cap (default: 10,000 events), return a descriptive error demanding the agent start a new session. Long-lived sessions should be an explicit operator decision, not a silent corruption mode.

**Embedded over Distributed.** The MVP uses `database/sql` with a local SQLite file in WAL mode. No Postgres. No Redis. No external dependencies. The proxy must be a single static binary that deploys as a sidecar with `cp truebearing /usr/local/bin`.

### Rule 4: The Pipeline Pattern

The evaluation engine is a strict, ordered pipeline of pure functions. Each stage either passes the call forward or returns a final decision. No stage may skip another.

```
Receive MCP Request
    → Auth/JWT Validation          [401 if missing or invalid]
    → Session Load & Fingerprint   [error if policy changed]
    → may_use Whitelist Check      [deny if tool not in allowed list]
    → Budget Evaluator             [deny if cap reached]
    → Taint Evaluator              [deny if tainted and not cleared]
    → Sequence Evaluator           [deny if sequence violated]
    → Escalation Router            [pause if policy says escalate]
    → Emit Decision
    → Append Signed Audit Record
    → Forward to Upstream MCP Server
```

If any stage returns a non-allow decision, execution halts immediately. Nothing downstream runs.

### Rule 5: Policy Bound at Session Birth

When a session is created, `session.PolicyFingerprint = sha256(canonical_policy_yaml)`. On every subsequent request to that session, if the active policy fingerprint differs, return:

```json
{"error": "policy_changed", "message": "Policy was updated after this session was created. Start a new session to continue under the updated policy.", "old_fingerprint": "...", "new_fingerprint": "..."}
```

The **hard** option is correct. Design partners can handle session renewal. Silent re-evaluation under a different policy creates audit gaps.

### Rule 6: Shadow Mode Before Block Mode

Every policy rule supports `enforcement_mode: shadow`. In shadow mode, TrueBearing logs the violation and signs the audit record, but **allows the call to proceed**. This is the only safe way to onboard a new design partner. They run shadow for a week, watch `truebearing audit query`, prove the policy captures their intent, then flip to `enforcement_mode: block`.

---

## 4. The Build Order

The POC proved that building the engine first and the CLI as an afterthought produces "works on my machine." The correct order is outside-in.

```
CLI Design
    → Policy DSL Specification
        → Wire Protocol (MCP parsing)
            → Engine Pipeline
                → Integration Tests
                    → SDKs
```

**Why this order:**
- The CLI defines the operator contract. Every engine capability must be expressible via a CLI command.
- The DSL defines what the engine must evaluate. Engine structs are derived from parser output, not invented independently.
- The wire protocol defines what raw data enters the engine. The engine's input types are the wire protocol's output types.
- Tests are written against the engine once all inputs are defined.
- SDKs wrap the wire protocol. They are last because they have no new logic — they are transport wrappers.

---

## 5. Phase 1 — CLI Skeleton & Cryptographic Identity

**Goal:** Create the operator-facing shell. Prove the trust model. No evaluation logic yet — every command is a well-structured stub that prints `[not yet implemented]`.

**Delivers:** A binary that design partners can hold. A trust model that cannot be weakened later.

### 1.1 Repository Structure

```
truebearing/
├── cmd/
│   ├── main.go
│   ├── serve.go
│   ├── simulate.go
│   ├── policy/
│   │   ├── validate.go
│   │   ├── diff.go
│   │   ├── explain.go
│   │   └── lint.go
│   ├── audit/
│   │   ├── verify.go
│   │   ├── replay.go
│   │   └── query.go
│   ├── session/
│   │   ├── list.go
│   │   ├── inspect.go
│   │   └── terminate.go
│   ├── escalation/
│   │   ├── list.go
│   │   ├── approve.go
│   │   └── reject.go
│   └── agent/
│       ├── register.go
│       └── list.go
├── internal/
│   ├── identity/      # JWT minting, validation, Ed25519 keys
│   ├── policy/        # YAML parser, linter, fingerprinter
│   ├── session/       # Session store, sequence log
│   ├── engine/        # Evaluation pipeline
│   ├── proxy/         # MCP HTTP/stdio listeners
│   ├── audit/         # Signing, verification, querying
│   ├── escalation/    # Escalation state machine
│   ├── budget/        # Cost and call-count accumulators
│   └── store/         # SQLite DAL
├── pkg/
│   └── mcpparse/      # MCP JSON-RPC wire format parser (public, importable)
├── policy-packs/
│   ├── fintech/
│   ├── healthcare/
│   └── devops/
├── testdata/
│   └── traces/        # Captured MCP session traces for simulate tests
├── go.mod
├── go.sum
└── truebearing.policy.yaml  # Example policy for the README
```

### 1.2 CLI Framework

Use `github.com/spf13/cobra` for command routing. Use `github.com/spf13/viper` for configuration file management (`~/.truebearing/config.yaml` and per-project `.truebearing.yaml`). All commands accept `--policy <path>` and `--db <path>` overrides.

### 1.3 Cryptographic Identity (Fix 1)

`truebearing agent register <name>` must be the first piece of logic that actually does something, because it underpins everything else.

**Key generation:**
```go
// internal/identity/keypair.go
// Generate an Ed25519 keypair. Store private key in ~/.truebearing/keys/<name>.pem
// Store public key in ~/.truebearing/keys/<name>.pub.pem
// Both files are 0600 permissions.
func GenerateKeypair(name string) (ed25519.PublicKey, ed25519.PrivateKey, error)
```

**JWT claims structure:**
```go
type AgentClaims struct {
    jwt.RegisteredClaims
    AgentName        string   `json:"agent"`
    PolicyFile       string   `json:"policy_file"`
    AllowedTools     []string `json:"allowed_tools"`      // populated from may_use at registration time
    ParentAgent      string   `json:"parent_agent"`       // empty string for root agents
    ParentAllowed    []string `json:"parent_allowed"`     // parent's allowed_tools, for delegation check
    IssuedByProxy    string   `json:"issued_by"`          // proxy instance ID
}
```

**Delegation enforcement via JWT claims:**  
When Agent A spawns Agent B, A passes its JWT claims to B's registration call. The proxy validates that `B.AllowedTools ⊆ A.AllowedTools`. Child agents cannot claim tools not in the parent's JWT. This makes the delegation check a set intersection, not a policy lookup — it is O(n) at request time with no database read required.

**No JWT = 401. No exceptions. No override flags.**

### 1.4 Embedded SQLite Setup

On first run (any command that touches state), initialize `~/.truebearing/truebearing.db`:

```sql
-- Sessions
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    agent_name      TEXT NOT NULL,
    policy_fingerprint TEXT NOT NULL,
    tainted         INTEGER NOT NULL DEFAULT 0,  -- bool
    tool_call_count INTEGER NOT NULL DEFAULT 0,
    estimated_cost_usd REAL NOT NULL DEFAULT 0.0,
    created_at      INTEGER NOT NULL,  -- unix nano
    last_seen_at    INTEGER NOT NULL,
    terminated      INTEGER NOT NULL DEFAULT 0
);

-- Per-session ordered event log (the sequence engine's source of truth)
CREATE TABLE IF NOT EXISTS session_events (
    seq             INTEGER NOT NULL,  -- monotonically increasing, scoped to session_id
    session_id      TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    arguments_json  TEXT,
    decision        TEXT NOT NULL,     -- allow | deny | escalate | shadow_deny
    policy_rule     TEXT,              -- which rule triggered the decision
    recorded_at     INTEGER NOT NULL,  -- unix nano
    PRIMARY KEY (session_id, seq),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

-- Signed audit log (append-only; never UPDATE or DELETE)
CREATE TABLE IF NOT EXISTS audit_log (
    id              TEXT PRIMARY KEY,  -- uuid
    session_id      TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    tool_name       TEXT NOT NULL,
    arguments_sha256 TEXT NOT NULL,
    decision        TEXT NOT NULL,
    policy_fingerprint TEXT NOT NULL,
    agent_jwt_sha256 TEXT NOT NULL,
    client_trace_id TEXT,             -- W3C traceparent or vendor trace ID extracted from request headers; see §9.1a
    recorded_at     INTEGER NOT NULL,
    signature       TEXT NOT NULL     -- ed25519 base64 over canonical JSON of all above fields
);

-- Registered agents
CREATE TABLE IF NOT EXISTS agents (
    name            TEXT PRIMARY KEY,
    public_key_pem  TEXT NOT NULL,
    policy_file     TEXT NOT NULL,
    allowed_tools_json TEXT NOT NULL,
    registered_at   INTEGER NOT NULL,
    jwt_preview     TEXT NOT NULL     -- first 32 chars of issued JWT for display
);

-- Escalations
CREATE TABLE IF NOT EXISTS escalations (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    tool_name       TEXT NOT NULL,
    arguments_json  TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending | approved | rejected
    reason          TEXT,
    created_at      INTEGER NOT NULL,
    resolved_at     INTEGER
);
```

**WAL mode must be set on every open:**
```go
db.Exec("PRAGMA journal_mode=WAL")
db.Exec("PRAGMA foreign_keys=ON")
db.Exec("PRAGMA synchronous=NORMAL")
```

---

## 6. Phase 2 — Policy DSL & Parser

**Goal:** Define the language operators use. Build a parser that produces typed Go structs. Build the linter and fingerprinter. Nothing evaluates yet — parse only.

**Delivers:** `truebearing policy validate`, `truebearing policy lint`, `truebearing policy explain`, `truebearing policy diff`. Design partners can author policies and get feedback before the engine exists.

### 6.1 Full DSL Schema

```yaml
# truebearing.policy.yaml
version: "1"
agent: finance-bot

# shadow: log violations, allow calls — for onboarding and testing
# block:  deny on any violation — for production
enforcement_mode: shadow  # default; change to block for production

session:
  max_history: 1000          # hard cap; error when reached, no eviction
  max_duration_seconds: 3600 # optional; expire session after N seconds

budget:
  max_tool_calls: 50
  max_cost_usd: 5.00
  # soft_alert_at_pct: 80   # post-MVP: emit a warning at 80% consumption

# The canonical list of tools this agent is permitted to call.
# Any tool not in this list is denied before any other check runs.
may_use:
  - read_invoice
  - verify_invoice
  - manager_approval
  - execute_wire_transfer
  - read_external_email
  - run_compliance_scan
  - check_escalation_status   # TrueBearing injects this virtual tool automatically

tools:
  execute_wire_transfer:
    enforcement_mode: block   # tool-level override; escalates or blocks regardless of global mode

    sequence:
      # This tool may only be called after ALL of the following have fired in this session.
      only_after:
        - verify_invoice
        - manager_approval

      # This tool may never be called if ANY of the following fired in this session.
      never_after:
        - read_external_email   # if untrusted email was read, block wire transfer

      # Minimum number of times verify_invoice must have been called before this tool runs.
      # Useful for multi-invoice batches.
      requires_prior_n:
        tool: verify_invoice
        count: 1

    escalate_when:
      # Escalate to human if amount_usd exceeds this threshold.
      # TrueBearing extracts this from the tool call's JSON arguments using the given path.
      argument_path: "$.amount_usd"
      operator: ">"
      value: 10000

  read_external_email:
    taint:
      # Reading untrusted external email taints the session.
      # Any tool with never_when: session_tainted will be blocked until taint is cleared.
      applies: true
      label: "external_email_read"

  run_compliance_scan:
    taint:
      # Calling this tool explicitly clears the taint applied by read_external_email.
      clears: true

  # A tool with no sub-keys is allowed with no restrictions beyond may_use and budget.
  read_invoice: {}
  verify_invoice: {}
  manager_approval: {}
```

### 6.2 Go Types (from Parser Output)

```go
// internal/policy/types.go

type Policy struct {
    Version         string          `yaml:"version"`
    Agent           string          `yaml:"agent"`
    EnforcementMode EnforcementMode `yaml:"enforcement_mode"`
    Session         SessionPolicy   `yaml:"session"`
    Budget          BudgetPolicy    `yaml:"budget"`
    MayUse          []string        `yaml:"may_use"`
    Tools           map[string]ToolPolicy `yaml:"tools"`

    // Derived at parse time, not in YAML
    Fingerprint     string // sha256 of canonical YAML bytes
    SourcePath      string // path the file was loaded from
}

type EnforcementMode string
const (
    EnforcementShadow EnforcementMode = "shadow"
    EnforcementBlock  EnforcementMode = "block"
)

type SessionPolicy struct {
    MaxHistory         int `yaml:"max_history"`
    MaxDurationSeconds int `yaml:"max_duration_seconds"`
}

type BudgetPolicy struct {
    MaxToolCalls int     `yaml:"max_tool_calls"`
    MaxCostUSD   float64 `yaml:"max_cost_usd"`
}

type ToolPolicy struct {
    EnforcementMode EnforcementMode `yaml:"enforcement_mode"` // tool-level override
    Sequence        SequencePolicy  `yaml:"sequence"`
    Taint           TaintPolicy     `yaml:"taint"`
    EscalateWhen    *EscalateRule   `yaml:"escalate_when"`
}

type SequencePolicy struct {
    OnlyAfter     []string       `yaml:"only_after"`
    NeverAfter    []string       `yaml:"never_after"`
    RequiresPriorN *PriorNRule   `yaml:"requires_prior_n"`
}

type PriorNRule struct {
    Tool  string `yaml:"tool"`
    Count int    `yaml:"count"`
}

type TaintPolicy struct {
    Applies bool   `yaml:"applies"`
    Label   string `yaml:"label"`
    Clears  bool   `yaml:"clears"`
}

type EscalateRule struct {
    ArgumentPath string      `yaml:"argument_path"` // JSONPath expression
    Operator     string      `yaml:"operator"`      // >, <, >=, <=, ==, !=, contains, matches
    Value        interface{} `yaml:"value"`
}
```

### 6.3 Policy Fingerprinting

```go
// internal/policy/fingerprint.go
// Canonical fingerprint: sort YAML keys, marshal to JSON, sha256.
// This must be deterministic across machines and Go versions.
func Fingerprint(p *Policy) string {
    // Marshal to canonical JSON (sorted keys), then sha256.
    // Store result in p.Fingerprint.
}
```

### 6.4 Linter Rules

`truebearing policy lint` must warn on all of the following:

| Code | Severity | Condition |
|---|---|---|
| `L001` | ERROR | `may_use` is empty or missing |
| `L002` | ERROR | A tool in `tools:` is not listed in `may_use` |
| `L003` | ERROR | `only_after` references a tool not in `may_use` |
| `L004` | ERROR | `never_after` references a tool not in `may_use` |
| `L005` | WARNING | `enforcement_mode` is missing (defaults to shadow; warn so operators know) |
| `L006` | WARNING | No `budget` block defined |
| `L007` | WARNING | `max_history` is not set (defaults to 1000) |
| `L008` | WARNING | A tool with `escalate_when` has no escalation channel configured |
| `L009` | INFO | `enforcement_mode: shadow` — reminder that violations are logged but not blocked |
| `L010` | ERROR | `requires_prior_n.count` is zero or negative |
| `L011` | WARNING | A tool has `taint.applies: true` but no tool has `taint.clears: true` — session can never be untainted |
| `L012` | ERROR | `escalate_when.operator` is not one of the valid operators |
| `L013` | ERROR | Circular sequence dependency detected: tool A has `only_after: [B]` and tool B has `only_after: [A]` — the agent will permanently deadlock |

**L013 implementation note:** The linter must build a Directed Acyclic Graph (DAG) from all `only_after` and `never_after` relationships across all tools in `may_use`. Use a standard DFS cycle detection algorithm (Kahn's algorithm or recursive DFS with a visited/in-stack set). If a cycle is detected, report the full cycle path in the error message:

```
L013 [ERROR] Circular sequence dependency: execute_payment → only_after → verify_invoice → only_after → execute_payment
             This session can never satisfy both constraints simultaneously. The agent will permanently block.
```

This check runs on the full graph, not just pairs — cycles of length 3+ (A→B→C→A) must also be caught.

---

## 7. Phase 3 — Wire Protocol & MCP Proxy Shell

**Goal:** Catch the traffic. Parse it correctly. Forward it untouched. No evaluation yet.

**Delivers:** `truebearing serve` starts a listening proxy. Traffic flows through. Nothing is blocked yet. End-to-end connectivity is proven.

### 7.1 MCP Wire Format

MCP uses JSON-RPC 2.0 over HTTP (`Content-Type: application/json`) or stdio. The proxy must handle both.

**HTTP transport (primary for MVP):**

```
POST /mcp/v1
Authorization: Bearer <agent_jwt>
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": "req_001",
  "method": "tools/call",
  "params": {
    "name": "execute_wire_transfer",
    "arguments": {
      "vendor_id": "v_123",
      "amount_usd": 15000.00,
      "invoice_ref": "INV-2025-441"
    }
  }
}
```

**Parsed struct:**

```go
// pkg/mcpparse/types.go
type MCPRequest struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
}

type ToolCallParams struct {
    Name      string          `json:"name"`
    Arguments json.RawMessage `json:"arguments"`
}

// IsTool returns true if method == "tools/call"
func (r *MCPRequest) IsTool() bool { return r.Method == "tools/call" }
```

Only `method: "tools/call"` is intercepted and evaluated. All other MCP methods (`tools/list`, `initialize`, `ping`) are forwarded directly to the upstream without evaluation.

### 7.1a Session ID Protocol

MCP over HTTP has no native session concept. Individual `tools/call` requests carry no implicit session context. The proxy must establish this contract explicitly.

**Required header on every `tools/call` request:**
```
X-TrueBearing-Session-ID: <uuidv4>
```

**Engine rule:** If a `tools/call` arrives without this header, the proxy returns immediately:
```json
{"error": "missing_session_id", "message": "X-TrueBearing-Session-ID header is required on all tools/call requests.", "code": 400}
```
No evaluation runs. No audit record is written for headerless requests (there is no session to attach it to).

**Session creation:** If the header is present but no matching row exists in `sessions`, the proxy creates the session on the first call — setting `policy_fingerprint`, `created_at`, and `last_seen_at`. This is the implicit session creation model; no explicit "start session" call is required from the agent.

**SDK responsibility:** The Python and Node `PolicyProxy` SDKs generate a `uuid.uuid4()` / `crypto.randomUUID()` when the `PolicyProxy` object is instantiated and inject `X-TrueBearing-Session-ID` into every outbound request automatically. Operators never touch this header directly unless they are managing session lifecycle themselves (e.g., to explicitly continue a session across multiple agent runs).

```python
# internal SDK behavior — operator never writes this
class PolicyProxy:
    def __init__(self, client, policy):
        self._session_id = str(uuid.uuid4())  # one session per PolicyProxy instance
        self._policy = policy
        # injects X-TrueBearing-Session-ID on every request
```

If an operator wants to resume a prior session (e.g., a long-running workflow across multiple Python processes), they pass `session_id` explicitly:
```python
client = PolicyProxy(anthropic.Anthropic(), policy='./policy.yaml', session_id='sess_abc123')
```

**`truebearing serve` flag addition:**
```
--require-session-header   Enforce X-TrueBearing-Session-ID (default: true; set false only for local dev)
```

### 7.2 Proxy Architecture

```
[Agent / LLM Framework]
        |
        | HTTP POST /mcp/v1
        ↓
[TrueBearing Proxy — net/http listener]
        |
        ├── [Auth Middleware]        reads Authorization: Bearer, validates JWT
        ├── [Request Parser]         reads body, parses JSON-RPC
        ├── [Intercept Decision]     is this a tools/call? yes → engine pipeline
        │                            no → forward immediately
        ├── [Engine Pipeline]        (Phase 4)
        │         ↓
        │    Decision: allow | deny | escalate
        │         ↓
        ├── [Audit Writer]           sign and append to SQLite audit_log
        │
        ├── On allow:  forward to upstream MCP server, stream response back
        ├── On deny:   return synthetic JSON-RPC error response to caller
        └── On escalate: write escalation to DB, return synthetic "paused" response
```

### 7.3 Upstream Configuration

`truebearing serve --upstream https://mcp.acme.internal --policy ./policy.yaml --port 7773`

The proxy is a pure reverse proxy for non-tool traffic. Use `httputil.ReverseProxy` from stdlib. Override `Director` to add the upstream target. Do not use third-party proxy libraries for the MVP.

### 7.4 Asynchronous Escalation (Critical Design Decision)

LLM provider HTTP clients (OpenAI SDK, Anthropic SDK) have request timeouts of 60–120 seconds. Human escalation reviewers take minutes to hours. The proxy **must not** hold the HTTP connection open while awaiting human approval.

**Resolution:**

When policy evaluates to `escalate`:
1. Write the escalation to the `escalations` table with `status: pending`.
2. Immediately return a synthetic JSON-RPC **success** response to the caller:

```json
{
  "jsonrpc": "2.0",
  "id": "req_001",
  "result": {
    "content": [{
      "type": "text",
      "text": "{\"status\": \"escalated\", \"escalation_id\": \"esc_abc123\", \"message\": \"This action requires human approval. Call check_escalation_status with this ID to poll for a decision.\"}"
    }]
  }
}
```

3. TrueBearing injects a virtual tool `check_escalation_status` into the agent's tool schema on every `tools/list` response. This tool is not forwarded to the upstream — it is handled entirely by TrueBearing.
4. The LLM framework's agent will call `check_escalation_status({"escalation_id": "esc_abc123"})` on its next tool call. TrueBearing intercepts this, queries the `escalations` table, and returns `pending | approved | rejected`.
5. When an operator runs `truebearing escalation approve esc_abc123`, the status transitions to `approved`. The next `check_escalation_status` poll returns `approved`, and the agent can retry the original tool call (which will now pass the escalation check because the approval is recorded in the session history).

This design means the agent's run loop never crashes due to a timeout. The cost is one extra tool call roundtrip per escalation.

---

## 8. Phase 4 — The Evaluation Engine

**Goal:** The core business logic. Each evaluator is a pure function. Each is independently testable.

**Delivers:** `truebearing serve` now actually enforces policy. Every evaluator has unit tests before it is wired in.

### 8.1 The Evaluator Interface

```go
// internal/engine/evaluator.go

type Decision struct {
    Action      Action  // Allow, Deny, Escalate, ShadowDeny
    Reason      string  // human-readable explanation
    RuleID      string  // which policy rule triggered this
    SessionTainted bool
}

type Action string
const (
    Allow      Action = "allow"
    Deny       Action = "deny"
    Escalate   Action = "escalate"
    ShadowDeny Action = "shadow_deny" // enforcement_mode: shadow
)

// Each evaluator in the pipeline satisfies this interface.
type Evaluator interface {
    Evaluate(ctx context.Context, call *ToolCall, session *session.Session, policy *policy.Policy) (Decision, error)
}

// ToolCall is the engine's internal representation of an intercepted call.
type ToolCall struct {
    SessionID    string
    AgentName    string
    ToolName     string
    Arguments    json.RawMessage
    ArgumentsMap map[string]interface{} // parsed once, reused across evaluators
    RequestedAt  time.Time
}
```

### 8.2 Evaluator 1 — MayUse Whitelist

```go
// internal/engine/mayuse.go
// If the tool is not in policy.MayUse, deny immediately.
// This is the fastest check and runs first.
```

### 8.3 Evaluator 2 — Budget

```go
// internal/engine/budget.go
// Load session.ToolCallCount and session.EstimatedCostUSD from SQLite.
// Check against policy.Budget.MaxToolCalls and policy.Budget.MaxCostUSD.
// If either is exceeded, deny.
//
// Cost estimation: use a configurable token cost table.
// For MVP: flat $0.001 per tool call as a conservative estimate.
// Post-MVP: parse the upstream MCP response for actual token counts.
```

### 8.4 Evaluator 3 — Taint

```go
// internal/engine/taint.go
//
// Taint check logic:
// 1. If session.Tainted == true AND the tool's policy has no explicit clearance...
//    AND the tool has no sequence predicate that explicitly requires session_untainted...
//    Check if this tool has a sequence.never_after entry that matches any taint-applying tool.
//    If so, deny.
//
// 2. If the called tool has taint.clears == true AND session is currently tainted:
//    Allow the call, then mark session.Tainted = false.
//
// 3. After an allow decision for a tool with taint.applies == true:
//    Mark session.Tainted = true.
//
// Taint state transitions happen AFTER the allow decision is emitted and the
// audit record is written. The tool call that taints the session is itself allowed.
```

### 8.5 Evaluator 4 — Sequence

```go
// internal/engine/sequence.go
//
// Query: SELECT tool_name, seq FROM session_events
//        WHERE session_id = ? AND decision IN ('allow', 'shadow_deny')
//        ORDER BY seq ASC
//
// only_after check:
//   For each tool T in policy.OnlyAfter:
//     If T does not appear in session history with a lower seq than the current call → deny
//
// never_after check:
//   For each tool T in policy.NeverAfter:
//     If T appears anywhere in session history → deny
//
// requires_prior_n check:
//   Count occurrences of policy.RequiresPriorN.Tool in session history.
//   If count < policy.RequiresPriorN.Count → deny
//
// All three checks run even if one fails. Collect all violations and return the
// full list in the deny reason. Operators need to know all the things wrong,
// not just the first one.
```

### 8.6 Evaluator 5 — Escalation

```go
// internal/engine/escalation.go
//
// If the tool has an escalate_when rule:
//   Extract the value at ArgumentPath from call.ArgumentsMap using a JSONPath evaluator.
//   Apply the operator comparison.
//   If condition is true:
//     Check if there is already an 'approved' escalation for this session + tool + argument hash.
//     If yes: allow (the human already approved this specific call).
//     If no:  return Escalate decision.
```

### 8.7 Wiring the Pipeline

```go
// internal/engine/pipeline.go

var defaultPipeline = []Evaluator{
    &MayUseEvaluator{},
    &BudgetEvaluator{},
    &TaintEvaluator{},
    &SequenceEvaluator{},
    &EscalationEvaluator{},
}

func (p *Pipeline) Evaluate(ctx context.Context, call *ToolCall, sess *session.Session, pol *policy.Policy) Decision {
    for _, evaluator := range p.stages {
        decision, err := evaluator.Evaluate(ctx, call, sess, pol)
        if err != nil {
            // evaluation error → fail closed
            return Decision{Action: Deny, Reason: fmt.Sprintf("evaluator error: %v", err)}
        }
        if decision.Action != Allow {
            // respect tool-level enforcement_mode override
            if effectiveMode(pol, call.ToolName) == policy.EnforcementShadow {
                decision.Action = ShadowDeny
            }
            return decision
        }
    }
    return Decision{Action: Allow}
}
```

---

## 9. Phase 5 — Evidence, Audit & Simulation DX

**Goal:** Produce the artifacts that make TrueBearing valuable to compliance-conscious design partners. Make the system observable and replayable.

**Delivers:** `truebearing audit verify`, `truebearing audit query`, `truebearing audit replay`, `truebearing simulate`.

### 9.1 Audit Record Structure

Every decision (including `allow` decisions) is signed and written to `audit_log`.

```go
// internal/audit/record.go

type AuditRecord struct {
    ID               string `json:"id"`                // uuid v4
    SessionID        string `json:"session_id"`
    Seq              uint64 `json:"seq"`
    AgentName        string `json:"agent_name"`
    ToolName         string `json:"tool_name"`
    ArgumentsSHA256  string `json:"arguments_sha256"`  // sha256 of raw arguments JSON
    Decision         string `json:"decision"`
    DecisionReason   string `json:"decision_reason"`
    PolicyFingerprint string `json:"policy_fingerprint"`
    AgentJWTSHA256   string `json:"agent_jwt_sha256"`  // sha256 of the bearer token
    ClientTraceID    string `json:"client_trace_id,omitempty"` // W3C traceparent or vendor trace ID; see §9.1a
    RecordedAt       int64  `json:"recorded_at"`        // unix nanoseconds
    Signature        string `json:"signature"`          // base64 ed25519 over canonical JSON of above fields
}
```

### 9.1a Observability Correlation (Trace ID Passthrough)

Design partners running LangSmith, Datadog, Helicone, or Grafana will observe a blocked tool call in their LLM trace UI and need to find the corresponding TrueBearing decision without any manual correlation work. This requires zero-config cross-system traceability.

**The approach:** The proxy reads standard distributed tracing headers on every inbound request and stores the value verbatim in `audit_log.client_trace_id`. No OTel exporter is required for the MVP — the correlation link is the trace ID itself, which both systems already have.

**Header priority (first match wins):**
```go
// internal/proxy/traceheaders.go

var tracingHeaders = []string{
    "traceparent",           // W3C standard — LangSmith, Jaeger, Honeycomb, most modern stacks
    "x-datadog-trace-id",    // Datadog
    "x-cloud-trace-context", // Google Cloud Trace
    "x-amzn-trace-id",       // AWS X-Ray
    "x-b3-traceid",          // Zipkin B3 (LangChain default in some configs)
}

func ExtractClientTraceID(headers http.Header) string {
    for _, h := range tracingHeaders {
        if v := headers.Get(h); v != "" {
            return fmt.Sprintf("%s=%s", h, v) // preserve header name for disambiguation
        }
    }
    return "" // omitempty: not included in audit record if absent
}
```

**Result:** An engineer at End Close opens a blocked call in Datadog, copies the `x-datadog-trace-id`, and runs:
```sh
truebearing audit query --trace-id "x-datadog-trace-id=abc123"
```
They immediately see the exact `deny` record, the policy rule that triggered it, and the session state at that moment. Zero configuration on their side — they get this for free the moment they deploy TrueBearing into a stack that already emits trace headers.

**`ClientTraceID` participates in audit signatures.** It is included in the canonical JSON before signing. This means the correlation link is itself tamper-evident — an auditor can verify that the trace ID in the TrueBearing record matches the trace ID in the LLM observability tool for the same request.

**Signing:**
```go
// Canonical JSON: sort all keys alphabetically, no extra whitespace.
// Sign using the proxy's Ed25519 private key (stored in ~/.truebearing/keys/proxy.pem).
// Base64-encode the 64-byte signature.
```

**Verification:**
```go
// truebearing audit verify <logfile.jsonl>
// For each line: re-derive canonical JSON (excluding "signature" field),
// verify signature against the proxy's public key.
// Print: OK | TAMPERED for each record.
```

### 9.2 Simulation Engine

`truebearing simulate --trace <trace.jsonl> --policy <policy.yaml>`

A trace file is a newline-delimited JSON file of raw MCP tool call requests (the same format TrueBearing writes when `--capture-trace` is passed to `serve`). Simulate replays them in-memory against the given policy and prints a diff:

```
Session: sess_abc123
Policy:  fintech-payments@a8f9c2
────────────────────────────────────────────────────────────
 seq │ tool                    │ old_decision │ new_decision │ changed
─────┼─────────────────────────┼──────────────┼──────────────┼────────
   1 │ read_invoice             │ allow        │ allow        │
   2 │ verify_invoice           │ allow        │ allow        │
   3 │ execute_wire_transfer    │ allow        │ DENY         │ ◄ ──  sequence.only_after: manager_approval not satisfied
   4 │ manager_approval         │ allow        │ allow        │
────────────────────────────────────────────────────────────
Summary: 1 decision changed. 1 call that was previously allowed would now be DENIED.
```

Simulate never writes to the database and never contacts an upstream. It is a pure offline evaluation.

---

## 10. Phase 6 — SDKs & Integration Story

**Goal:** Make the 2-line integration real. Ship Python and Node.js SDKs that route through the proxy transparently.

**Delivers:** `pip install truebearing` and `npm install @mercator/truebearing`.

### 10.1 Python SDK

```python
# pip install truebearing

from truebearing import PolicyProxy
import anthropic

# Before:
# client = anthropic.Anthropic()

# After (2 lines):
from truebearing import PolicyProxy
client = PolicyProxy(anthropic.Anthropic(), policy='./truebearing.policy.yaml')

# Nothing else changes. The SDK:
# 1. Spawns `truebearing serve` as a subprocess on a random local port if not already running.
# 2. Injects the proxy URL as the base_url for the Anthropic client.
# 3. Reads the agent JWT from ~/.truebearing/keys/<agent_name>.jwt and injects it
#    as the Authorization header.
# 4. On KeyboardInterrupt, shuts down the subprocess cleanly.
```

The Python SDK also supports explicit proxy mode (for teams that run the proxy as a sidecar, not a subprocess):

```python
client = PolicyProxy(
    anthropic.Anthropic(),
    proxy_url='http://localhost:7773',
    agent_jwt=os.environ['TRUEBEARING_AGENT_JWT']
)
```

### 10.2 Node.js SDK

```typescript
// npm install @mercator/truebearing
import { PolicyProxy } from '@mercator/truebearing';
import Anthropic from '@anthropic-ai/sdk';

const client = new PolicyProxy(new Anthropic(), {
  policy: './truebearing.policy.yaml'
});
```

### 10.3 Framework Adapters (Post-MVP prep)

For LangGraph and CrewAI, the integration is the same — the proxy intercepts at the MCP transport layer, not at the framework layer. No framework-specific adapter is required. The only requirement is that the framework sends MCP-formatted tool calls (which LangGraph, CrewAI, OpenAI Agents SDK, and all MCP-native clients do natively).

For LangChain's older function calling (not MCP-native), provide a thin `MercatorLLM` wrapper that converts LangChain tool calls to MCP format before proxying.

---

## 11. Data Models & Schemas

### 11.1 Session State Transitions

```
Created
    │
    ├── tool call arrives
    │       │
    │       ├── allow     → seq increments, counters update, taint may change
    │       ├── deny      → audit record written, call blocked, session remains active
    │       ├── shadow_deny → audit record written, call allowed, session remains active
    │       └── escalate  → escalation written, synthetic response returned
    │
    ├── budget.max_tool_calls reached → Exhausted (soft terminate; log error; demand new session)
    ├── session.max_history reached   → HistoryCapped (error; demand new session)
    ├── session.max_duration exceeded → Expired (auto-terminate)
    └── truebearing session terminate → Terminated (hard terminate)
```

### 11.2 Evidence Object (JSON export format for auditors)

```json
{
  "evidence_id": "ev_01J8X...",
  "schema_version": "1",
  "session_id": "sess_abc123",
  "seq": 3,
  "agent": "finance-bot",
  "tool": "execute_wire_transfer",
  "arguments_sha256": "e3b0c44...",
  "decision": "deny",
  "decision_reason": "sequence.only_after: manager_approval not satisfied (not in session history)",
  "policy": {
    "fingerprint": "a8f9c2...",
    "source": "fintech-payments.policy.yaml",
    "enforcement_mode": "block"
  },
  "session_state": {
    "tainted": false,
    "tool_call_count": 3,
    "estimated_cost_usd": 0.003
  },
  "agent_jwt_sha256": "f4d2...",
  "recorded_at": "2026-02-27T14:22:11.334Z",
  "signature": "base64_ed25519_signature"
}
```

---

## 12. The Policy DSL — Full Specification

### Predicate Reference

| Predicate | Type | Description |
|---|---|---|
| `only_after: [T1, T2]` | Sequence | All listed tools must appear in session history before this tool may run |
| `never_after: [T1]` | Sequence | If any listed tool appears in session history, this tool is blocked |
| `requires_prior_n: {tool: T, count: N}` | Sequence | Tool T must appear at least N times in session history |
| `taint.applies: true` | Taint | Calling this tool taints the session |
| `taint.clears: true` | Taint | Calling this tool clears the session taint |
| `escalate_when` | Escalation | Escalate to human if argument condition is true |

### Enforcement Mode Hierarchy

Tool-level `enforcement_mode` overrides the global `enforcement_mode`. Global `enforcement_mode` is the default for all tools that do not specify their own.

| Global | Tool-Level | Effective |
|---|---|---|
| shadow | *(none)* | shadow |
| shadow | block | block |
| block | *(none)* | block |
| block | shadow | shadow |

### Reserved Tool Names (TrueBearing-injected)

These tools are synthesized by TrueBearing and must not appear in `may_use` or `tools`:

| Tool Name | Purpose |
|---|---|
| `check_escalation_status` | Agent polls this to check if an escalation was approved or rejected |

---

## 13. The CLI Surface — Full Specification

### `truebearing serve`

```
truebearing serve [flags]

Flags:
  --policy <path>        Policy YAML file to load (default: ./truebearing.policy.yaml)
  --upstream <url>       Upstream MCP server URL (required)
  --port <int>           Local port to listen on (default: 7773)
  --db <path>            SQLite database path (default: ~/.truebearing/truebearing.db)
  --capture-trace <path> Write all MCP traffic to a JSONL trace file for later simulation
  --stdio                Accept MCP requests on stdin/stdout instead of HTTP
```

### `truebearing simulate`

```
truebearing simulate --trace <file> --policy <file> [--old-policy <file>]

Replay a captured session trace against a policy. If --old-policy is provided,
shows a diff between old and new policy decisions for each call.

Output: colored table showing each call, old decision, new decision, and reason for change.
```

### `truebearing policy validate`

```
truebearing policy validate <file>

Parse and validate a policy YAML file. Print errors and exit non-zero if invalid.
Suitable for use in CI.
```

### `truebearing policy diff <old> <new>`

```
truebearing policy diff old.policy.yaml new.policy.yaml

Show which rules changed between two policy files in a structured format.
Highlights: added tools, removed tools, changed sequence predicates, changed budget, mode changes.
```

### `truebearing policy explain <file>`

```
truebearing policy explain <file>

Print a plain-English summary of what a policy enforces.
Example output:

  Agent: finance-bot
  Mode: BLOCK (violations are denied)
  Allowed tools (7): read_invoice, verify_invoice, manager_approval, ...
  Budget: 50 tool calls / $5.00 per session

  Sequence guards:
    execute_wire_transfer: may only run after [verify_invoice, manager_approval]
    execute_wire_transfer: blocked if read_external_email was called this session

  Taint rules:
    read_external_email: taints the session
    run_compliance_scan: clears the taint

  Escalation rules:
    execute_wire_transfer: escalate to human if amount_usd > 10000
```

### `truebearing policy lint <file>`

```
truebearing policy lint <file>

Warn on common policy mistakes. See linter rule table in Phase 2.
Exit code 0 if no errors; exit code 1 if any ERRORs; exit code 0 with warnings printed if only WARNINGs.
```

### `truebearing audit verify <file>`

```
truebearing audit verify audit.jsonl

Verify Ed25519 signatures on every record in an audit log file.
Prints OK or TAMPERED per line. Exits non-zero if any record fails.
```

### `truebearing audit replay <file> --policy <file>`

```
truebearing audit replay audit.jsonl --policy current.policy.yaml

Re-run an audit log through a (potentially different) policy.
Shows which decisions would change. Useful for retroactive policy analysis.
```

### `truebearing audit query`

```
truebearing audit query [flags]

Flags:
  --session <id>    Filter by session ID
  --tool <name>     Filter by tool name
  --decision <d>    Filter by decision (allow, deny, shadow_deny, escalate)
  --trace-id <id>   Filter by client trace ID (matches W3C traceparent, x-datadog-trace-id, etc.)
  --from <time>     Start of time range (RFC3339)
  --to <time>       End of time range (RFC3339)
  --format <fmt>    Output format: table (default), json, csv
```

### `truebearing session list`

```
truebearing session list

Show all active sessions with:
  - Session ID
  - Agent name
  - Policy fingerprint (short)
  - Taint status
  - Tool call count / max
  - Estimated cost / budget
  - Age
```

### `truebearing session inspect <id>`

```
truebearing session inspect sess_abc123

Full history for a session: every tool call, decision, reason, timestamp, sequence number.
```

### `truebearing session terminate <id>`

```
truebearing session terminate sess_abc123

Force-expire a session. Any subsequent tool calls with this session ID will receive a 410 Gone.
```

### `truebearing escalation list`

```
truebearing escalation list [--status pending|approved|rejected]

List escalations with: ID, session, tool, argument preview, status, age.
```

### `truebearing escalation approve <id> [--note "reason"]`

```
truebearing escalation approve esc_abc123 --note "Verified with CFO directly"

Approve an escalation. The next check_escalation_status call from the agent will return approved.
```

### `truebearing escalation reject <id> --reason "reason"`

```
truebearing escalation reject esc_abc123 --reason "Amount exceeds daily limit; requires board approval"
```

### `truebearing agent register <name>`

```
truebearing agent register finance-bot --policy ./truebearing.policy.yaml

Generate an Ed25519 keypair for the named agent.
Issue a signed JWT bound to the policy.
Output:
  Agent: finance-bot
  Public key: ~/.truebearing/keys/finance-bot.pub.pem
  JWT written to: ~/.truebearing/keys/finance-bot.jwt
  Allowed tools (from policy may_use): [read_invoice, verify_invoice, ...]

  To use:
    export TRUEBEARING_AGENT_JWT=$(cat ~/.truebearing/keys/finance-bot.jwt)
    OR pass --agent-jwt flag to your client
```

### `truebearing agent list`

```
truebearing agent list

Show all registered agents: name, registration date, policy file, tool count, JWT expiry.
```

---

## 14. The Three POC Fixes (Non-Negotiable)

These are not improvements. They are correctness requirements. Nothing is shipped to a design partner before all three are implemented.

### Fix 1 — Cryptographic Authentication

Agent identity is Ed25519-signed JWT. Not a header. Not a config value. Not an API key.

- `truebearing agent register <name>` generates the keypair and issues the JWT.
- Every request to the proxy must carry `Authorization: Bearer <jwt>`.
- The proxy validates the JWT signature on every request. A valid-looking but unsigned or wrongly-signed token is treated identically to a missing token: `401 Unauthorized`.
- The JWT carries `allowed_tools` claims. Delegation enforcement is a set intersection check at request time. No database lookup required.

### Fix 2 — Monotonic Sequence Numbers Without Eviction

`session_events.seq` is a `uint64` that starts at 1 and only increments. It is never reused. It is never reset within a session.

When `SELECT COUNT(*) FROM session_events WHERE session_id = ?` reaches `policy.Session.MaxHistory`, the session is **dead**. Any new tool call returns:

```json
{
  "error": "session_history_limit_reached",
  "message": "Session sess_abc123 has reached its maximum history of 1000 events. Start a new session to continue.",
  "session_id": "sess_abc123",
  "limit": 1000
}
```

There is no ring buffer. There is no eviction. There is no silent data loss.

### Fix 3 — Policy Binding at Session Creation

When a session is created (first tool call with no existing session record), the current policy fingerprint is written to `sessions.policy_fingerprint`.

On every subsequent request to that session:
```go
if activePolicyFingerprint != session.PolicyFingerprint {
    return http.StatusConflict, `{
        "error": "policy_changed",
        "message": "Policy was updated after this session was created. Start a new session.",
        "session_policy_fingerprint": session.PolicyFingerprint,
        "current_policy_fingerprint": activePolicyFingerprint
    }`
}
```

The hard option. No soft re-evaluation under a different policy. Design partners implement session renewal.

---

## 15. Production Deployment Model

### MVP: Local Binary (The Design Partner Model)

For the 90-day sprint to closed beta, TrueBearing runs locally on the design partner's machine or as a sidecar in their container. No cloud infrastructure is required.

```
# Install
brew install truebearing          # macOS
curl -sf https://truebearing.dev/install.sh | sh   # Linux

# Or: go install github.com/mercator-hq/truebearing@latest

# Initialize
truebearing agent register my-finance-agent --policy ./policy.yaml

# Start the proxy
truebearing serve --upstream https://mcp.acme.internal --policy ./policy.yaml

# In your application
export TRUEBEARING_AGENT_JWT=$(cat ~/.truebearing/keys/my-finance-agent.jwt)
export ANTHROPIC_BASE_URL=http://localhost:7773   # or use the Python SDK
```

**Everything is local.** The SQLite database is local. The signing keys are local. There is no external service dependency. This is intentional: design partners are protective of their production traffic. They will not route it through an unknown third-party service on day one. Local first builds trust.

### Post-MVP: Hosted Control Plane

Once design partners trust the binary, offer a hosted control plane for:
- Centralized policy management (push policy updates without restarting the proxy)
- Cross-instance audit log aggregation
- Team-based escalation routing (Slack/webhook/email notifications for pending escalations)
- Policy pack marketplace
- SOC 2 Type II compliance evidence export

The hosted control plane never sees tool call arguments (only hashes). The proxy signs audit records locally before uploading them. Trust architecture remains local.

### Distribution

- **Binary:** Go cross-compiled for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`. Published as GitHub Releases.
- **Docker:** `ghcr.io/mercator/truebearing:latest` — a distroless static binary image.
- **PyPI:** `pip install truebearing` — Python SDK that embeds the binary as a resource or downloads it on first use.
- **npm:** `npm install @mercator/truebearing` — same pattern.

### API Keys / Operator Authentication

For the local MVP, the proxy's admin API (session list, escalation approve, etc.) is accessible only on localhost with no authentication. This is acceptable because only the machine owner can reach it.

For the hosted control plane, operator API keys are Ed25519-signed long-lived tokens issued at signup. The pattern is identical to agent tokens but with operator claims instead of agent claims.

---

## 16. The Integration Story (The 2-Line Promise)

**From the perspective of an engineer at End Close (payments automation):**

My agents have write access to our payment processor and our ERP. I need to guarantee that `verify_invoice → approve → pay` is never violated. A double-pay is unrecoverable.

**Step 1: Install**
```sh
pip install truebearing
```

**Step 2: Write a policy (5 minutes)**
```yaml
# endclose.policy.yaml
version: "1"
agent: payments-agent
enforcement_mode: shadow  # start here; flip to block after a week of observation

budget:
  max_tool_calls: 100
  max_cost_usd: 1.00

may_use:
  - read_invoice
  - verify_invoice
  - manager_approval
  - execute_payment
  - check_escalation_status

tools:
  execute_payment:
    enforcement_mode: block  # always block this, regardless of global shadow mode
    sequence:
      only_after:
        - verify_invoice
        - manager_approval
    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 50000
```

**Step 3: Integrate (2 lines)**
```python
from truebearing import PolicyProxy
client = PolicyProxy(anthropic.Anthropic(), policy='./endclose.policy.yaml')
# Everything else is unchanged.
```

**Step 4: Observe for a week**
```sh
truebearing audit query --decision shadow_deny
```

**Step 5: Flip to production enforcement**
```yaml
enforcement_mode: block
```

**Total time to value:** Under 30 minutes. Zero agent code changes. The agent doesn't know TrueBearing exists.

---

## 17. Design Partner Use Cases

These are the specific policies each design partner cohort would write. They demonstrate the DSL's domain agnosticism.

### End Close (YC W26) — Payment Automation

```yaml
tools:
  execute_wire_transfer:
    sequence:
      only_after: [verify_invoice, manager_approval]
      never_after: [read_external_email]  # block if untrusted email was ingested
    escalate_when:
      argument_path: "$.amount_usd"
      operator: ">"
      value: 10000
  read_external_email:
    taint:
      applies: true
```

### LunaBill (YC F25) — Healthcare Billing

```yaml
may_use: [read_claim, verify_eligibility, submit_claim, read_patient_phi, check_escalation_status]
tools:
  submit_claim:
    sequence:
      only_after: [verify_eligibility, read_claim]
    escalate_when:
      argument_path: "$.claim_amount_usd"
      operator: ">"
      value: 5000
  read_patient_phi:
    taint:
      applies: true
      label: phi_accessed
  verify_phi_compliance:
    taint:
      clears: true
```

### Avallon AI (YC W26) — Insurance Claims

```yaml
budget:
  max_tool_calls: 200
  max_cost_usd: 2.00
tools:
  approve_claim:
    sequence:
      only_after: [ingest_claim, fraud_check, adjudicate]
    escalate_when:
      argument_path: "$.payout_usd"
      operator: ">"
      value: 25000
```

### Caseflood (YC-backed) — Legal Operations

```yaml
tools:
  send_document_external:
    sequence:
      never_after: [read_privileged_document]  # cannot exfiltrate privileged docs
  read_privileged_document:
    taint:
      applies: true
  run_privilege_review:
    taint:
      clears: true
```

### Ritivel (YC W26) — Life Sciences Regulatory

```yaml
# EU AI Act Article 9 compliance: every agent decision is evidence
# Policy fingerprint in every audit record proves which policy was active
enforcement_mode: block
tools:
  submit_regulatory_filing:
    sequence:
      only_after: [draft_document, medical_review, legal_review, qa_review]
      requires_prior_n:
        tool: qa_review
        count: 2  # two independent QA passes required
```

---

## 18. Strategic Blind Spots & Additions

These are gaps identified through design analysis that are not in the original brief. Each is validated, reasoned, and assigned a priority.

### Addition 1: Shadow Mode (P0 — Critical for Adoption)

Without shadow mode, no design partner will deploy TrueBearing to a production agent. A misconfigured policy that blocks a critical tool call breaks their product. Shadow mode removes this fear entirely. It is the primary onboarding path.

**Implementation:** Global and per-tool `enforcement_mode: shadow`. In shadow mode, the engine runs the full evaluation pipeline, records the decision as `shadow_deny` in the audit log (signed, real), but forwards the call to upstream. The audit log shows exactly what would have been blocked. After a week of observation, the operator runs `truebearing audit query --decision shadow_deny` to review, adjusts the policy, then flips to `block`.

### Addition 2: `truebearing policy explain` (P0 — Sales Tool)

When approaching a design partner, this command is the demo. Run it on their first policy file and they immediately understand what TrueBearing enforces in plain English. It converts skeptics. It is also a debugging tool — if the policy doesn't explain the way you intended, it's wrong.

### Addition 3: Trace Capture + `truebearing simulate` (P0 — Trust Builder)

Design partners will not believe TrueBearing is correct until they can replay their own real traffic against a policy and see the diff. `truebearing serve --capture-trace ./trace.jsonl` captures everything. `truebearing simulate --trace ./trace.jsonl --policy ./policy.yaml` replays it offline. The result is concrete proof.

### Addition 4: Cost Estimation Per Tool (P1)

The budget engine needs per-tool cost estimates to be useful. For MVP: a configurable cost table in the policy or a separate `costs.yaml` file:

```yaml
# costs.yaml (optional, loaded alongside policy)
tool_cost_estimates:
  execute_wire_transfer: 0.002  # USD per call
  read_invoice: 0.001
  default: 0.001
```

Post-MVP: parse upstream MCP server responses for actual token counts and update estimates from real data.

### Addition 5: Rate Limit / Loop Detection (P1)

An agent in an infinite loop calls the same tool repeatedly. TrueBearing's budget engine catches this eventually (max_tool_calls), but a rate limit catches it faster and produces a more descriptive error.

```yaml
tools:
  search_web:
    rate_limit:
      max_calls_per_minute: 5
```

### Addition 6: Environment Isolation (P1 — DevOps / SecOps Audience)

An agent running in a CI/staging environment must not be able to call production tool endpoints. Enforce at the proxy, not by trusting the agent.

```yaml
# production.policy.yaml
tools:
  deploy_service:
    require_env: production  # only callable when proxy is started with --env production
```

The `--env` flag on `truebearing serve` sets the runtime environment. Tool calls to environment-restricted tools from non-matching environments are denied.

### Addition 7: Webhook Escalation Notifications (P1)

Operators won't watch a terminal waiting for escalations. The escalation system needs to push notifications.

```yaml
# In policy or config
escalation:
  webhook_url: https://hooks.slack.com/services/...
  # or: email: ops-team@acme.com
  # or: stdout (default for local dev)
```

When an escalation is created, TrueBearing fires a POST to the webhook with the escalation payload. The operator uses the Slack message's link to run `truebearing escalation approve <id>`.

### Addition 8: The `truebearing init` Command (P1 — Onboarding)

```sh
truebearing init

? Agent name: payments-agent
? List the tools your agent uses (comma-separated): read_invoice,verify_invoice,execute_payment
? Which tools are high-risk (must be sequence-guarded)? execute_payment
? What must happen before execute_payment? verify_invoice
? Set a budget (tool calls per session)? 50
? Set a cost budget (USD per session)? 5.00

✓ Created truebearing.policy.yaml
✓ Run `truebearing policy explain truebearing.policy.yaml` to review
✓ Run `truebearing agent register payments-agent` to issue credentials
✓ Run `truebearing serve --upstream <your_mcp_url>` to start the proxy
```

This interactive scaffolder generates a valid first policy from answers to 5 questions. It is the entry point for engineers who have never written a policy.

---

## 19. Testing & Quality Strategy

### Unit Tests (Per Evaluator)

Every evaluator in the pipeline has a table-driven unit test suite covering:
- Happy path: tool allowed
- Boundary conditions on every predicate
- All deny paths
- Shadow mode behavior
- Error/fault injection (bad arguments, nil session, malformed policy)

**Target:** 90%+ coverage on `internal/engine/`.

### Integration Tests

Full pipeline tests using a test SQLite database:
1. Register an agent
2. Start a session (implicit on first tool call)
3. Issue a sequence of tool calls
4. Assert decisions match expected outcomes
5. Assert audit log contents and signature validity
6. Assert session state (taint, budget counters)

One integration test per design partner use case (End Close payments, LunaBill billing, etc.). These serve as living documentation of what TrueBearing guarantees.

### Policy Linter Tests

Every linter rule has a test case with a YAML file that triggers it. CI fails if any linter rule does not have a test.

### Performance Benchmarks

```sh
go test -bench=. ./internal/engine/...
```

**Target:** `Evaluate()` p99 < 2ms on a sequence of 1000 session events. The SQLite query is the hot path; optimize it first.

### Fuzz Tests

Fuzz the MCP JSON-RPC parser with `go test -fuzz`. The proxy must never panic on malformed input, regardless of what the agent sends.

### Audit Signature Verification

A test that:
1. Produces 1000 audit records
2. Tampers with 5 random records (changes the decision field)
3. Runs `audit verify`
4. Asserts exactly 5 `TAMPERED` results

---

## 20. Moat & IP Architecture

### Why This Is Hard to Replicate

**1. Sequence state management at scale is non-trivial.**  
The naive implementation (ring buffer with eviction) silently corrupts session history. We've already discovered this. The correct implementation (monotonic sequence numbers + hard history cap) requires understanding why the naive approach fails. This is documented in Fix 2.

**2. The escalation async design is subtle.**  
Every obvious implementation of "pause for human approval" breaks LLM framework timeouts. The virtual tool injection pattern is non-obvious and requires understanding both the MCP protocol and LLM orchestration lifecycle.

**3. Policy fingerprint binding is easy to miss.**  
Binding a session to a specific policy version and hard-erroring on policy change seems obvious in hindsight but is absent from every existing tool. The POC didn't do it. We learned.

**4. The DSL is the accumulation moat.**  
Every design partner who writes production behavioral policies is creating organizational knowledge that encodes their business logic, compliance requirements, and security constraints. A policy corpus developed over months of production experience does not get migrated. This mirrors OPA/Rego policies and Terraform state files. The policy language is the lock-in.

**5. Behavioral telemetry at the proxy position.**  
TrueBearing sees every tool call across every agent for every customer. That signal trains behavioral anomaly detection (post-MVP) that no observability-only tool can replicate. It improves with scale.

### What to Open Source vs. Keep Closed

| Component | Open Source | Rationale |
|---|---|---|
| Policy DSL specification | Yes | Community writes the policies. The real value is the library, not the spec. |
| CLI binary | Yes | Adoption. Developer trust. Reviewability. |
| Core proxy & engine | Yes | Infrastructure credibility. OPA and Cerbos won by being open. |
| Policy packs | Yes | Community-driven. Network effects. |
| Hosted control plane | No | Revenue. Policy sync, team management, compliance exports. |
| Behavioral anomaly detection | No | The data asset. Trained on production traces. |
| Compliance evidence export (SOC2/HIPAA bundles) | No | Enterprise revenue. |
| Escalation workflow integrations (Slack, PagerDuty) | Partial | Basic webhook open; deep integrations closed. |

---

## 21. Post-MVP Roadmap Signals

These are not in scope for the closed beta. They are noted here because early design decisions must not block them.

**Near-term (post closed beta):**
- Hosted control plane with policy push (remove the proxy restart requirement on policy change)
- Slack/PagerDuty escalation integrations
- Per-tool cost estimation from upstream token counts
- Rate limiting and loop detection
- Environment isolation

**Medium-term:**
- Behavioral anomaly detection: flag sessions where tool call sequences are statistically unusual compared to that agent's historical baseline
- Policy suggestion engine: "based on your session history, we suggest adding this sequence guard"
- `truebearing policy test` — YAML-based policy unit tests (like OPA's `rego_test`)
- VSCode extension with policy syntax highlighting and inline linting

**Long-term (Series A scope):**
- Multi-tenant hosted proxy (route design partner traffic through Mercator cloud; no local binary required)
- Compliance evidence bundles mapped to EU AI Act Article 9 / HIPAA / SOC2
- Policy marketplace (certified packs, community contributions, revenue share)
- Cross-agent behavioral graph: visualize the delegation chain for every session

---

## Appendix A: Dependency List

| Package | Purpose | Notes |
|---|---|---|
| `github.com/spf13/cobra` | CLI framework | Standard Go CLI |
| `github.com/spf13/viper` | Config management | Works with cobra |
| `github.com/golang-jwt/jwt/v5` | JWT validation | Ed25519 support |
| `modernc.org/sqlite` | Embedded SQLite | Pure Go, no CGo required — critical for single binary |
| `gopkg.in/yaml.v3` | Policy YAML parsing | Standard |
| `github.com/google/uuid` | UUID generation | Audit record IDs |
| `github.com/tidwall/gjson` | JSONPath for escalation arg extraction | Fast, no reflection |

**No other dependencies for the core binary.** The Python and Node SDKs have their own dependency trees (requests/httpx, node-fetch) but are separate packages.

---

## Appendix B: File Naming Conventions

- Policy files: `<agent-name>.policy.yaml` or `truebearing.policy.yaml`
- Trace files: `<session-id>-<date>.trace.jsonl`
- Audit log exports: `audit-<date>-<session-id>.jsonl`
- Agent JWT: `~/.truebearing/keys/<agent-name>.jwt`
- Proxy private key: `~/.truebearing/keys/proxy.pem`

---

*This document is the engineering north star for the TrueBearing MVP. It governs design decisions until closed beta with design partners. Any deviation from the thumb rules documented here requires explicit team discussion and this document updated to reflect the decision and the reasoning.*
