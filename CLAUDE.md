# CLAUDE.md — TrueBearing Engineering Instructions

> This file is the operational contract between you (the coding assistant) and this codebase.
> Read it fully before writing a single line of code. It does not repeat the architecture —
> `mvp-plan.md` owns that. This file owns how you work.

---

## 0. Before You Do Anything

Read these two files in full at the start of every session:

1. **`CLAUDE.md`** — this file. How to work.
2. **`TODO.md`** — what to build next, what has been built, notes from prior sessions.

Then identify the single task you have been asked to work on. Do not begin adjacent tasks. Do not refactor things you were not asked to touch. Do not "clean up while you're in there." Scope discipline is the most important habit in this codebase.

The MVP plan is at `docs/mvp-plan.md`. Read the relevant section for your current task before writing code. If the plan and `TODO.md` conflict, `TODO.md` wins — it contains decisions made after the plan was written.

---

## 1. Project Identity

**TrueBearing** is a transparent MCP proxy with sequence-aware behavioral policy enforcement. It is infrastructure for trust. It sits in the critical path of autonomous systems that execute irreversible real-world actions (money movement, data submission, API calls). Correctness is more important than cleverness. Clarity is more important than brevity.

**Language:** Go, throughout. Standard library first. Every third-party dependency requires justification.

**Approved dependencies (core binary):**
```
github.com/spf13/cobra        CLI framework
github.com/spf13/viper        Config management
github.com/golang-jwt/jwt/v5  JWT with Ed25519 support
modernc.org/sqlite            Pure-Go SQLite (no CGo — required for single static binary)
gopkg.in/yaml.v3              Policy YAML parsing
github.com/google/uuid        UUID generation
github.com/tidwall/gjson      JSONPath for escalation argument extraction
```

Adding a dependency outside this list requires a comment in `go.mod` explaining why the standard library or an approved package was insufficient.

---

## 2. The Build Order (Non-Negotiable)

```
CLI Design → Policy DSL → Wire Protocol → Engine → Tests → SDKs
```

Never implement an engine feature that has no CLI command surfacing it. Never implement a CLI command that has no corresponding type in the policy or session layer. Work from the outside in. If a task asks you to implement something that skips a layer, flag it before proceeding.

---

## 3. Code Style & Formatting

### Formatting
- All code must pass `gofmt`. Run it before considering any file done.
- All code must pass `go vet`. Zero warnings.
- Imports: stdlib first, then external, then internal — separated by blank lines.

### Naming
- Exported types: `PascalCase`. Unexported: `camelCase`. No abbreviations unless they are universal in Go (`ctx`, `err`, `db`, `r`, `w`).
- File names: `snake_case.go`. Test files: `<file>_test.go` in the same package.
- Constants: `PascalCase` for exported, `camelCase` for unexported. Never `ALL_CAPS` unless interoperating with a C library (we are not).
- Error variables: `ErrSomething`. Error types: `SomethingError`.

### Error Handling
- **Never discard errors.** `_ = something()` is only acceptable for `defer f.Close()` where the error is genuinely unactionable.
- Wrap errors with context: `fmt.Errorf("loading policy from %s: %w", path, err)`. The message should read as a sentence fragment that completes "failed while ...".
- At the top of the call stack (CLI handlers, HTTP handlers), log the full error and return a user-facing message. Never expose internal error strings directly to API callers.
- In the evaluation pipeline, errors mean **deny**. An evaluator that returns an error must not allow the tool call. See Rule 1 in `mvp-plan.md`.

### No Panics in Production Code
- `panic` is only acceptable in `init()` for programmer errors that are impossible to recover from (e.g., a regexp that fails to compile from a string literal).
- Never `panic` on user input, malformed files, or network conditions.
- Use `log.Fatal` only in `main()` for startup failures that render the binary inoperable.

---

## 4. Comments — The Why, Not the What

Comments explain intent, not mechanics. If a comment restates what the code obviously does, delete it.

**Bad:**
```go
// increment the counter
session.ToolCallCount++
```

