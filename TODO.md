# TODO.md — TrueBearing MVP Build Tracker

> **How to use this file:**
>
> - Read it at the start of every session before writing code.
> - When a task is complete, mark it `[x]`, add a `**Notes:**` block, and list files changed.
> - Do not delete completed tasks. The history is the documentation.
> - One task per session. Do not jump ahead.
>
> **Cross-reference:** `docs/mvp-plan.md` contains the full specification for each phase.
> Section numbers below (e.g., §1.1) refer to that document.

---

## Phase 1 — CLI Skeleton & Cryptographic Identity

> **Goal:** Create the operator-facing shell and the cryptographic trust model.
> No evaluation logic. Every command is a well-structured stub.
> **Delivers:** A compilable binary. A trust model that cannot be weakened later.
> **mvp-plan.md reference:** §5 (Phase 1)

---

- [x] **Task 1.1** — Repository scaffold & go.mod initialisation
      **Scope:**
  - Create the full directory structure from `mvp-plan.md §1.1` verbatim.
  - Initialise `go.mod` with module path `github.com/mercator-hq/truebearing`.
  - Add approved dependencies to `go.mod` (cobra, viper, jwt, sqlite, yaml.v3, uuid, gjson).
  - Create empty placeholder `doc.go` files in every `internal/` and `pkg/` package with a one-line package comment.
  - Create `docs/` directory and copy `mvp-plan.md` into it.
  - Create a minimal `README.md` (project name, one-line description, "under construction").
  - Verify: `go build ./...` succeeds with no errors.

  **Satisfaction check:**
  - `go build ./...` exits 0.
  - All directories from the plan exist.
  - No logic exists yet — placeholder files only.

  **Status:** Complete
  **Files:**
  - `go.mod`, `go.sum` — module initialised with all seven approved dependencies
  - `README.md` — minimal under-construction readme
  - `cmd/main.go` — package main entry point stub with `// TODO(1.2)` marker
  - `internal/identity/doc.go`, `internal/policy/doc.go`, `internal/session/doc.go`,
    `internal/engine/doc.go`, `internal/proxy/doc.go`, `internal/audit/doc.go`,
    `internal/escalation/doc.go`, `internal/budget/doc.go`, `internal/store/doc.go`
  - `pkg/mcpparse/doc.go`
  - All directories: `cmd/policy/`, `cmd/audit/`, `cmd/session/`, `cmd/escalation/`,
    `cmd/agent/`, `policy-packs/fintech/`, `policy-packs/healthcare/`,
    `policy-packs/devops/`, `testdata/traces/`
  **Notes:**
  - All seven approved dependencies are marked `// indirect` because no source file imports
    them yet; they will become direct once Task 1.2 wires up cobra and viper.
  - `docs/mvp-plan.md` was already present; no copy needed.
  - `go build ./...`, `go vet ./...`, and `gofmt -l .` all exit clean.

---

- [x] **Task 1.2** — CLI framework: cobra root + all command stubs
      **Scope:**
  - Implement `cmd/main.go` with cobra root command.
  - Implement stub command files for every command in `mvp-plan.md §13`:
    `serve`, `simulate`, `policy/validate`, `policy/diff`, `policy/explain`, `policy/lint`,
    `audit/verify`, `audit/replay`, `audit/query`, `session/list`, `session/inspect`,
    `session/terminate`, `escalation/list`, `escalation/approve`, `escalation/reject`,
    `agent/register`, `agent/list`.
  - Every stub must: print `"[not yet implemented]: <command name>"` and return `nil`.
  - Register `--policy`, `--db`, `--config` as persistent flags on the root command.
  - Wire viper to `~/.truebearing/config.yaml` and per-project `.truebearing.yaml`.
  - Add a `PersistentPreRunE` on root that will (for now) just verify flags are readable.

  **Satisfaction check:**
  - `truebearing --help` lists all top-level commands.
  - `truebearing policy --help` lists all policy subcommands.
  - Every command runs and prints `[not yet implemented]` without panicking.
  - `go vet ./...` clean.

  **Status:** Complete
  **Files:**
  - `cmd/main.go` — cobra root command, viper config wiring, `PersistentPreRunE`
  - `cmd/serve.go` — serve stub (flags: --upstream, --port, --capture-trace, --stdio)
  - `cmd/simulate.go` — simulate stub (flags: --trace, --old-policy)
  - `cmd/policy/policy.go`, `validate.go`, `lint.go`, `explain.go`, `diff.go`
  - `cmd/audit/audit.go`, `verify.go`, `query.go`, `replay.go`
  - `cmd/session/session.go`, `list.go`, `inspect.go`, `terminate.go`
  - `cmd/escalation/escalation.go`, `list.go`, `approve.go`, `reject.go`
  - `cmd/agent/agent.go`, `register.go`, `list.go`
  **Notes:**
  - Subcommand groups (`cmd/policy/`, `cmd/audit/`, etc.) are separate Go packages imported
    by `package main` in `cmd/`. Package names match the directory name (e.g., `package policy`,
    `package audit`) for clean import-site readability.
  - `PersistentPreRunE` on root calls `initConfig`, which loads `~/.truebearing/config.yaml`
    and merges a per-project `.truebearing.yaml` from the working directory using a separate
    viper instance so global config search paths are not modified.
  - `--policy` and `--db` are persistent flags on root, inherited by all subcommands.
    `viper.BindPFlag` links them so config file values serve as fallbacks to flag defaults.
  - `SilenceErrors: true` and `SilenceUsage: true` are set on root; errors are printed
    explicitly in `main()` to avoid cobra's default double-printing.
  - The cobra `completion` command appears in `--help` automatically; this is expected
    cobra behavior and is not removed.
  - `// TODO(task-id):` markers in stubs reference the implementing task: serve/simulate →
    Phase 3/5; policy → 2.3; audit → 5.3; session → 5.6; escalation → 5.5; agent → 1.6.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.

---

- [x] **Task 1.3** — `internal/store`: SQLite DAL, schema, open/migrate
      **Scope:**
  - Implement `internal/store/store.go`: `Open(path string) (*Store, error)` that opens SQLite,
    sets `PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA synchronous=NORMAL`.
  - Implement schema migration in `internal/store/schema.go`. Apply the full schema from
    `mvp-plan.md §1.4` using `CREATE TABLE IF NOT EXISTS`. All five tables.
  - Expose `NewTestDB(t *testing.T) *Store` in `internal/store/testing.go` using
    `file::memory:?cache=shared` with `t.Cleanup` for teardown.
  - Write tests: `Open` creates all tables; `NewTestDB` is clean per test; WAL mode is set.
  - Do not implement any query methods yet — schema and open only.

  **Satisfaction check:**
  - `go test ./internal/store/...` passes.
  - Schema matches the plan exactly (column names, types, constraints, foreign keys).
  - No query methods exist yet — only `Open`, `Close`, `NewTestDB`.

  **Status:** Complete
  **Files:**
  - `internal/store/store.go` — `Store` struct, `Open` (with PRAGMAs + migrate), `Close`
  - `internal/store/schema.go` — `migrate()` + five `CREATE TABLE IF NOT EXISTS` DDL constants
  - `internal/store/testing.go` — `NewTestDB(t *testing.T) *Store` using unique named in-memory DSNs
  - `internal/store/store_test.go` — 7 tests (50 subtests): all tables exist, all columns present,
    WAL/foreign-key PRAGMAs set, migrate is idempotent, NewTestDB isolation, FK constraint enforced
  **Notes:**
  - `modernc.org/sqlite` and `github.com/google/uuid` were added to `go.mod`/`go.sum` (both approved
    dependencies from CLAUDE.md). uuid was pulled in transitively by modernc.org/sqlite.
  - `Store.db` has `SetMaxOpenConns(1)` to prevent "database is locked" errors from concurrent
    writes in the connection pool. This is standard practice for SQLite + database/sql.
  - In-memory databases return `"memory"` from `PRAGMA journal_mode` (not `"wal"`), even when WAL
    was set; `TestOpen_WALMode` accepts both values. File-based databases in production return `"wal"`.
  - `NewTestDB` uses an atomic counter suffix (`file:testdbN?mode=memory&cache=shared`) to give
    each call a distinct named in-memory database, preventing state leakage between parallel tests.
  - `testing.go` is a non-test file importing `testing` — this is intentional so `NewTestDB` is
    accessible from tests in other packages (e.g. `internal/engine` integration tests in Task 4.8).
  - `TestSessionEventsFK` confirms that `foreign_keys=ON` is enforced at the DB level: inserting a
    `session_events` row referencing a non-existent session returns an error.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.