**Good:**
```go
// Increment before the budget check so a call that hits the cap is counted in the
// audit log. This ensures the cap is visible in the session history even for denied calls.
session.ToolCallCount++
```

### Package-level doc comments
Every `internal/` package must have a `doc.go` or a package comment on the main file explaining:
1. What this package owns (one sentence).
2. What it does NOT own (one sentence, if there is a likely confusion).
3. Key invariants the caller must respect.

Example:
```go
// Package engine implements the TrueBearing evaluation pipeline.
//
// It is the only package that makes allow/deny/escalate decisions.
// It does not own session persistence (see package store) or audit signing (see package audit).
//
// Invariant: every call to Pipeline.Evaluate must result in exactly one AuditRecord
// being written, regardless of decision outcome. Callers must not write audit records themselves.
package engine
```

### Decision comments
When you make a non-obvious design choice — a specific algorithm, a data layout, an ordering decision — write a `// Design:` comment explaining the tradeoff and why this option was chosen over the obvious alternative.

Example:
```go
// Design: we collect ALL sequence violations before returning rather than short-circuiting
// on the first failure. Operators debugging a policy need to see every violated predicate
// in a single request, not discover them one at a time over multiple attempts.
```

### TODO comments
`// TODO(task-id):` with a reference to the TODO.md task number. Never leave a bare `// TODO` — it becomes orphaned and untrackable.

---

## 5. Testing — Rules and Requirements

### The Rule
**No function in `internal/engine/` or `internal/policy/` is considered complete without tests.** For all other packages, tests are required for any function with conditional logic.

### Test File Location
Tests live alongside the code they test, in the same package. Use `package foo_test` (black-box) for public API tests and `package foo` (white-box) for internal invariant tests. Use white-box only when you genuinely need to inspect unexported state.

### Table-Driven Tests
All evaluator tests and all linter tests must be table-driven. The pattern:

```go
func TestSequenceEvaluator(t *testing.T) {
    cases := []struct {
        name          string
        sessionEvents []store.SessionEvent  // what happened before this call
        toolName      string                // the tool being called
        policy        *policy.ToolPolicy
        wantAction    engine.Action
        wantRuleID    string
    }{
        {
            name:      "allowed when only_after satisfied",
            // ...
        },
        {
            name:      "denied when only_after not satisfied",
            // ...
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // ...
        })
    }
}
```

### What to Test
For each evaluator, the minimum test matrix is:
- Happy path: call is allowed.
- Every denial path: one test per `only_after`, `never_after`, `requires_prior_n`, taint applied, taint blocks, budget exceeded (calls), budget exceeded (cost), escalation triggered, escalation previously approved (should allow).
- Boundary conditions: exactly at the limit, one under, one over.
- Shadow mode: same inputs as a denial path, verify action is `shadow_deny` not `deny`.
- Error injection: nil session, malformed arguments, evaluator returns error → verify pipeline returns `deny`.

### No Mocks for SQLite
Use a real in-memory SQLite database in tests. `modernc.org/sqlite` supports `file::memory:?cache=shared`. The store package must expose a `NewTestDB(t *testing.T) *sql.DB` helper that initializes the schema and registers cleanup via `t.Cleanup`. Do not mock the database — test against the real implementation.

### Integration Tests
`internal/engine/integration_test.go` contains end-to-end scenario tests. Each design partner use case in `mvp-plan.md §17` must have one integration test that runs the full pipeline and asserts the correct sequence of decisions. These tests are the living specification of what TrueBearing guarantees.

### Benchmark Tests
Each evaluator must have a `BenchmarkEvaluate` function. The benchmark uses a session with 1000 historical events. Run with `go test -bench=. -benchmem ./internal/engine/`. The target is p99 < 2ms. If a benchmark regresses more than 20% versus the previous commit, the task is not done.

---

## 6. The Evaluation Pipeline — Invariants

These invariants must hold at all times. Every change to `internal/engine/` must be reviewed against them.