---

- [x] **Task 1.4** — `internal/identity`: Ed25519 keypair generation and storage
      **Scope:**
  - Implement `internal/identity/keypair.go`:
    - `GenerateKeypair(name string, dir string) (PublicKey, PrivateKey, error)`.
    - Writes private key to `<dir>/keys/<name>.pem` (PEM-encoded PKCS8, permissions `0600`).
    - Writes public key to `<dir>/keys/<name>.pub.pem` (PEM-encoded PKIX, permissions `0600`).
  - Implement `LoadPrivateKey(path string)` and `LoadPublicKey(path string)`.
  - Write tests: key round-trip (generate → load → verify they match); file permissions are `0600`
    (use `os.Stat`); loading a non-existent file returns a descriptive error.

  **Satisfaction check:**
  - `go test ./internal/identity/...` passes.
  - Key files written at correct paths with correct permissions.
  - No JWT code yet — keys only.

  **Status:** Complete
  **Files:**
  - `internal/identity/keypair.go` — `GenerateKeypair`, `LoadPrivateKey`, `LoadPublicKey`,
    plus unexported `writePrivateKey` and `writePublicKey` helpers
  - `internal/identity/keypair_test.go` — 6 tests: round-trip, sign/verify, file permissions
    (both .pem and .pub.pem), not-found errors for both loaders, keys directory creation
  **Notes:**
  - Private key is encoded as PKCS8 PEM (type `"PRIVATE KEY"`); public key as PKIX PEM
    (type `"PUBLIC KEY"`). These match the formats used by `crypto/x509` stdlib functions
    `MarshalPKCS8PrivateKey` / `ParsePKCS8PrivateKey` and `MarshalPKIXPublicKey` /
    `ParsePKIXPublicKey`.
  - Both key files are written with `0600` permissions per the security invariant in CLAUDE.md §8.
    The `keys/` directory itself is created with `0700`.
  - Public key files use `0600` (not `0644`) by design: agent names in filenames are operational
    information that should not be readable by other local users.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.
  - No JWT code in this task — keys only. JWT minting/validation is Task 1.5.

---

- [x] **Task 1.5** — `internal/identity`: JWT minting and validation
      **Scope:**
  - Implement `internal/identity/jwt.go`:
    - `AgentClaims` struct from `mvp-plan.md §1.3`.
    - `MintAgentJWT(claims AgentClaims, privateKey ed25519.PrivateKey, expiry time.Duration) (string, error)`.
    - `ValidateAgentJWT(tokenString string, publicKey ed25519.PublicKey) (*AgentClaims, error)`.
  - Validation must: verify signature, reject expired tokens, reject tokens with no `agent` claim.
  - Write tests: valid token round-trips; expired token rejected; tampered token rejected (flip one byte
    in the signature); missing claim rejected.

  **Design note:** `ValidateAgentJWT` takes a `publicKey` argument, not a keystore. The proxy
  will look up the public key from the `agents` table before calling this. Keeping key lookup
  separate from validation makes this function purely testable.

  **Satisfaction check:**
  - `go test ./internal/identity/...` passes with all cases above.
  - No code in `internal/identity` touches the database.

  **Status:** Complete
  **Files:**
  - `internal/identity/jwt.go` — `AgentClaims`, `MintAgentJWT`, `ValidateAgentJWT`, `ErrMissingAgentClaim`
  - `internal/identity/jwt_test.go` — 6 tests: round-trip (all fields), expired rejection, tampered
    signature rejection, wrong-key rejection, missing-agent-claim rejection, expiry-duration correctness
  - `go.mod` / `go.sum` — `github.com/golang-jwt/jwt/v5 v5.3.1` added (approved dependency from CLAUDE.md)
  **Notes:**
  - Signing method is locked to `jwt.SigningMethodEdDSA` inside the key function passed to
    `jwt.ParseWithClaims`. Any token presenting a different `alg` header is rejected before the
    key function returns, preventing algorithm-confusion attacks (e.g., `alg: none`, HMAC variants).
  - `jwt.WithExpirationRequired()` is passed to the parser so tokens that omit `exp` are rejected
    rather than treated as non-expiring — fail-closed per CLAUDE.md §8.
  - JWT `NumericDate` is second-precision (RFC 7519 §2). The expiry test truncates comparison bounds
    to the second to avoid sub-millisecond timing races.
  - `ErrMissingAgentClaim` is a package-level sentinel error so callers (the proxy auth middleware
    in Task 3.3) can type-check the denial reason without string matching.
  - No database access anywhere in `internal/identity` — key lookup stays in the caller (proxy).
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.

---

- [x] **Task 1.6** — `cmd/agent/register`: wire up `truebearing agent register`
      **Scope:**
  - Implement `cmd/agent/register.go` to actually work (first real CLI command).
  - It must: validate `--policy` file exists; parse the policy YAML just enough to extract `may_use`
    (a minimal parse — full parser is Phase 2; for now just unmarshal `may_use: []string`);
    call `identity.GenerateKeypair`; mint a JWT with `AllowedTools` set from `may_use`;
    write the JWT to `~/.truebearing/keys/<name>.jwt` (permissions `0600`);
    insert the agent into the `agents` table via the store.
  - Print a structured success summary (see `mvp-plan.md §13: truebearing agent register`).
  - Implement `cmd/agent/list.go` to read from the `agents` table and print a table.

  **Satisfaction check:**
  - `truebearing agent register my-agent --policy ./testdata/minimal.policy.yaml` creates key files,
    a JWT file, and a row in the database.
  - `truebearing agent list` shows the registered agent.
  - Re-registering the same name overwrites cleanly (no duplicate key error).

  **Status:** Complete
  **Files:**
  - `internal/store/agents.go` — NEW: `Agent` struct, `UpsertAgent`, `ListAgents`, `AllowedTools`
  - `internal/store/agents_test.go` — NEW: 5 tests covering insert, overwrite, empty list, ordering,
    and AllowedTools decoding
  - `cmd/agent/register.go` — replaced stub; implements full registration flow with `--expiry-days` flag
  - `cmd/agent/list.go` — replaced stub; tabwriter table with name, policy, tool count, registered, expires
  - `cmd/agent/list_test.go` — NEW: white-box tests for unexported `jwtExpiry` helper (valid + 6 invalid cases)
  - `testdata/minimal.policy.yaml` — NEW: minimal 3-tool policy fixture for manual and future integration tests
  **Notes:**
  - `minimalPolicy` struct in `register.go` parses only `may_use: []string`. The full parser is Phase 2
    (Task 2.1). A `// Design:` comment explains the intentional scope limit.
  - `resolveDBPath(tbHome string)` is defined in `register.go` and shared with `list.go` within the
    same `cmd/agent` package. It reads `viper.GetString("db")` then falls back to
    `~/.truebearing/truebearing.db`.
  - `Agent.JWTPreview` stores the full JWT text (not just 32 chars as the schema comment suggests).
    A `// Design:` comment in `agents.go` explains the tradeoff: storing the full JWT lets `agent list`
    decode and display the expiry via `jwtExpiry()` without adding a separate schema column. JWTs are
    not secrets — they are intended to be shared as Bearer tokens.
  - `jwtExpiry` in `list.go` decodes the JWT payload segment with `base64.RawURLEncoding` (standard
    for JWTs per RFC 7519) and extracts the `exp` field without signature verification. This is safe
    for a local admin display command — we issued these tokens ourselves.
  - nil `may_use` in policy YAML is normalised to `[]string{}` before JSON marshalling, so
    `allowed_tools_json` is always `"[]"` not `"null"`.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.
  - Smoke-tested manually: register creates key files + JWT + DB row; list shows tabular output with
    expiry; re-register overwrites to a single row with updated timestamp.

---

## Phase 2 — Policy DSL & Parser

> **Goal:** Define the language operators write. Build a parser that produces typed Go structs.
> Build the linter, fingerprinter, and the four `policy` CLI commands.
> **Delivers:** `truebearing policy validate`, `lint`, `explain`, `diff` all work.
> **mvp-plan.md reference:** §6 (Phase 2)

---

- [x] **Task 2.1** — `internal/policy`: types, YAML parser, fingerprinter
      **Scope:**
  - Implement `internal/policy/types.go` with all structs from `mvp-plan.md §6.2`.
  - Implement `internal/policy/parser.go`:
    - `ParseFile(path string) (*Policy, error)` — reads YAML, unmarshals to struct, calls fingerprinter.
    - `ParseBytes(data []byte, sourcePath string) (*Policy, error)` — same but from bytes (for tests).
  - Implement `internal/policy/fingerprint.go`:
    - Fingerprint is `sha256` of the **canonical JSON** of the parsed struct (marshalled with sorted keys),
      not raw YAML bytes. This makes the fingerprint stable across YAML whitespace changes.
    - Return as a 16-char hex prefix for display (e.g., `"a8f9c244"`) and the full 64-char hex for storage.
  - Write tests: parse the example policy from `mvp-plan.md §6.1`; verify all fields; fingerprint is stable
    across whitespace changes; missing required fields produce descriptive errors.

  **Satisfaction check:**
  - `go test ./internal/policy/...` passes.
  - `ParseFile` of a malformed YAML returns an error, never panics.
  - Fingerprint of identical content (different whitespace) is identical.

  **Status:** Complete
  **Files:**
  - `internal/policy/types.go` — NEW: `Policy`, `EnforcementMode`, `SessionPolicy`, `BudgetPolicy`,
    `ToolPolicy`, `SequencePolicy`, `PriorNRule`, `TaintPolicy`, `EscalateRule` with both yaml and
    json struct tags. `Fingerprint` and `SourcePath` carry `json:"-"` to exclude them from hashing.
    `ShortFingerprint()` method returns the first 8 hex chars for display.
  - `internal/policy/fingerprint.go` — NEW: `Fingerprint(p *Policy) (string, error)` — computes
    SHA-256 over canonical JSON (`encoding/json.Marshal` sorts map keys alphabetically, encodes
    struct fields in definition order), stores full 64-char hex in `p.Fingerprint`.
  - `internal/policy/parser.go` — NEW: `ParseFile`, `ParseBytes`, unexported `validate` (checks
    version and agent are non-empty), unexported `normalize` (nil slices/maps → empty equivalents
    for fingerprint stability).
  - `internal/policy/parser_test.go` — NEW: 11 tests covering full DSL example field verification,
    minimal policy, malformed YAML, missing version, missing agent, file-not-found, disk reads,
    fingerprint whitespace stability, fingerprint content sensitivity, source-path exclusion from
    fingerprint, and nil-slice normalization.
  **Notes:**
  - `EscalateRule.Value` is `interface{}` per the plan's §6.2 type definition. This is the only
    use of `interface{}` in the policy layer; CLAUDE.md §12 prohibits it in `internal/engine/` (the
    evaluation pipeline), not in the policy parsing layer.
  - The `normalize` function ensures that `may_use: []` and an omitted `may_use` field produce
    identical fingerprints (both normalize to `[]string{}`). Same for `tools: {}` vs omitted.
    This is tested in `TestNormalize_NilSlicesBeforeFingerprint`.
  - Fingerprint uses `encoding/json.Marshal` directly on `*Policy`. The `json:"-"` tags on
    `Fingerprint` and `SourcePath` exclude them. No separate fingerprintable struct is needed.
  - Short fingerprint is 8 hex chars (matching plan examples like `a8f9c2` + 2 more chars). The
    plan body says "16-char prefix" but all examples show 6–8 chars; 8 was chosen to match the
    TODO example `"a8f9c244"`.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.
    11/11 policy tests pass.

---