1. **One audit record per tool call, always.** Whether the decision is allow, deny, shadow_deny, or escalate — exactly one `AuditRecord` is written. Never zero. Never two.
2. **Evaluators are pure.** An evaluator reads from the session and the policy. It does not write to either. State mutations (taint change, counter increments) happen in the pipeline orchestrator after the decision, not inside the evaluator.
3. **First failure terminates the pipeline.** When an evaluator returns a non-allow decision, no subsequent evaluators run.
4. **Error = deny.** An evaluator that returns a non-nil error produces a `Deny` decision. The error is logged. The pipeline does not propagate errors to callers — it converts them to decisions.
5. **Shadow mode is applied at the pipeline level, not in evaluators.** Evaluators always return `Deny` or `Escalate` when a violation is found. The pipeline converts to `ShadowDeny` based on the effective enforcement mode. Evaluators are unaware of shadow mode.

---

## 7. The SQLite Store — Invariants

1. **`audit_log` is append-only.** No UPDATE or DELETE ever touches this table. If you find yourself writing one, stop.
2. **Sequence numbers never reuse.** `session_events.seq` is a monotonically increasing `uint64` scoped to a session. When a session's event count reaches `policy.Session.MaxHistory`, return an error. Do not evict or wrap around.
3. **WAL mode on every open.** The store package's `Open()` function must always set `PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA synchronous=NORMAL`. This is not optional.
4. **No raw SQL in packages other than `store`.** All database access goes through `internal/store`. Other packages call store methods, never `db.Query` directly.

---

## 8. Security Invariants

1. **Fail closed.** If TrueBearing cannot evaluate a request — database error, policy parse error, JWT validation failure — it returns an error response and blocks the tool call. It never defaults to allow under uncertainty.
2. **No JWT = 401, always.** There is no flag, environment variable, or config option that bypasses JWT validation. If you see code that does, it is a bug.
3. **Private keys are 0600.** Any code that writes a key file must set permissions to `0600`. Assert this in tests.
4. **Arguments are never logged in plaintext.** `audit_log.arguments_sha256` stores only the hash. `session_events.arguments_json` stores the raw JSON for the sequence engine — this is acceptable because it is local-only storage, not exported. Be explicit in comments when storing raw arguments.
5. **No secrets in error messages.** Errors returned to callers must not contain key material, JWT contents, or argument values. Log the full detail internally; surface a sanitised message externally.

---

## 9. How to Update TODO.md

At the end of every task:

1. Change the task's status from `[ ]` to `[x]`.
2. Add a `**Notes:**` block under the completed task containing:
   - What files were created or modified (list them).
   - Any design decisions made that deviate from or extend the plan.
   - Any invariants or gotchas that the next task touching this area should know.
   - Any `// TODO(task-id):` comments left in code that belong to a future task.
3. Do not delete completed tasks. The history is the documentation.

**Format for a completed task:**
```markdown
- [x] **1.2** — CLI Framework: cobra + viper skeleton, all commands stubbed
  **Status:** Complete
  **Files:** `cmd/main.go`, `cmd/serve.go`, `cmd/agent/register.go`, ... (all stubs)
  **Notes:** Used cobra's `PersistentPreRunE` on the root command to initialise the
  database and load config before any subcommand runs. Viper is bound to
  `~/.truebearing/config.yaml`. The `--db` and `--policy` flags are registered as
  persistent flags on the root command so all subcommands inherit them automatically.
  All unimplemented commands print: `fmt.Println("[not yet implemented]: <command>")` and
  return nil — not an error — so the shell exit code is 0 during the skeleton phase.
```

---

## 10. What "Done" Means

A task is done when all of the following are true:

- [ ] The code compiles with `go build ./...`
- [ ] `go vet ./...` produces zero output
- [ ] `gofmt -l .` produces zero output (no unformatted files)
- [ ] All tests pass: `go test ./...`
- [ ] New code has tests (see §5 for what requires tests)
- [ ] Benchmarks for evaluators run and are within the 2ms p99 target
- [ ] Every new function in `internal/` has a doc comment
- [ ] Every new package has a package-level doc comment
- [ ] `TODO.md` has been updated with a completion note
- [ ] No `fmt.Println` debug output left in non-CLI code
- [ ] No commented-out code left in the diff