- [x] **Task 2.2** — `internal/policy`: linter (L001–L013)
      **Scope:**
  - Implement `internal/policy/lint.go`:
    - `LintResult` struct: `Code string`, `Severity Severity` (Error/Warning/Info), `Message string`.
    - `Lint(p *Policy) []LintResult`.
  - Implement all 13 rules from `mvp-plan.md §6.4`. Each rule is a separate private function
    called from `Lint`. Never inline rule logic directly.
  - **L013 (cycle detection):** build a directed graph of `only_after` and `never_after` relationships.
    Run DFS cycle detection (Kahn's algorithm). If a cycle exists, report the full cycle path in the message.
  - Write tests: one test per lint rule — a YAML that triggers it, assert the correct code appears in output;
    one test with a valid policy that produces zero errors.

  **Satisfaction check:**
  - All 13 rules have tests. `go test ./internal/policy/...` passes.
  - A policy with a cycle returns exactly one `L013 ERROR`.
  - A clean policy with no issues returns an empty `[]LintResult`.

  **Status:** Complete
  **Files:**
  - `internal/policy/lint.go` — NEW: `Severity` type and constants (`SeverityError`, `SeverityWarning`,
    `SeverityInfo`), `LintResult` struct, `Lint(p *Policy) []LintResult`, and 13 private rule functions
    (`lintL001` through `lintL013`). L013 uses three-colour DFS cycle detection and reconstructs the
    full cycle path in the error message.
  - `internal/policy/lint_test.go` — NEW: 16 test functions, 35 total subtests. Table-driven tests
    for every lint rule including both triggering and non-triggering cases. Extra tests:
    `TestLint_L013_MessageFormat` (exact message structure), `TestLint_CleanPolicy` (zero results),
    `TestLint_SeverityValues` (string constants), `TestLint_AllValidOperatorsPassL012` (8 operators).
  - `internal/policy/types.go` — MODIFIED: added `EscalationConfig` struct and `Escalation
    *EscalationConfig` field to `Policy`. Uses pointer + `json:",omitempty"` so policies that omit
    the `escalation:` block produce identical fingerprints to pre-2.2 policies.
  **Notes:**
  - L013 uses three-colour DFS (white/gray/black), not Kahn's algorithm. DFS was chosen because it
    naturally reconstructs the full cycle path without a separate pass — when a back edge is detected,
    the current DFS stack IS the cycle. Kahn's algorithm detects cycles but does not reconstruct paths.
  - L013 only graphs `only_after` relationships. `never_after` relationships are not dependency edges —
    they represent mutual exclusion, not ordering, and cannot create deadlock cycles on their own.
  - `EscalationConfig` added to `types.go` now rather than waiting for Task 5.5a because L008 requires
    it to distinguish "no channel configured" from "webhook configured". The operational webhook-sending
    logic stays in Task 5.5a. Using a pointer with `omitempty` preserves fingerprint stability.
  - `buildMayUseSet` is unexported (package-level helper). L002, L003, L004 each call it independently
    to keep each rule function self-contained and independently testable.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.
    35/35 lint tests pass; 11/11 pre-existing parser tests still pass.

---

- [x] **Task 2.3** — `cmd/policy`: wire up all four policy commands
      **Scope:**
  - `policy validate <file>`: calls `ParseFile`, prints "OK" or errors, exits non-zero on error.
  - `policy lint <file>`: calls `ParseFile` then `Lint`, prints results with severity colours
    (red for ERROR, yellow for WARNING, cyan for INFO). Exit code 1 if any ERROR.
  - `policy explain <file>`: calls `ParseFile`, prints the plain-English summary format from
    `mvp-plan.md §13`. No free-form generation — this is a structured template renderer.
  - `policy diff <old> <new>`: calls `ParseFile` on both, compares field by field, prints a
    structured diff (added/removed tools, changed sequence predicates, changed budget, mode changes).

  **Satisfaction check:**
  - All four commands work against the example policy in `testdata/`.
  - `policy validate` exits non-zero on a broken YAML.
  - `policy lint` exits non-zero when any ERROR rule fires.
  - `policy explain` output matches the format in the plan exactly.

  **Status:** Complete
  **Files:**
  - `cmd/policy/validate.go` — replaced stub; calls `policy.ParseFile`, prints "OK" or returns error
  - `cmd/policy/lint.go` — replaced stub; ANSI-coloured output (red/yellow/cyan), returns error with
    count summary on any ERROR, uses `cmd.OutOrStdout()` for testability
  - `cmd/policy/explain.go` — replaced stub; structured template renderer matching mvp-plan §13
    format exactly; sections (Sequence guards, Taint rules, Escalation rules) are omitted when empty;
    `sortedKeys` ensures stable alphabetical tool ordering
  - `cmd/policy/diff.go` — replaced stub; compares enforcement mode, may_use (added/removed),
    budget, session limits, and per-tool predicates; prints "(no changes detected)" when identical
  - `cmd/policy/policy_test.go` — NEW: 13 test functions covering `describeMode` (3 cases),
    `describeBudget` (4 cases), `sameStringSet` (5 cases), `samePriorN` (6 cases),
    `sameEscalateRule` (7 cases), `printLintResults` (colour + empty), `printExplain`
    (minimal policy + all-sections policy), `printDiff` (no-change, mode change, added/removed
    tools, budget change, predicate change)
  **Notes:**
  - All output functions take an `io.Writer` parameter (populated by `cmd.OutOrStdout()` in cobra
    RunE) so they can be tested via `bytes.Buffer` without capturing os.Stdout.
  - ANSI colour codes are raw escape sequences (`\033[31m` etc.) defined as package-level constants
    — no external terminal library needed. Standard across POSIX terminals and modern Windows.
  - `policy lint` returns `fmt.Errorf("%d error(s) found", errCount)` when errors exist. Because
    the root command has `SilenceErrors: true`, cobra does not print this; `main()` prints it to
    stderr, producing a clean summary line after the coloured diagnostics.
  - `policy diff` uses `sameStringSet` for `only_after`/`never_after` because their evaluation
    semantics are order-independent. A change in list order with identical elements is not a
    policy change.
  - `EscalateRule.Value` equality in `sameEscalateRule` uses `fmt.Sprintf("%v", ...)` — this is
    display-only comparison in a CLI diff command, not enforcement logic.
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.
    Tests verified smoke-tested: validate OK/error, lint error exit code, explain output,
    diff no-change.

---

- [x] **Task 2.4** — `testdata/`: policy fixtures covering the full DSL feature surface
      **Scope:**
  - Create `testdata/policies/` with one `.policy.yaml` per domain pattern. Files are named
    after the enforcement pattern they demonstrate, not after any specific company or customer:
    - `fintech-payment-sequence.policy.yaml` — sequential approval guards (`only_after`), taint
      from external data ingestion, escalation on high-value threshold. Pattern: verify → approve
      → execute, where executing out of order is blocked.
    - `healthcare-phi-taint.policy.yaml` — taint on sensitive data access, block submission tools
      until a compliance scan clears the taint. Pattern: read sensitive → taint → clearance required
      before any outbound action.
    - `insurance-claims-sequence.policy.yaml` — multi-step sequential guard with `requires_prior_n`,
      escalation on payout threshold. Pattern: ingest → check → adjudicate → approve in order.
    - `legal-exfiltration-guard.policy.yaml` — taint on privileged document access blocks all
      outbound tools until explicit privilege review. Pattern: read sensitive → block transmission
      → clearance required.
    - `regulatory-multi-approval.policy.yaml` — `requires_prior_n` requiring multiple independent
      review passes before a filing tool can be called. Pattern: N approvals before irreversible action.
  - Each policy must pass `policy validate` and `policy lint` with zero ERRORs.
  - Each file's header comment explains the pattern and the risk it mitigates. No company names.
  - These files are the canonical examples used by integration tests, simulate demo, policy-packs.

  **Naming rule:** No company names appear in any filename or file content under `testdata/`.
  These fixtures demonstrate patterns. Patterns are customer-agnostic.

  **Satisfaction check:**
  - `truebearing policy lint testdata/policies/*.policy.yaml` — zero ERRORs across all files.
  - No company name appears in any filename or file content.

  **Status:** Complete
  **Files:**
  - `testdata/policies/fintech-payment-sequence.policy.yaml` — NEW: payments-agent; shadow+block
    tool-level override; only_after [verify_invoice, manager_approval]; never_after taint guard;
    requires_prior_n count:1; escalate_when amount_usd > 10000; taint apply/clear lifecycle.
  - `testdata/policies/healthcare-phi-taint.policy.yaml` — NEW: billing-agent; block mode;
    phi_accessed taint from read_phi; taint cleared by run_compliance_scan; submit_claim guarded
    by only_after [verify_eligibility, read_patient_record] + never_after [read_phi];
    escalate_when claim_amount_usd > 5000.
  - `testdata/policies/insurance-claims-sequence.policy.yaml` — NEW: claims-agent; block mode;
    only_after 3-step chain [ingest_claim, fraud_check, adjudicate_claim];
    requires_prior_n {tool: run_quality_check, count: 2}; escalate_when payout_usd > 25000.
  - `testdata/policies/legal-exfiltration-guard.policy.yaml` — NEW: legal-agent; block mode;
    privileged_document_accessed taint from read_privileged_document; taint cleared by
    run_privilege_review; never_after [read_privileged_document] on both send_document_external
    and send_email. Zero WARNINGs — no escalation, no shadow mode.
  - `testdata/policies/regulatory-multi-approval.policy.yaml` — NEW: regulatory-agent; block mode;
    only_after 4-step chain [draft_document, medical_review, legal_review, qa_review];
    requires_prior_n {tool: qa_review, count: 2}. EU AI Act Article 9 pattern. Zero WARNINGs.
  **Notes:**
  - All five files exit 0 from `policy validate` and `policy lint`.
  - Expected WARNINGs (not ERRORs): L008 on three files (escalation webhook not configured —
    test fixtures do not include production webhook URLs) and L009 on fintech (shadow mode).
  - Agent names use CLAUDE.md §11 approved pattern (payments-agent, billing-agent, etc.).
  - These fixtures are the canonical examples referenced by Task 4.8 integration tests:
    TestPaymentSequenceGuard → fintech-payment-sequence.policy.yaml
    TestPHITaintPropagation → healthcare-phi-taint.policy.yaml
    TestClaimsSequentialGuard → insurance-claims-sequence.policy.yaml
    TestPrivilegedDocumentExfiltrationGuard → legal-exfiltration-guard.policy.yaml
    TestMultiApprovalRegulatory → regulatory-multi-approval.policy.yaml
  - `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all exit clean.

---

## Phase 3 — Wire Protocol & MCP Proxy Shell

> **Goal:** Catch the traffic. Parse it correctly. Forward it untouched. No evaluation yet.
> **Delivers:** `truebearing serve` starts, accepts MCP traffic, forwards it upstream.
> Auth middleware rejects missing/invalid JWTs. Session ID header is enforced.
> **mvp-plan.md reference:** §7 (Phase 3)

---

- [ ] **Task 3.1** — `pkg/mcpparse`: MCP JSON-RPC wire format parser
      **Scope:**
  - Implement `pkg/mcpparse/types.go` and `pkg/mcpparse/parse.go` with the types from
    `mvp-plan.md §7.1`.
  - `ParseRequest(body []byte) (*MCPRequest, error)` — unmarshal, validate `jsonrpc: "2.0"` field.
  - `ParseToolCallParams(raw json.RawMessage) (*ToolCallParams, error)`.
  - `IsTool(r *MCPRequest) bool` — returns true iff `method == "tools/call"`.
  - Write fuzz test: `FuzzParseRequest` — must never panic on arbitrary input.
  - Write unit tests: valid tool call; valid non-tool call; malformed JSON; missing fields.

  **Satisfaction check:**
  - `go test ./pkg/mcpparse/...` passes.
  - `go test -fuzz=FuzzParseRequest ./pkg/mcpparse/...` runs for 30 seconds without panicking.
  - `pkg/mcpparse` has zero imports from `internal/` — it is a pure protocol parser.

---

- [ ] **Task 3.2** — `internal/proxy/traceheaders.go`: trace ID extraction
      **Scope:**
  - Implement `ExtractClientTraceID(headers http.Header) string` from `mvp-plan.md §9.1a`.
  - Priority order: `traceparent`, `x-datadog-trace-id`, `x-cloud-trace-context`,
    `x-amzn-trace-id`, `x-b3-traceid`.
  - Return format: `"<header-name>=<value>"`. Return `""` if none present.
  - Write table-driven tests: each header format; multiple headers present (first wins); no headers.

  **Satisfaction check:**
  - `go test ./internal/proxy/...` passes.
  - Function is pure (no side effects, no logging).

---

- [ ] **Task 3.3** — `internal/proxy`: JWT auth middleware
      **Scope:**
  - Implement `internal/proxy/auth.go`:
    - `AuthMiddleware(store *store.Store) func(http.Handler) http.Handler`.
    - Reads `Authorization: Bearer <jwt>` from the request.
    - Looks up the agent's public key from the `agents` table by the `agent` claim in the (unverified) JWT.
    - Calls `identity.ValidateAgentJWT`.
    - On failure: writes `{"error": "unauthorized", "message": "..."}` with status 401 and **stops**.
    - On success: stores `*AgentClaims` in the request context.
  - Implement context key type and helper `AgentClaimsFromContext(ctx context.Context) (*AgentClaims, bool)`.
  - Write tests: missing header → 401; invalid signature → 401; expired token → 401;
    valid token → claims in context; agent not in DB → 401.

  **Satisfaction check:**
  - `go test ./internal/proxy/...` passes.
  - No path through the middleware allows a request with a bad JWT to proceed.

---

- [ ] **Task 3.4** — `internal/proxy`: session ID middleware
      **Scope:**
  - Implement `internal/proxy/session.go`:
    - Middleware that reads `X-TrueBearing-Session-ID` from the request headers.
    - Only enforces on `tools/call` requests (check method after parsing MCP body, or check header universally — see note below).
    - If header is missing on a `tools/call`: return `{"error": "missing_session_id", ...}` with status 400.
    - If present: store the session ID string in the request context.
  - **Design decision to make and document:** Should the header be required on ALL requests or only
    `tools/call`? Decision: enforce only on `tools/call` (non-tool MCP methods like `initialize` and
    `tools/list` are forwarded without a session). Write a `// Design:` comment explaining this.
  - Write tests: `tools/call` without header → 400; `tools/list` without header → forwarded (no 400);
    `tools/call` with header → session ID in context.

  **Satisfaction check:**
  - `go test ./internal/proxy/...` passes.
  - Non-tool methods are unaffected by this middleware.

---

- [ ] **Task 3.5** — `internal/proxy`: reverse proxy and `truebearing serve`
      **Scope:**
  - Implement `internal/proxy/proxy.go`:
    - `New(upstream *url.URL, store *store.Store) *Proxy`.
    - HTTP handler that: runs auth middleware → session middleware → for non-tool calls, forwards
      directly via `httputil.ReverseProxy`; for tool calls, calls `engine.Pipeline.Evaluate`
      (stub: always return `Allow` for now) → forwards upstream on allow → returns synthetic error on deny.
  - Wire `cmd/serve.go` to start this proxy on the configured port.
  - Load the policy file and agent keys on startup. Fail with a clear error if either is missing.
  - The stub `engine.Pipeline.Evaluate` (always allow) is a temporary shim — note it with
    `// TODO(task-4.1): replace with real pipeline` comment.
  - Write tests: proxy starts; non-tool request forwarded; tool call without JWT → 401;
    tool call without session ID → 400; tool call with valid auth → forwarded (stub allows).
  - **No real evaluation yet.** The proxy shell passes all valid-auth tool calls through.

  **Satisfaction check:**
  - `truebearing serve --upstream http://localhost:9999 --policy ./testdata/policies/fintech-payment-sequence.policy.yaml`
    starts without error and listens on the configured port.
  - A `tools/call` with a valid JWT and session ID is forwarded upstream.
  - A `tools/call` with no JWT returns 401.

---

- [ ] **Task 3.5a** — `GET /health` endpoint
      **Scope:**
  - Add a `GET /health` route to the proxy HTTP server (registered before auth middleware —
    health checks must not require a JWT).
  - Response body:
    ```json
    {
      "status": "ok",
      "policy_fingerprint": "a8f9c2",
      "policy_file": "./fintech-payment-sequence.policy.yaml",
      "proxy_version": "0.1.0",
      "db_path": "~/.truebearing/truebearing.db"
    }
    ```
  - If the proxy is running but the policy file has become unreadable or the DB is unreachable,
    return `{"status": "degraded", "reason": "..."}` with HTTP 503.
  - This endpoint is used by the Python and Node SDKs to determine when the subprocess proxy
    is ready to accept traffic. Without it, the SDK has no reliable readiness signal.
  - Write tests: healthy state returns 200 + correct body; degraded state returns 503.

  **Why this is between proxy shell and engine:** the SDK (Task 6.1) polls `/health` before
  forwarding any requests. If this endpoint doesn't exist until Phase 6, the SDK cannot be
  implemented correctly. Building it here keeps the dependency chain clean.

  **Satisfaction check:**
  - `curl http://localhost:7773/health` returns 200 with the correct JSON body.
  - No JWT required to call it.
  - SDK subprocess management can use it as a readiness probe.

---

## Phase 4 — The Evaluation Engine

> **Goal:** The core business logic. Each evaluator is a pure function, independently testable.
> **Delivers:** `truebearing serve` now actually enforces policy. Each evaluator has full test coverage.
> **mvp-plan.md reference:** §8 (Phase 4), §6 (Evaluator Invariants)

---

- [ ] **Task 4.1** — `internal/engine`: pipeline skeleton, types, invariants
      **Scope:**
  - Implement `internal/engine/types.go`: `Decision`, `Action`, `ToolCall` from `mvp-plan.md §8.1`.
  - Implement `internal/engine/evaluator.go`: `Evaluator` interface.
  - Implement `internal/engine/pipeline.go`: `Pipeline` struct with an ordered `[]Evaluator` slice.
    - `Evaluate(ctx, call, session, policy)` runs each evaluator in order.
    - First non-allow decision terminates the pipeline.
    - Evaluator errors produce `Deny` decisions (never propagated to caller as errors).
    - Shadow mode conversion: if evaluator returns `Deny` or `Escalate` but effective enforcement
      mode is `shadow`, convert to `ShadowDeny` before returning.
    - Document all five invariants from `CLAUDE.md §6` in the package doc comment.
  - Write tests for the pipeline orchestration: first-failure-terminates; error→deny; shadow conversion.
  - Do not implement any evaluators yet — use a test double that returns a configurable decision.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes.
  - All five invariants from CLAUDE.md are documented in `doc.go`.
  - The pipeline can be constructed and called with zero evaluators (returns Allow).

---

- [ ] **Task 4.2** — `internal/store`: session CRUD methods
      **Scope:**
  - Implement in `internal/store/sessions.go`:
    - `CreateSession(id, agentName, policyFingerprint string) error`.
    - `GetSession(id string) (*Session, error)`.
    - `UpdateSessionTaint(id string, tainted bool) error`.
    - `IncrementSessionCounters(id string, costDelta float64) error` — increments `tool_call_count`
      and adds `costDelta` to `estimated_cost_usd` atomically.
    - `TerminateSession(id string) error`.
  - Implement `internal/store/events.go`:
    - `AppendEvent(event *SessionEvent) error` — inserts with next monotonic seq for the session.
    - `GetSessionEvents(sessionID string) ([]SessionEvent, error)` — ordered by seq ASC.
    - `CountSessionEvents(sessionID string) (int, error)`.
  - Write tests for all methods using `NewTestDB`. Test the monotonic sequence invariant explicitly:
    insert 5 events, verify seq values are 1,2,3,4,5. Test that `CountSessionEvents` reaching
    `max_history` is detectable.

  **Satisfaction check:**
  - `go test ./internal/store/...` passes.
  - Sequence numbers are monotonic and never reuse (verified in tests).
  - All methods use parameterised queries (no string interpolation in SQL).

---

- [ ] **Task 4.3** — `internal/engine`: MayUse evaluator
      **Scope:**
  - Implement `internal/engine/mayuse.go`:
    - `MayUseEvaluator` that checks `call.ToolName` against `policy.MayUse`.
    - Also injects the `check_escalation_status` virtual tool as always-allowed (regardless of `may_use`).
    - Returns `Deny` with `RuleID: "may_use"` and a message naming the tool if not in the list.
  - Full test matrix: tool in may_use → allow; tool not in may_use → deny; `check_escalation_status`
    always allow even when absent from may_use; empty may_use list → deny all.
  - Benchmark: `BenchmarkMayUseEvaluator` with a may_use list of 50 tools.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes. Benchmark runs.
  - `check_escalation_status` is always allowed.
  - No domain-specific tool names in the evaluator code.

---

- [ ] **Task 4.4** — `internal/engine`: Budget evaluator
      **Scope:**
  - Implement `internal/engine/budget.go`:
    - `BudgetEvaluator` that checks `session.ToolCallCount >= policy.Budget.MaxToolCalls` and
      `session.EstimatedCostUSD >= policy.Budget.MaxCostUSD`.
    - Returns `Deny` with `RuleID: "budget.max_tool_calls"` or `"budget.max_cost_usd"`.
    - If `Budget` is zero-valued (not set in policy), this evaluator returns `Allow` immediately.
  - Full test matrix: under budget → allow; at exact limit → deny; over limit → deny;
    no budget configured → allow; cost limit exceeded → deny; both exceeded → deny (first wins,
    report both in the reason message).
  - Benchmark: `BenchmarkBudgetEvaluator`.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes.
  - Zero-value budget (not configured) never causes a denial.

---

- [ ] **Task 4.5** — `internal/engine`: Taint evaluator
      **Scope:**
  - Implement `internal/engine/taint.go`:
    - `TaintEvaluator` implements the taint logic from `mvp-plan.md §8.4`.
    - If `session.Tainted == true` and the tool has `sequence.never_after` containing any
      taint-applying tool, return `Deny` with `RuleID: "taint.session_tainted"`.
    - Note: taint state mutations (set tainted, clear tainted) happen in the pipeline orchestrator
      AFTER the decision, not inside this evaluator. The evaluator is read-only.
  - Full test matrix: untainted session → allow; tainted session + tool that respects taint → deny;
    tainted session + tool that clears taint → allow (clearance is evaluated here, mutation is not);
    tainted session + tool with no taint rules → allow.
  - Benchmark: `BenchmarkTaintEvaluator`.
  - **After implementing:** update `Pipeline.Evaluate` in `pipeline.go` to apply taint mutations
    after the decision (set `session.Tainted = true` if allowed call has `taint.applies`; set
    `session.Tainted = false` if allowed call has `taint.clears`). Write tests for the mutations.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes.
  - Taint mutations happen in the pipeline, not the evaluator.

---

- [ ] **Task 4.6** — `internal/engine`: Sequence evaluator
      **Scope:**
  - Implement `internal/engine/sequence.go`:
    - `SequenceEvaluator` implements all three predicates from `mvp-plan.md §8.5`.
    - Query `session_events` for the session's history via the store (pass store as a dependency).
    - `only_after`: all listed tools must appear in history.
    - `never_after`: none of the listed tools may appear in history.
    - `requires_prior_n`: the named tool must appear at least N times.
    - **Collect all violations before returning** (do not short-circuit on first failure).
    - Return `Deny` with a reason string listing every violated predicate.
  - Full test matrix: all predicates satisfied → allow; each predicate violated individually → deny
    with correct reason; all three violated simultaneously → deny with all three in reason; empty
    history → only_after fails; history with exactly N-1 occurrences → requires_prior_n fails.
  - Benchmark: `BenchmarkSequenceEvaluator` with a session history of 1000 events.

  **Key performance note:** the SQL query for `GetSessionEvents` must use `ORDER BY seq ASC`. The
  evaluator should not sort in Go — sorting in the DB is cheaper. Write a `// Design:` comment here.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes.
  - Benchmark with 1000 events runs under 2ms p99.
  - All violations are reported in a single denial, not just the first.

---

- [ ] **Task 4.7** — `internal/engine`: Escalation evaluator
      **Scope:**
  - Implement `internal/engine/escalation.go`:
    - `EscalationEvaluator` implements `mvp-plan.md §8.6`.
    - Uses `gjson` to extract the argument value at `EscalateWhen.ArgumentPath`.
    - Supports operators: `>`, `<`, `>=`, `<=`, `==`, `!=` for numeric comparisons;
      `contains` and `matches` for string comparisons.
    - Before returning `Escalate`: check the `escalations` table for an existing `approved`
      record for this session + tool + arguments hash. If found, return `Allow`.
  - Full test matrix: no escalation rule → allow; rule not triggered → allow; rule triggered, no prior
    approval → escalate; rule triggered, prior approval exists → allow; invalid JSONPath → deny
    (fail closed); unsupported operator → deny (fail closed).
  - Benchmark: `BenchmarkEscalationEvaluator`.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes.
  - An approved escalation unblocks the subsequent call without re-escalating.
  - Invalid paths and operators fail closed (deny), never allow.

---

- [ ] **Task 4.8** — `internal/engine`: wire full pipeline into proxy; integration tests
      **Scope:**
  - Replace the stub `engine.Pipeline.Evaluate` in the proxy with the real pipeline containing
    all five evaluators in order: MayUse → Budget → Taint → Sequence → Escalation.
  - Remove the `// TODO(task-4.1)` comment.
  - Implement `internal/engine/integration_test.go` with one full-pipeline test per domain
    pattern from `testdata/policies/`. Test names describe the pattern, not a company:
    - `TestPaymentSequenceGuard`: verify `verify_invoice → manager_approval → execute_payment`
      is enforced; calling `execute_payment` without prior approvals is denied.
    - `TestPHITaintPropagation`: verify taint from sensitive data read blocks submission tools
      until a compliance clearance tool is called.
    - `TestClaimsSequentialGuard`: verify `requires_prior_n` blocks a finalisation tool until
      the prerequisite tool has run the required number of times.
    - `TestPrivilegedDocumentExfiltrationGuard`: verify taint on privileged document access
      blocks all outbound tools until explicit clearance.
    - `TestMultiApprovalRegulatory`: verify a filing tool cannot be called until N independent
      approval tools have fired.
  - These tests use real SQLite (NewTestDB), real policy files from `testdata/policies/`, and the full
    pipeline. They are slow tests — gate them with `//go:build integration` build tag.

  **Satisfaction check:**
  - `go test ./internal/engine/...` passes (unit tests, no build tag).
  - `go test -tags integration ./internal/engine/...` passes (integration tests).
  - All five domain pattern scenarios produce the correct decision sequence.

---

## Phase 5 — Evidence, Audit & Simulation DX

> **Goal:** Produce tamper-evident audit records. Build the simulation engine.
> Wire the audit commands to real data.
> **Delivers:** `truebearing audit verify`, `audit query`, `audit replay`, `truebearing simulate`.
> **mvp-plan.md reference:** §9 (Phase 5)

---

- [ ] **Task 5.1** — `internal/audit`: AuditRecord, signing, verification
      **Scope:**
  - Implement `internal/audit/record.go`: `AuditRecord` struct from `mvp-plan.md §9.1`
    (including `ClientTraceID` field from §9.1a).
  - Implement `internal/audit/sign.go`:
    - `Sign(record *AuditRecord, privateKey ed25519.PrivateKey) error` — computes canonical JSON
      (sorted keys, no signature field), signs it, base64-encodes, sets `record.Signature`.
    - `Verify(record *AuditRecord, publicKey ed25519.PublicKey) error` — reconstructs canonical JSON,
      verifies signature.
  - Implement `internal/audit/writer.go`:
    - `Write(record *AuditRecord, store *store.Store) error` — inserts into `audit_log` table.
    - The pipeline must call this after every decision (see invariant 1 in CLAUDE.md §6).
  - Write tests: sign → verify round-trip; tampered record fails verification (flip one byte in `decision`);
    canonical JSON is stable (field order doesn't matter for verification).

  **Satisfaction check:**
  - `go test ./internal/audit/...` passes.
  - A record with a tampered field fails `Verify`.

---

- [ ] **Task 5.2** — `internal/store`: audit log query methods
      **Scope:**
  - Implement `internal/store/audit.go`:
    - `QueryAuditLog(filters AuditFilter) ([]AuditRecord, error)`.
    - `AuditFilter` struct: `SessionID string`, `ToolName string`, `Decision string`,
      `TraceID string`, `From time.Time`, `To time.Time`. All fields are optional (zero value = no filter).
    - Build the query dynamically. Use parameterised placeholders.
  - Write tests: query by each filter field individually; combined filters; empty result; trace ID filter.

  **Satisfaction check:**
  - `go test ./internal/store/...` passes.
  - No string interpolation in the query builder.

---

- [ ] **Task 5.3** — `cmd/audit`: wire up `audit verify`, `audit query`, `audit replay`
      **Scope:**
  - `audit verify <file>`: reads a JSONL audit log file, verifies each record's signature using the
    proxy's public key. Prints `OK` or `TAMPERED` per line. Exit non-zero if any `TAMPERED`.
  - `audit query`: calls `store.QueryAuditLog` with flags mapped to `AuditFilter`. Supports
    `--session`, `--tool`, `--decision`, `--trace-id`, `--from`, `--to`, `--format` (table/json/csv).
  - `audit replay <file> --policy <file>`: reads a JSONL trace file (not audit log — these are raw
    MCP request traces), re-runs each call through the evaluation pipeline in memory against the
    given policy, prints a diff table showing changed decisions.

  **Satisfaction check:**
  - `audit verify` correctly identifies tampered records in a test fixture.
  - `audit query --decision deny` returns only deny records.
  - `audit replay` runs without a live proxy or upstream.

---

- [ ] **Task 5.4** — `truebearing simulate`
      **Scope:**
  - Implement `cmd/simulate.go` with the full simulation engine from `mvp-plan.md §9.2`.
  - Reads a `--trace` JSONL file of raw MCP tool call requests.
  - Optionally reads `--old-policy` to show a diff between two policy versions.
  - Runs the full pipeline in memory against a fresh in-memory SQLite database.
  - Prints the coloured diff table format from the plan.
  - Does not write to the persistent database. Does not contact an upstream.
  - Create `testdata/traces/payment-sequence-violation.trace.jsonl` — a sample trace representing
    the payment sequential approval pattern: read_invoice → verify_invoice → execute_payment, where
    the third call violates policy (manager_approval was skipped). Used as the canonical demo trace.

  **Satisfaction check:**
  - `truebearing simulate --trace testdata/traces/payment-sequence-violation.trace.jsonl
--policy testdata/policies/fintech-payment-sequence.policy.yaml` prints the diff table correctly.
  - The `execute_payment` call shows as `DENY` (missing manager_approval).

---

- [ ] **Task 5.5** — `internal/escalation`: escalation state machine and commands
      **Scope:**
  - Implement `internal/escalation/escalation.go`:
    - `Create(sessionID string, seq uint64, toolName string, argumentsJSON string, store *store.Store) (string, error)` — creates a pending escalation, returns ID.
    - `Approve(id string, note string, store *store.Store) error`.
    - `Reject(id string, reason string, store *store.Store) error`.
    - `GetStatus(id string, store *store.Store) (string, error)` — returns `pending | approved | rejected`.
  - Wire the `check_escalation_status` virtual tool into the proxy: when the engine receives a
    `tools/call` for `check_escalation_status`, intercept it before forwarding, call `GetStatus`,
    and return a synthetic MCP response. Never forward this to the upstream.
  - Implement `cmd/escalation/list.go`, `approve.go`, `reject.go` using the above.
  - Write tests: create → get status (pending); approve → get status (approved); reject → get status
    (rejected); approve non-existent ID → error.

  **Satisfaction check:**
  - `truebearing escalation list` shows pending escalations.
  - `truebearing escalation approve <id> --note "CFO approved"` transitions status.
  - `check_escalation_status` tool calls are never forwarded upstream.

---

- [ ] **Task 5.5a** — Escalation webhook notifications
      **Scope:**
  - When an escalation is created (in `internal/escalation/escalation.go`), fire a notification.
  - Notification target is configured via `escalation.webhook_url` in the policy YAML or in
    `~/.truebearing/config.yaml`. If neither is set, fall back to stdout (a structured JSON line).
  - Implement `internal/escalation/notify.go`:
    - `Notify(esc *Escalation, cfg *NotifyConfig) error`
    - If `cfg.WebhookURL != ""`: POST the escalation payload as JSON to the URL.
      Use a 5-second timeout. Log the error if the POST fails; do not block the escalation creation.
    - If `cfg.WebhookURL == ""`: write a structured JSON line to stdout.
  - Webhook payload:
    ```json
    {
      "event": "escalation.created",
      "escalation_id": "esc_abc123",
      "session_id": "sess_xyz",
      "tool": "execute_payment",
      "reason": "amount_usd 15000 > threshold 10000",
      "approve_cmd": "truebearing escalation approve esc_abc123",
      "reject_cmd": "truebearing escalation reject esc_abc123 --reason "...""
    }
    ```
  - Add `escalation:` block to the policy DSL (update parser in `internal/policy/types.go`):
    ```yaml
    escalation:
      webhook_url: https://hooks.slack.com/services/...
    ```
  - Write tests: webhook fires on escalation create; webhook failure is logged, not fatal;
    stdout fallback works when no URL configured.

  **Why this matters for the demo:** without notifications, human escalation requires the operator
  to poll `truebearing escalation list` in a terminal and notice a new entry. That is not a
  product — it is a workaround. A Slack webhook means the demo shows a real approval workflow:
  call blocked → Slack message arrives → operator runs approve command → agent continues.
  That is the human-oversight story that EU AI Act Article 9 asks for, made visible.

  **Satisfaction check:**
  - Starting the proxy with a `webhook_url` configured: creating an escalation fires a POST.
  - Starting the proxy without a `webhook_url`: creating an escalation prints to stdout.
  - Webhook POST failure does not crash the proxy or prevent the escalation from being recorded.

---

- [ ] **Task 5.6** — `cmd/session`: wire up session commands
      **Scope:**
  - `session list`: queries all non-terminated sessions from the store. Prints table with ID, agent,
    policy fingerprint (short), tainted (yes/no), call count/max, cost/budget, age.
  - `session inspect <id>`: calls `store.GetSessionEvents` and prints full history in a table.
  - `session terminate <id>`: calls `store.TerminateSession`. Subsequent tool calls on this session
    return `410 Gone`.
  - Implement the `410 Gone` check in the proxy: after loading a session, check `session.Terminated`.

  **Satisfaction check:**
  - All three commands work against a live database.
  - Terminated session tool calls return `410` with a clear message.

---

## Phase 6 — SDKs & Integration Story

> **Goal:** Make the 2-line integration real. Ship Python and Node SDKs.
> **Delivers:** `pip install truebearing` and `npm install @mercator/truebearing` provide
> `PolicyProxy` that wraps any Anthropic/OpenAI client transparently.
> **mvp-plan.md reference:** §10 (Phase 6)

---

- [ ] **Task 6.1** — Python SDK: `truebearing` package on PyPI
      **Scope:**
  - Create `sdks/python/` directory.
  - Implement `PolicyProxy` class that:
    - Generates a `uuid4` session ID on instantiation.
    - Accepts `session_id=` kwarg for explicit session continuity.
    - Locates the proxy (subprocess or explicit `proxy_url`).
    - If no `proxy_url`, spawns `truebearing serve` as a subprocess on a random open port,
      waits for it to be ready (poll `/health` endpoint).
    - Injects `Authorization: Bearer <jwt>` and `X-TrueBearing-Session-ID` headers into
      every outbound request.
    - Reads JWT from `~/.truebearing/keys/<agent_name>.jwt` or `TRUEBEARING_AGENT_JWT` env var.
    - Shuts down the subprocess on `__del__` / context manager exit.
  - Write a `pyproject.toml`. Publish to PyPI (or TestPyPI for MVP).
  - Write tests: `PolicyProxy` injects headers; subprocess mode starts and stops cleanly.

  **Satisfaction check:**
  - `pip install truebearing` installs cleanly.
  - The 2-line integration from `mvp-plan.md §16` works against a local MCP test server.

---

- [ ] **Task 6.2** — Node.js SDK: `@mercator/truebearing` package on npm
      **Scope:**
  - Create `sdks/node/` directory.
  - Implement TypeScript `PolicyProxy` class with identical behaviour to the Python SDK.
  - Write `package.json`, build with `tsc`, publish to npm.
  - Write tests using Jest or Vitest.

  **Satisfaction check:**
  - `npm install @mercator/truebearing` installs cleanly.
  - The 2-line integration works from a TypeScript project.

---

---

## Phase 7 — Demo Readiness

> **Goal:** Transform a working codebase into something a design partner engineer can pick up
> on their own, get running in ten minutes, and understand immediately.
> This phase is documentation and DX polish. It is not optional — it is the difference
> between a demo that converts and one that doesn't.
> **No new engine logic. No new CLI commands (except `init`).**

---

- [ ] **Task 7.1** — `README.md`: full quick-start guide
      **Scope:**
  - Replace the "under construction" README with a complete quick-start guide.
  - A design partner engineer must be able to go from zero to a confirmed blocked tool call
    in under ten minutes, following only the README.
  - Structure:
    1. **What TrueBearing is** — one paragraph. The sequence problem, not the permissions problem.
    2. **Install** — single command (brew / curl install script / go install).
    3. **Write a policy** — show the shortest possible meaningful policy (3–4 tools, one
       `only_after` guard, a budget). Use a generic agent name (`payments-agent`, `data-agent`).
    4. **Register your agent** — one command, show the output.
    5. **Start the proxy** — one command.
    6. **Make a tool call** — show a `curl` example calling the proxy with the correct headers.
       Show what an allowed response looks like. Show what a denied response looks like.
    7. **Check the audit log** — `truebearing audit query`, show sample output.
    8. **Next steps** — link to `docs/policy-reference.md`, `docs/demo-script.md`, policy packs.
  - The README must not mention any specific company name. Examples use generic agent and tool names.
  - After writing, time yourself following it from scratch. If it takes more than 10 minutes,
    find the friction and remove it.

  **Satisfaction check:**
  - A reader with no prior TrueBearing knowledge can follow it to a working blocked call.
  - `policy lint` passes on every YAML snippet shown in the README.
  - No company names.

---

- [ ] **Task 7.2** — `docs/demo-script.md`: meeting narrative and command sequence
      **Scope:**
  - Write the script for a 20-minute technical demo to an engineer at a design partner company.
  - The script is operator-agnostic — it works for a payments company, a healthcare company,
    or a DevOps company by swapping one policy file. The narrative structure is the same.
  - Structure:
    1. **The setup** (2 min): "Your agent had permission. That wasn't the problem." Show a
       policy with a sequence guard in plain YAML. Run `truebearing policy explain`. The engineer
       reads it and immediately understands what is enforced.
    2. **The integration** (3 min): show the Python SDK. Two lines. Nothing else changes.
       Explain that the agent code is untouched.
    3. **Shadow mode** (3 min): start the proxy in shadow mode. Make a sequence-violating tool
       call. Show it passes through. Show the audit log entry marked `shadow_deny`. Explain
       this is how you deploy safely — watch for a week before enforcing.
    4. **The block** (5 min): flip to `enforcement_mode: block`. Repeat the sequence-violating
       call. It is denied. Show the exact denial reason. Show the audit record, signed.
       Run `truebearing audit verify` — every record shows `OK`.
    5. **The escalation** (5 min): trigger an escalation (high-value threshold). Show the
       webhook/stdout notification. Run `truebearing escalation approve`. Show the agent
       continuing. This is the human oversight story.
    6. **The simulation** (2 min): run `truebearing simulate` on a captured trace.
       Show the diff. "This is what would have happened if this policy had been running last week."
  - Include the exact commands to run at each step, with expected output shown.
  - Note which policy file to load for each vertical (fintech, healthcare, devops) so the
    presenter can swap in one flag and make the demo relevant to whoever is in the room.

  **Satisfaction check:**
  - Someone who has not built this system can run the demo end-to-end from the script alone.
  - Every command in the script has been verified to produce the shown output.

---

- [ ] **Task 7.3** — `truebearing init`: interactive policy scaffolder
      **Scope:**
  - Implement `cmd/init.go` with the interactive scaffolder from `mvp-plan.md §18 Addition 8`.
  - This command is the entry point for an engineer who has never written a TrueBearing policy.
    It must generate a valid, lint-clean policy from 5 questions — no prior DSL knowledge needed.
  - Prompts (in order):
    1. Agent name (e.g. `payments-agent`)
    2. List the tools your agent uses (comma-separated)
    3. Which tools are high-risk and must be sequence-guarded? (subset of above)
    4. For each high-risk tool: what must happen first? (maps to `only_after`)
    5. Set a budget: max tool calls per session, max cost in USD
  - Generated policy uses `enforcement_mode: shadow` by default. A comment in the file
    explains how to change it to `block` once the operator has reviewed the shadow logs.
  - After generating, automatically run `policy lint` on the output and show the results.
  - Print the next-steps checklist:
    ```
    ✓ Created truebearing.policy.yaml
    Next steps:
      1. truebearing agent register <agent-name> --policy truebearing.policy.yaml
      2. truebearing serve --upstream <your-mcp-url> --policy truebearing.policy.yaml
      3. Run your agent for a week in shadow mode
      4. truebearing audit query --decision shadow_deny   (review what would have been blocked)
      5. Set enforcement_mode: block in truebearing.policy.yaml when you're ready
    ```
  - Placing `init` in Phase 7 (not Phase 6) is deliberate: this command is part of the
    onboarding experience, not the core engine. It should be built after everything it
    references (policy validation, linting, agent register) is complete and stable.

  **Satisfaction check:**
  - Running `truebearing init` and answering 5 questions produces a file that passes `policy lint`
    with zero ERRORs.
  - Generated file uses `enforcement_mode: shadow` by default.
  - The next-steps checklist references only commands that exist and work.

---

## Policy Packs

> These are created alongside or after Phase 6. They are standalone YAML files, not Go code.

---

- [ ] **Task P.1** — `policy-packs/fintech/`: fintech starter pack
      **Scope:**
  - `policy-packs/fintech/payments-safety.policy.yaml` — sequential approval guard pattern for
    payment workflows, with inline comments explaining each rule and the risk it mitigates.
  - `policy-packs/fintech/README.md` — when to use each rule, what risks it mitigates.
  - All files must pass `policy lint` with zero ERRORs.

---

- [ ] **Task P.2** — `policy-packs/healthcare/`: healthcare starter pack
      **Scope:**
  - `policy-packs/healthcare/hipaa-phi-guard.policy.yaml` — taint on PHI access,
    block exfiltration tools until compliance scan runs.
  - `policy-packs/healthcare/README.md`.

---

- [ ] **Task P.3** — `policy-packs/devops/`: DevOps starter pack
      **Scope:**
  - `policy-packs/devops/production-guard.policy.yaml` — environment isolation,
    sequence guards on deploy workflows.
  - `policy-packs/devops/README.md`.

---

## Maintenance Notes

> This section is updated as decisions are made during development.
> Add entries here when something is discovered that affects future tasks.

_(empty — populated as development progresses)_

---

## Decisions Log

> When a design decision is made that deviates from or extends `mvp-plan.md`, log it here
> with the task number and rationale. These decisions inform future tasks.

_(empty — populated as development progresses)_