**Do not declare a task done if any item above is not satisfied.** Ask for clarification rather than ship something incomplete.

---

## 11. Customer Agnosticism in Code Artefacts

TrueBearing is a domain-agnostic policy engine. No specific customer, company, or prospect name
may appear in any code file, test file, fixture file, or filename in this repository.

**The rule:** testdata fixtures and policy packs are named after the **pattern** they demonstrate,
not the company that inspired the example. `fintech-payment-sequence.policy.yaml` is correct.
`endclose.policy.yaml` is not — even as a test fixture.

**Why this matters:**
- A design partner shown the codebase during a technical evaluation should not see another
  company's name in the test suite. It signals that TrueBearing was built for someone else.
- Pattern-named fixtures are better documentation. The name states what is being demonstrated.
- Renaming fixtures after a prospect becomes a customer is wasted work. Name them right once.

**The only place company names are acceptable** is in planning documents under `docs/` and in
`README.md` narrative examples that are clearly illustrative. They must never appear in:
- Any filename under `testdata/`, `policy-packs/`, `cmd/`, `internal/`, or `pkg/`
- Any Go test function name or test case name string
- Any policy YAML `agent:` field in testdata (use generic names like `payments-agent`,
  `claims-agent`, `legal-agent`)
- Any comment in production code

---

## 12. Things You Must Not Do

- **Do not implement features not in the current task.** Scope is sacred.
- **Do not refactor code outside the files you were asked to change.** If you notice a problem, add a `// TODO(task-id):` comment and note it in `TODO.md` for a future task.
- **Do not add dependencies without flagging it.** If you believe a dependency is necessary, state it explicitly before adding it and explain why the standard library is insufficient.
- **Do not use `interface{}` or `any` in the evaluation pipeline.** Use concrete types. The pipeline is performance-critical and type-safe by design.
- **Do not write domain-specific logic in Go.** The engine must not contain the words "invoice", "payment", "HIPAA", "PHI", or any business domain term. If you find yourself writing one, you are in the wrong layer.
- **Do not use `log.Println` in library code.** Library code (`internal/`) returns errors. CLI code (`cmd/`) logs them. This is the boundary.
- **Do not silently default to allow.** If in doubt, deny and surface a clear error.

---

## 12. Commit Message Format

```
<phase>/<subunit>: <imperative short description>

<optional body: what changed and why, not what the code does>

Refs: TODO-<task-id>
```

Examples:
```
phase1/1.3: implement Ed25519 keypair generation and storage

Keys are stored in ~/.truebearing/keys/ with 0600 permissions.
Using crypto/ed25519 from stdlib rather than a third-party package.

Refs: TODO-1.3
```

```
phase2/2.1: add YAML policy parser and Policy struct

Fingerprint is computed over canonical JSON of the parsed struct
(sorted keys) rather than raw YAML bytes to be stable across
whitespace changes in the source file.

Refs: TODO-2.1
```

---

## 13. Where Things Live

| Concern | Package |
|---|---|
| CLI commands and user-facing output | `cmd/` |
| JWT generation and validation | `internal/identity/` |
| Ed25519 key I/O | `internal/identity/` |
| Policy YAML parsing, types, linter, fingerprinter | `internal/policy/` |
| SQLite schema, migrations, all DB access | `internal/store/` |
| Session state (load, create, update taint/budget) | `internal/session/` |
| Evaluation pipeline and all evaluators | `internal/engine/` |
| HTTP listener, JWT middleware, request routing | `internal/proxy/` |
| MCP JSON-RPC wire format parsing | `pkg/mcpparse/` |
| Audit record signing and verification | `internal/audit/` |
| Escalation state machine | `internal/escalation/` |
| Trace ID header extraction | `internal/proxy/traceheaders.go` |

If a new file does not clearly belong to one of these, flag it before creating it.