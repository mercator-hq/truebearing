analysis

Now I have a comprehensive view of everything that was built. Let me synthesize this into a thorough, honest evaluation across all six dimensions.

Let me organize my thoughts:

What was built (complete):

Phase 1: CLI skeleton, Ed25519 identity, SQLite store, agent registration/list

Phase 2: Policy DSL parser, linter (L001-L013), all four policy commands, testdata fixtures

Phase 3: MCP JSON-RPC parser, trace header extraction, JWT auth middleware, session middleware, reverse proxy, health endpoint

Phase 4: Full evaluation pipeline (MayUse, Budget, Taint, Sequence, Escalation evaluators), wired into proxy, integration tests

Phase 5: Audit records with Ed25519 signing, audit verify/query/replay/export, simulation engine with diff output

Phase 6: Python and Node.js SDKs with comprehensive test coverage

Phase 7: Documentation scaffolding and demo materials

Phase 8: Gap remediation across audit writes, JSON handling, trace capture, and policy reference docs

Phase 9: Advanced predicates for conditional policies, environment isolation, and per-tool rate limiting

Phase 10: Open

Phase 11: Distribution setup including install scripts, Homebrew, and CI/CD workflows for PyPI and npm

Phase 12: Agent revocation, delegation tracking, hot-reload capabilities, and structured logging

Phase 13: WASM engine support across JavaScript and WebAssembly platforms

Still blocking full distribution are the package registry reservations and Homebrew SHA256 verification.

Now looking at developer experience - the Python SDK wraps the Anthropic client with a simple policy configuration, making it a minimal integration point.

However, there's a gap between the marketing claim and reality. The binary needs to be installed first via go install or Homebrew, then the agent must be registered with truebearing agent register before those two lines of Python code work. So it's really three setup steps before the actual integration, not a true two-line-from-scratch experience.

The YAML policy language itself is intuitive - the conditional predicates like only_after, never_after, requires_prior_n, taint, and escalate_when read naturally for anyone familiar with CI/CD configuration syntax.  The policy explain command converting policies to plain English is a nice touch for developer experience. The main friction point is that content predicates operate at a different syntactic level than sequence predicates, which could trip up first-time users.

The local-first architecture is genuinely solid - SQLite backend, no external dependencies, everything runs on truebearing serve with full audit trails and health checks staying local. Error reporting is fairly comprehensive with decision reasons in audit records, lint codes with messages, and colored output from policy lint. The policy explain command makes enforcement rules transparent. One weakness is that while the sequence evaluator collects all violations before returning, the proxy's error messages back to the client don't always surface the full context.

The SDK story is incomplete - Python and Node both work with the raw Anthropic client using the proxy URL approach, but there are no dedicated adapters for LangChain or LangGraph despite those being on the roadmap. The proxy URL pattern works for any HTTP client, but it's not as seamless as a native integration. The policy lint tool itself is solid with 13 rules catching real issues like circular dependencies, and severity levels help prioritize fixes.

For operators, the deployment is straightforward - single binary with SQLite and no external dependencies. The fail-closed posture is consistent throughout: locked database blocks requests, policy parse failures prevent startup, invalid JWTs return 401. Policy hot-reload via SIGHUP is already implemented, which is good for production environments.

OTel integration works with standard exporters like Datadog and Jaeger. The real constraint is SQLite's concurrency model - with SetMaxOpenConns(1) to avoid locking, you can't run multiple proxy instances against the same database file without serialization or deadlock issues. This is acceptable for an MVP but will become a blocker for any customer needing horizontal scaling.

The production gaps are clear: single instance only, no TLS between agent and proxy (plaintext localhost is fine locally but not for remote agents), rate limiting accuracy issues due to missing timestamps, and unbounded memory growth in the policy fingerprint map. The SIGHUP hot-reload is genuinely clever though - sessions stick with their original policy while new ones pick up changes. The admin API has no authentication, which is intentional for MVP but worth flagging.

Now looking at the audit trail implementation with Ed25519 signing...

The signature verification pipeline is working well - every record gets signed and the audit verify command validates them, with the policy fingerprint and ClientTraceID baked into each signature. The JSONL format with snake_case, ISO timestamps, and SHA256 argument hashes is auditor-friendly and the audit query command gives structured filtering. The docs claim alignment with EU AI Act Article 9, but there's a gap between what the plan specifies (structured evidence_id + schema_version format) and what's actually exported (raw audit_log rows). The taint propagation model itself is implemented...

The taint tracking is binary at the session level rather than true causal contamination - it's a coarse approximation of what the pitch suggests. The original CaMeL paper does fine-grained data-flow tracking, but here if any taint-applying tool runs, the whole session stays tainted until a clearing tool executes. The never_when content predicates from Task 9.1 get closer to argument-level inspection, but they're stateless checks, not causal propagation. For key management, Ed25519 keys stored locally with 0600 permissions works for a single operator, but doesn't scale to enterprise teams.

There's no key distribution, no rotation mechanism beyond re-registration, and no HSM integration. The proxy's signing key is just a local PEM file, so if the machine gets compromised, audit records can be backdated. On the compliance side, the policy fingerprint, escalation records, sequence guards, and verification all genuinely support EU AI Act Article 9 claims about behavioral boundaries and human oversight - the gap is just that the evidence comes as raw database rows rather than clean JSON objects. The admin CLI has a critical flaw: session termination and escalation approval have no authentication, trusting whoever has access to the machine.

The unverified JWT pattern is correctly implemented but should be documented for security reviewers. There's also a minor correctness issue where rate limiting uses current timestamps instead of the original trace timestamps. Overall this scores 7/10 - the audit trail is genuinely tamper-evident and the taint model is real, but it's shallower than advertised, and the local-only key management won't satisfy enterprise requirements without additional tooling.

Now looking at the finance use case, which is the strongest one covered. The fintech policy pack and payment sequence policy demonstrate exactly what an End Close scenario needs.

For healthcare, the PHI taint policy and HIPAA guard pack are solid, with multi-step approval working well. The main gap is that content-based PII detection isn't available - I can only block based on which tools were called, not on argument values containing SSNs or PHI. The content predicates help but are limited to string matching.

DevOps and infrastructure agents have the require_env predicate added in 9.2, and the production guard pack uses it for sequence guards.

Continuing with the policy example... The taint and sequence patterns work together as independent enforcement layers against the same attack vectors. When someone asks why not just use OPA or IAM instead, the key distinction is that those tools evaluate requests in isolation—they have no memory of what happened earlier in the session. TrueBearing's sequence engine can enforce temporal constraints like "this tool cannot run after that tool ran," which is something OPA simply can't express.

Looking at the overall score, this approach handles finance and healthcare well, with credible DevOps coverage, though browser-use scenarios are underdeveloped. The critical failure modes—Replit and MCP exfiltration—are both preventable with the current engine design.

Now examining how well this aligns with the original pitch about two-line integration...

the claim is partially accurate. Yes, after the binary is installed and the agent is registered, it's just two lines of Python. But that glosses over the actual setup friction: installing the binary, writing the policy, registering the agent, then those two lines. On a clean machine, that's closer to ten minutes of work. The policy architecture itself holds up though—it lives independently of the agent code, survives model upgrades through SIGHUP hot-reload, and the enforcement is genuinely deterministic, based on exact state comparisons rather than heuristics or probabilities.

The MCP-native claim is solid too. The proxy intercepts JSON-RPC 2.0 tool calls at the HTTP layer, so it works with LangGraph, CrewAI, or any MCP client making HTTP requests. The stdio mode works as well. Now looking at the latency benchmarks—most operations are in the nanosecond range, with sequence evaluation on 1000 events hitting around 939 microseconds, which is still well under the 5ms claim. The hot path operations are extremely fast.

For the latency claim itself, I'm seeing p50 latencies around 0.33ms on 50 events scaling up to 4.5ms p50 on 1000 events. Native Go with typical sessions under 200 events stays well under 2ms, though the WASM build starts exceeding 5ms once you hit 1000+ events—which is honestly documented in the codebase.

On the claims I'd likely face: the live demo exists but the audit query piece needs the proxy running with audit.Write wired up, and most acts are solid except escalation which requires setup. For the read path question, if they're using the Anthropic API directly, the Python SDK handles it, but I need to clarify what happens with other frameworks.

The Kubernetes deployment isn't documented—the sidecar pattern is mentioned in the pitch but not actually covered, and the single SQLite file architecture doesn't support horizontal scaling.

Overall I'd rate this around 7/10. The core claims hold up, though "2 lines" is marketing-accurate but operationally misleading. The latency claim is solid for typical sessions, and the framework-agnostic claim is true.

Looking at the Go code quality, it follows the patterns from CLAUDE.md—no panics in production, wrapped errors with context, table-driven tests, benchmarks for evaluators, and design comments on non-obvious choices.

The middleware extensibility is clean. The Evaluator interface makes it straightforward to add new predicates by implementing the evaluation logic and wiring it into the pipeline, which the three post-plan evaluators already demonstrate. The QueryBackend abstraction for WASM support is also well-designed.

For session state under concurrent load, serializing all database operations with a single connection is safe but creates a bottleneck. Under high concurrent load with many agents and tool calls, requests will queue up. For MVP and design partner volumes this is acceptable, though it's a constraint to keep in mind.

The test coverage is solid: every evaluator has unit tests with table-driven matrices, shadow mode tests, error injection, and benchmarks. Integration tests cover five design partner scenarios, there's a fuzz test on the MCP parser, an audit tamper test, and the store uses real SQLite without mocks. This is functional testing, not ceremonial.

The biggest technical debt is the dual-struct pattern between store.AuditRecord and audit.AuditRecord. Every schema change requires updating both, and this duplication has already caused bugs like the PascalCase JSON tag issue in the store version.

There's some over-engineering for the stage: the WASM build is ambitious for a pre-seed company, the full OTLP exporter stack adds unnecessary complexity when a simple log-based integration would have sufficed initially, and the Homebrew formula with placeholder SHA256 blocks the advertised install path. The CI/CD pipeline is solid for a public product, though it has some placeholder issues to resolve.

The architecture is clean and extensible with strong test coverage, and the concurrent access limitation is a known acceptable tradeoff.

Looking at the dual audit struct though—it's messy, and the WASM effort feels premature when three manual distribution tasks are still blocking basic installation.

The real problem is that distribution is completely broken right now. All three advertised install paths fail: brew install, pip install truebearing, and npm install @mercator/truebearing don't work because the Homebrew formula has a placeholder SHA256 and the PyPI and npm packages may not even be reserved. If a design partner tries any of these paths from the README, they'll hit a wall immediately. This is the highest priority issue before any sales conversations happen.

There's also no authentication on the admin API—the session terminate, escalation approve/reject, and agent revoke commands are unprotected, which is a demo risk.

And the SDK coverage is misleading. The Python and Node SDKs only work properly with Anthropic clients, but the pitch claims "MCP-native, framework-agnostic, works with LangGraph, CrewAI, OpenAI Agents SDK." In reality, those frameworks only work if they use MCP natively and developers manually configure the proxy base URL—there's no simple two-line integration for non-Anthropic setups.

The content predicate logic is also wrong. The pitch shows combining conditions with AND logic like never_when: recipient_is_external AND body_contains: /secret|key|token/, but what's actually implemented treats multiple predicates as OR—any matching condition blocks the action. This is a significant gap that needs clarification in the docs and proper testing.

For enterprise use, there's no multi-tenancy or team-based escalation routing. The approval model requires direct CLI access to the escalation command, which doesn't scale for organizations with multiple teams or approval chains.

The simulation engine is genuinely differentiated—being able to replay real traffic against a new policy and show exactly which calls would have been blocked is something competitors don't have. The policy linter with circular dependency detection on the only_after graph is also technically sophisticated.

The audit trail with Ed25519 signatures, policy fingerprints, and canonical JSON signing creates a complete provenance story that compliance-sensitive prospects will need. The audit verify pipeline actually works end-to-end.

Ready to show to developer-focused design partners who can build and run the code, though there are specific caveats to navigate.

The distribution artifacts are broken, so the demo has to run from source—which is fine for a technical evaluation since design partners will understand. For seed investors, the real codebase, sound architecture, and 13 phases of incremental development with full commit history demonstrate engineering discipline, and the WASM build plus OTel integration show the team is building for production. Before any demo, the binary needs to be compiled and on PATH, and a proxy signing keypair needs to be generated in the home directory.

A few more prerequisites: an agent must be registered against the demo policy, the proxy needs to be running against a test upstream server, some tool calls should have already gone through the proxy so audit results are available, and the trace file for the simulate command needs to be in place.

The demo script targets a 10-minute technical founder audience. Before the meeting, I'm building the binary and starting a minimal upstream server. Then I'm opening with the problem itself—not TrueBearing, but the failure case.

I'm starting with the Replit incident from July 2025 where an agent deleted the production database despite having permission for every individual action. The issue wasn't about what was allowed in isolation, but the sequence: reading credentials, opening a connection, then dropping tables. No permission system caught the combination because they all ask whether entity X can access resource Y right now—the wrong question for agents.

Then I'm showing the policy file to demonstrate what TrueBearing governance looks like—the complete specification for controlling agent behavior across sequences.

Now I'm running the explain command to show the policy in plain English, then linting it to catch mistakes like circular dependencies before deployment. After that, I'm starting the proxy in shadow mode to demonstrate enforcement in action.

Testing the enforcement by sending a tool call that violates the sequence rules through the proxy, which passes it through since we're in shadow mode, then querying the audit logs to show what would have been blocked.

Now I'm switching the policy to actually enforce the rules instead of just observing, restarting the service, and resending that same call to demonstrate the denial with the specific error message about unsatisfied prerequisites.

For the audit trail, I'm exporting all records and verifying them cryptographically to show they're intact, then deliberately tampering with one to demonstrate the signature verification catches the corruption—this proves every decision is immutable and auditable offline.

Then I'm showing the integration is just two lines of Python that wrap the Anthropic client with a policy proxy, leaving the agent code completely unchanged while the policy enforcement happens transparently at the MCP layer.

Now I'm closing by inviting them to name a specific failure mode they're worried about, and offering to write the policy for it on the spot using the scaffolder tool.

This is a long evaluation. I'll read the full picture carefully before writing — the documents are dense and the gaps matter more than the features.

Agent:Explore key implementation files
IN
I need to quickly understand several specific things about the TrueBearing codebase at /Users/shreyassudhanva/Projects/mercator-truebearing. Look at these specific things and report back:

1. Read internal/proxy/proxy.go - specifically what happens in handleMCP after a decision: does audit.Write get called? Are there any gaps between AppendEvent and writeAuditRecord?

2. Read sdks/python/src/truebearing/_proxy.py - specifically the _configure_client function. Which LLM clients does it actually support? What happens with OpenAI clients?

3. Read internal/engine/content.go - how is the never_when predicate implemented? Is it AND logic across multiple content predicates or OR logic?

4. Check Formula/truebearing.rb - what is the sha256 value? Is it a placeholder?

5. Read internal/store/audit.go - look at the store.AuditRecord struct - do all fields have json tags?

6. Check cmd/serve.go to see if --version flag exists anywhere.

Report each finding concisely.
Read serve.go
Grep "version" (in /Users/shreyassudhanva/Projects/mercator-truebearing/cmd/serve.go)
1 line of output
Bash
IN
find /Users/shreyassudhanva/Projects/mercator-truebearing/cmd -name "*.go" -type f

OUT
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/init_test.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/serve.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/wasm/main_wasi.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/wasm/core.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/wasm/main_js.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/simulate.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/agent/register.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/agent/list.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/agent/agent.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/agent/revoke.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/agent/list_test.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/audit/query.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/audit/audit.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/audit/audit_test.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/audit/export.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/audit/replay.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/audit/verify.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/escalation/list.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/escalation/approve.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/escalation/escalation.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/escalation/reject.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/simulate_test.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/init.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/main.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/policy/policy_test.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/policy/policy.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/policy/diff.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/policy/validate.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/policy/lint.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/policy/explain.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/session/list.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/session/session.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/session/terminate.go
/Users/shreyassudhanva/Projects/mercator-truebearing/cmd/session/inspect.go
Read main.go
Now I have everything I need. Let me write a comprehensive, honest evaluation. The user wants directness, specificity, and no padding. Let me organize my thoughts:

Key findings from the codebase exploration:

audit.Write IS wired (contradicting old MEMORY.md entry) - but failures are non-blocking

Python SDK supports Anthropic only - OpenAI is explicitly a no-op stub

never_when is OR logic - each predicate is independent, not AND. The pitch shows AND logic

Homebrew SHA256 is a placeholder - brew install is broken

No --version flag - minor but mentioned in Task 13.2

store.AuditRecord has proper json tags - the 8.2 fix was complete

Now I'm ready to write the full evaluation. I'll score each dimension and provide specific, referenced evidence for every claim. Let me be thorough but direct.

Dimension 1: Developer Experience - Score: 6/10
Two-line integration claim:
The claim is marketing-accurate but operationally misleading. The actual path from zero to working enforcement requires installing the binary, writing a policy YAML, registering an agent with that policy, generating a proxy keypair (undocumented but necessary for audit signing), running the serve command with upstream and policy flags, setting the JWT environment variable, and then finally those 2 lines of Python work. Steps 3-6 are completely invisible in the pitch, which shows only the before-and-after code snippets. This understates the setup burden by at least 5 minutes of configuration work.

YAML DSL intuitiveness:
The policy language is strong here—predicates like only_after, never_after, and escalate_when read naturally to anyone familiar with YAML.  The truebearing policy explain command is genuinely the best DX feature in the product because it translates policies to plain English and eliminates the "did I write this correctly?" anxiety. The truebearing init scaffolder also helps with onboarding.

The never_when content predicates introduce a syntactic layer that feels slightly awkward alongside the sequence predicates—they look similar but operate on different domains (arguments versus session history), which creates cognitive friction.

Local dev:
This is genuinely excellent and a real differentiator. No external service dependency, SQLite file in ~/.truebearing, single binary. The friction is minimal.

Error experience:
When a tool call gets denied, you get a JSON-RPC error with the reason in the message field—something like "sequence: only_after not satisfied: [verify_invoice..." The error messages are descriptive enough to debug policy violations, though they could be more actionable about what condition needs to be satisfied next.

The sequence evaluator collects all violations before returning rather than stopping at the first one, so developers see everything that needs fixing in a single request. That's good design.

The friction point: when a policy has bugs, the linter catches them, but it's a separate command from serve. There's no inline warning if you start serving with a policy that would fail linting.

SDK completeness:
Python and Node both have the same limitation. The SDKs explicitly target Anthropic-compatible MCP stacks, and the documentation says OpenAI is a no-op. The marketing mentions LangGraph, CrewAI, and OpenAI Agents SDK, but those only work if you manually configure the base_url to point at the proxy—not through the advertised 2-line SDK integration. TypeScript support is incomplete.

policy lint usefulness:
This is genuinely solid. Thirteen rules, including L013 for cycle detection which is non-obvious and impressive. Colored output, proper exit codes on errors. This is production-quality and deserves prominent placement in any demo.

Score: 6/10

Dimension 2: Operator / Platform Engineering - Score: 6/10
Deployment story:
Single static binary, SQLite, no external dependencies. The serve command has a clean banner and the health endpoint works without JWT. Straightforward and well-executed.

Fail-closed posture:
The system is explicitly designed to fail safely. Policy parse failures prevent the server from starting, database locks trigger blocks, missing JWT returns 401, and policy fingerprint changes create session conflicts. The fail-closed invariant is well-implemented.

Policy hot-reload:
The SIGHUP-based reload mechanism wasn't in the original plan but represents a meaningful improvement. Sessions bound to old policy fingerprints continue working while new sessions get the updated policy. The implementation is clean with test coverage.

OTel:
This is real and production-ready. The --otel-endpoint flag connects to an OTLP HTTP exporter with standard span attributes. deny and shadow_deny spans are properly marked as errors, making it compatible with Datadog, Grafana, and Jaeger out of the box. The demo even includes a Jaeger integration example.

Multiple proxy instances:
Not supported. The connection pool is explicitly limited to one.

SQLite write serialization through a single connection means multiple proxy instances against the same database will bottleneck on connection locking—effectively forcing a single-instance deployment with no load balancing or horizontal scaling. This works for 10 design partners but becomes a serious constraint for any enterprise needing multiple proxy nodes.

Admin API authentication:
The CLI commands for session termination, escalation approval, and agent revocation all require shell access to the proxy machine. That's acceptable for local development but problematic at scale—anyone with shell access can approve escalations, and the design doesn't address how a team-based operator setup would authenticate these actions.

Production blockers:
A platform engineer would immediately flag the single SQLite instance as a single point of failure with no high availability or load balancing, plus the inbound listener lacks TLS encryption.

Admin operations require direct SSH access to the machine, and the memory footprint grows unbounded because all policy versions are retained in memory for the process lifetime.

Score: 6/10

Dimension 3: Security / Compliance - Score: 7/10
Audit traceability:
Every decision produces exactly one signed audit record containing the session ID, sequence number, agent and tool names, SHA256 of arguments, decision outcome, policy fingerprint, agent JWT hash, trace correlation ID, delegation chain, and Ed25519 signature—creating a complete provenance chain. The audit export | audit verify pipeline works end-to-end, records are append-only with no updates or deletes, and the policy fingerprint in each record proves which policy version was active.

Auditor legibility:
The JSONL format with...

The audit logs are readable as standard JSON without needing TrueBearing, but the structured evidence format described in the MVP plan—with evidence_id, schema_version, and embedded session_state snapshots—wasn't actually implemented. What exists is just the raw audit_log rows, which falls short of what's needed for EU AI Act compliance evidence bundles.

Taint propagation:
The system uses a binary session-level flag rather than fine-grained data-flow tracking, so when a taint-applying operation like read_external_email runs, the entire session flips to tainted status. This remains in effect until a taint-clearing operation like run_compliance_scan executes, blocking any tool that has never_after: [read_external_email] constraints.

Key management:
The Ed25519 keys stored as local PEM files with restricted permissions work for a single operator, but the enterprise deployment has critical gaps: no key distribution, no rotation mechanism beyond overwriting, no HSM integration, and the proxy's signing key lives as plaintext—meaning a compromise exposes the ability to backdate all audit records. Agent JWTs also lack revocation support.

EU AI Act Article 9:
The audit trail combining signed records, policy fingerprints, escalation logs, and session history provides a credible foundation for demonstrating the agent stayed within behavioral boundaries with human oversight—the policy fingerprint proves which version was active and escalation records prove approvals were sought and granted.

Security holes:
The unverified JWT payload peek to get the agent name for DB lookup is a documented pattern, not a vulnerability. But audit write failures are non-blocking, so an attacker who can block the audit database can create gaps in the log without detection.

The admin CLI lacks authentication entirely, and an operator with database access can insert fake escalation records to bypass rate limits. Escalation approval matching uses SHA-256 hashing of call arguments, which means previously-approved calls with identical arguments will pass the check again within a session—this is intentional but needs explicit documentation.

Score: 7/10

Finance agents are the strongest use case with all features demonstrated against the fintech policy pack, including high-value transfer escalations and sequential approval guards. Healthcare agents work well too with the PHI taint model and multi-step approval requirements functioning as intended.

DevOps and infrastructure agents have credible support through environment predicates and sequence guards on deployment workflows. Browser and computer-use agents are the weakest link since there's no way to capture browser-level events—all predicates operate on MCP tool calls and JSON arguments, so while taint can catch browser reading if it's implemented as an MCP tool, complex browser governance is out of scope.

For preventing incidents like the Replit case, I can define a policy that enforces a specific sequence—requiring a backup and safety check before allowing database deletion, with escalation triggered when certain conditions are met.

On exfiltration, I'm layering two independent guards: taint marking on credential reads and a sequence rule that blocks data transmission if credential reads appear anywhere in the session history.

The strongest objection from a technical founder would likely be that Anthropic already has...

Actually, the real issue is that standard permission systems only control what an agent can access in isolation. An agent with permission to read the database and write to an external API can exfiltrate data using only authorized tools—no permission model catches this. Only sequence-aware policies can prevent it.

The latency concern is also addressable: the benchmarks show sub-1ms overhead for typical sessions, with the sequence evaluator handling even 1000-event sessions in under a millisecond.

Now looking at how well this aligns with the pitch itself—the "two-line integration" claim is technically accurate once prerequisites are in place, but operationally it's a bit misleading. - two lines. But reaching that point requires 5+ steps. For an investor demo this is acceptable - you pre-configure. For a design partner's first solo evaluation, this friction causes drop-off.

"Policy lives outside the agent":
True. Policy YAML is independent. SIGHUP hot-reload means no agent restart needed. Model/prompt changes don't affect policy. Architecturally correct.

"Deterministic enforcement, not heuristic":
True. Pure functions. Exact comparisons. No ML. No probability. The only non-deterministic element is the escalation approval check (which queries the DB), but the DB query itself is deterministic. This claim is solid.

"MCP-native, framework-agnostic":
The proxy layer is truly framework-agnostic since it intercepts JSON-RPC 2.0 calls regardless of whether you're using LangGraph, CrewAI, or other frameworks. However, the SDK's 2-line integration only works with Anthropic, so the framework-agnostic claim is only partially true depending on which component you're using.

"Sub-5ms evaluation latency":
Native Go handles typical sessions under 200 events well within spec, and even at 1000 events stays around 1ms p99. The WASM build hits 4.5ms p50 on longer sessions, which exceeds the target, so the claim holds for native but degrades with WASM at scale.

Pitch-specific claims that don't fully match implementation:
Looking at the gap between what the pitch claims and what's actually implemented, particularly around the example rule about detecting secrets in external emails. ) shows AND logic combining two content conditions. The implementation has OR logic: each never_when entry is independent. An engineer who reads the pitch and writes never_when: [{field: recipient, operator: is_external}, {field: body, operator: contains_pattern, value: "/secret/"}] will get a denial if EITHER condition fires, not only when both are true. This is a discoverability bug.

"Delegation tracking: Agent A spawned Agent B which called Tool C on behalf of User D" - The delegation chain is implemented (Task 12.2) but only one level deep ("parent → child" format). Multi-level delegation chains are not tracked as A → B → C → D.

The DevOps pitch says "The policy engine emits OpenTelemetry traces for every decision" - This is now true (Task 10.1).

Now thinking through the investor questions I'm not ready to answer: I can't point to an actual customer running this in production since the distribution channels are broken, though I have design partners and can show the code. The proxy sits in the critical path with no failover or circuit breaker, so if it goes down agents just fail—it's intentionally fail-closed but that's a real availability concern. And I need to be clearer about how TrueBearing layers on top of existing IAM rather than replacing it, since we're enforcing behavioral sequences while IAM handles access permissions. The middleware extensibility is straightforward—adding a new policy just means defining the type, implementing the evaluation method, and wiring it into the pipeline, which I've already demonstrated three times. SQLite's single connection approach with SetMaxOpenConns(1) prevents locking errors and queues writes under load, which is fine for the design partner volume we're targeting; the failure mode is latency, not correctness. The test suite covers all the paths that matter—deny cases, boundaries, shadow mode, error injection—with actual benchmark numbers documented.

The integration tests need the -tags integration flag to run, which adds a bit of friction. The real technical debt is the three separate audit structs floating around: store.AuditRecord, audit.AuditRecord, and the auditLogLine struct in the replay logic. Any schema change now requires updating multiple places, and this has already caused bugs like the PascalCase JSON field issue that broke verify. I need to consolidate these into a single struct or find another way to eliminate the duplication.

There's also some over-engineering happening. The WASM build was ambitious but the performance doesn't hit the 5ms target on realistic sessions, so claiming a native sub-1ms binary would've been more honest. The OpenTelemetry dependency chain is heavy for what amounts to structured logging with trace fields. And there are three broken distribution paths still sitting there.

The real issue is priority—a working brew install would've been far more valuable for a demo than the WASM build. I'm settling on a 7.5/10 score.

I have everything I need. This will take a few minutes to write — it's a full evaluation, not a summary.

TrueBearing MVP: Technical Evaluation
1. Developer Experience — 6/10
The "two-line integration" claim
Marketing-accurate, operationally misleading. What the pitch shows:


from truebearing import PolicyProxy
client = PolicyProxy(anthropic.Anthropic(), policy='./policy.yaml')
What actually has to happen first on a clean machine:

Install the binary (go build — brew, pip, npm are all broken right now; see Gap 1)
Write a policy YAML
truebearing agent register <name> --policy ./policy.yaml
Generate a proxy keypair (needed for audit signing; not documented in the onboarding flow)
truebearing serve --upstream <url> --policy ./policy.yaml
Set TRUEBEARING_AGENT_JWT in the environment
That's 10-15 minutes of configuration before the two Python lines mean anything. For an investor demo where you pre-configure, this is fine. For a design partner's solo first evaluation following the README, this is where engineers stop. The README's quick-start section does cover most of these steps, but the pitch's framing creates an expectation gap that will cause drop-off.

DSL intuitiveness
Strong. only_after, never_after, escalate_when, taint are readable by any YAML-literate engineer without documentation. truebearing policy explain is the best DX feature in the product — it eliminates the "is my policy correct?" uncertainty by converting the YAML to plain English. truebearing init gives first-time operators a scaffolder that prevents blank-page paralysis. The linter (L001–L013) with colored output and exit codes makes CI integration trivial.

One confusing seam: the sequence predicates (tool-history-based) and the never_when content predicates (argument-value-based) look syntactically similar but operate on different information. A first-time writer who conflates them will write incorrect policies and get non-obvious failures.

Local dev
Genuinely excellent. No external services. Single SQLite file. Single binary. /health requires no JWT. This is real and should be emphasized — most competing infrastructure tools require a service account, a cloud endpoint, or a sidecar with its own auth. TrueBearing on a laptop works completely offline.

Error experience
When a tool call is denied, the JSON-RPC error message embeds the full reason (e.g., "sequence: only_after not satisfied: [verify_invoice, manager_approval]; never_after violation: read_external_email in session history"). The sequence evaluator collects all violations before returning — a developer sees everything wrong in a single request, not one at a time. This is a deliberate engineering choice (documented in CLAUDE.md §6) and it's the right one.

SDK completeness
The Python SDK's _configure_client function explicitly states: "TrueBearing targets Anthropic-compatible MCP stacks" — OpenAI clients are documented no-ops. The 2-line integration works only for Anthropic SDK ≥ 0.40 via with_options. LangGraph, CrewAI, and OpenAI Agents SDK work through the proxy (they make HTTP requests that the proxy intercepts), but there is no 2-line SDK integration for them. The developer has to manually configure base_url. The pitch's claim of "framework-agnostic, works with LangGraph, CrewAI, OpenAI Agents SDK" is true at the network layer but false for the SDK integration path.

policy lint usefulness
Non-ceremonial. L013 DFS cycle detection is technically impressive and prevents a real class of policy mistakes (deadlock by design). Severity levels are appropriate. This is production-quality and is the correct first command to show in a demo.

2. Operator / Platform Engineering — 6/10
Deployment story
Clear. Single static binary, SQLite, one command to start. The health endpoint at /health (no JWT required) is correct — it's what the SDK polls for readiness. The startup banner and structured JSON logging via log/slog (2>&1 | jq .) are operator-friendly.

Fail-closed posture
Correctly implemented throughout. Policy parse failure = serve refuses to start. DB unavailable = health returns 503. JWT missing or invalid = 401. Evaluation error = Deny. These aren't flags — they're compile-time invariants enforced in the pipeline. This is the right design for infrastructure in the critical path.

Policy hot-reload
SIGHUP-based reload (Task 12.3) was not in the original MVP plan and is a significant operational improvement. Old sessions continue using their fingerprint-bound policy version (stored in polByFingerprint). New sessions pick up the new policy. Lint errors and parse errors during reload are rejected — the proxy continues with the current policy. This is exactly how a production proxy should behave. One caveat: polByFingerprint grows unbounded for the process lifetime. At GitOps push cadence this is a minor issue; for a long-lived proxy in a team doing frequent policy iteration, it's a memory leak.

OTel integration
Real. --otel-endpoint flag. OTLP HTTP exporter. Standard truebearing.* span attributes on every decision. deny and shadow_deny spans are marked codes.Error so dashboards filter without attribute parsing. This plugs into Datadog, Grafana, and Jaeger without custom work. The demo script has a live Jaeger example with docker command. This claim is fully backed.

Multiple proxy instances
Not supported. SetMaxOpenConns(1) serializes all SQLite operations through a single connection. Two proxy instances against the same SQLite file will produce write contention that either serializes or deadlocks. For design partner volumes (single process, local deployment) this is fine. For any enterprise asking about high availability or horizontal scaling, the answer is: not in this version, upgrade to the hosted control plane.

The single thing a platform engineer will say to block adoption: "We can't run this in production without HA. A single process on a single machine is a single point of failure for every agent we operate." The correct answer is shadow mode + local deployment first, HA later. Have that answer ready.

Admin API authentication
session terminate, escalation approve/reject, agent revoke all require shell access to the machine. For a solo developer, this is fine. For a team where the security team approves escalations and developers run the agent — there's no mechanism to separate those roles without also giving security team SSH access to the proxy machine. The webhook sends the notification but approval still requires CLI. This is the escalation model's biggest operational gap.

3. Security / Compliance — 7/10
Tamper-evident audit trail
Strong. Every decision (allow, deny, shadow_deny, escalate) produces exactly one Ed25519-signed audit record with: session ID, seq, agent name, tool name, SHA256 of arguments, decision, decision reason, policy fingerprint, agent JWT SHA256, W3C trace correlation ID, delegation chain. audit export | audit verify works end-to-end (Task 8.1 wired it, Task 8.2 fixed the JSON field name issue). Records are append-only — no UPDATE or DELETE ever touches audit_log. Policy fingerprint in every record creates an unambiguous link between a decision and the policy version that governed it.

One implementation note: if audit.Write fails (DB write error), the proxy logs the failure and continues — it does not block the tool call. This is by design (don't break agents because of logging issues), but it creates a potential audit gap if the DB is under write pressure. An attacker who can induce consistent write failures gets an ungoverned window.

Auditor legibility
The JSONL format with snake_case fields is readable in any JSON tool without TrueBearing. However: the evidence object format from mvp-plan.md §11.2 (evidence_id, schema_version, session_state snapshot embedded per record) was not built. What exists is the raw audit_log rows. For a formal EU AI Act compliance submission, you'd need to build the evidence bundle format on top of the existing export. The bones are there; the presentation layer isn't.

Taint propagation depth
The implementation is a binary session-level flag — not causal data-flow tracking. When a taint-applying tool is called, session.Tainted flips to true. Until a clearing tool runs, tools with never_after referencing taint-applying tools are blocked. This is real and effective protection. But it's session-level contamination, not value-level: TrueBearing can't tell you "the value from read_external_email was passed to execute_wire_transfer." The whole session is marked tainted. The pitch's framing ("once untrusted content enters the execution path, any outbound action requires explicit policy clearance") is accurate for this implementation. The CaMeL paper's data-flow taint tracking it's based on is not.

Key management for enterprise
Ed25519 keys as local PEM files at 0600 is correct for a single machine. For an enterprise team: no key distribution, no key rotation (re-registration overwrites the old key without invalidating active JWTs issued from it), no HSM integration, the proxy's signing private key is a plaintext file whose compromise allows audit record backdating. None of this is surprising at pre-seed/design partner stage, but a security-conscious enterprise will ask about it in the first technical evaluation call.

EU AI Act Article 9 readiness
The combination of: signed audit records with policy fingerprint + escalation records showing human approval was required + session event log with monotonic sequence numbers + audit verify for tamper detection is a legitimate evidence base. A regulator asking "can you demonstrate the agent operated within defined behavioral boundaries and that humans could have intervened?" has affirmative answers on all counts. The response is defensible. The gap is that it's raw data, not packaged evidence bundles — an auditor needs some tooling to assemble the narrative.

4. End Customer Value — 7/10
Vertical coverage
Finance agents (strongest): The fintech policy pack demonstrates every major DSL feature. Sequential approval guards, taint from external email ingestion, escalation on high-value thresholds — all work and all demo well. This is the sharpest-edged use case.

Healthcare agents (solid): PHI taint model works. requires_prior_n for multi-step approval works. The HIPAA guard policy pack is production-ready with appropriate caveats (it says explicitly it's one technical control, not a full HIPAA programme). Gap: no content-based PII detection — you can't block on argument values containing SSNs or PHI strings, only on which tools were called.

DevOps/Infra agents (credible): require_env predicate (Task 9.2) prevents staging agents from calling production tools. Sequence guards on deploy workflows. Rate limiting for loop detection. The production-guard policy pack uses these. Weaker demo story because the failure mode is harder to make visceral — "my CI agent deployed to prod" is less immediately alarming than "my finance agent sent a $50k wire."

Browser/Computer-use agents (thin): All predicates operate on MCP tools/call names and their JSON arguments. If a browser agent's read/write operations are exposed as discrete MCP tools, TrueBearing can gate them. If the agent uses a general browser_action tool with free-form arguments, only the content predicates (argument-level never_when) can do anything — and those require knowing the argument field paths in advance. The taint model works if "visit untrusted URL" and "exfiltrate data" are separate tool calls. It does not work if they're both subcommands of one browser_execute tool.

The Replit incident
A policy that would have prevented it:


tools:
  drop_table:
    sequence:
      only_after: [create_backup, run_safety_check]
      requires_prior_n: {tool: create_backup, count: 1}
    escalate_when:
      argument_path: "$.scope"
      operator: "=="
      value: "production"
    enforcement_mode: block
The sequence evaluator would block drop_table if create_backup wasn't in the session history. The escalation evaluator would require human approval for production-scoped operations. Two independent guards. This is a real, demonstrable claim.

The MCP exfiltration pattern

tools:
  read_credentials:
    taint:
      applies: true
  transmit_data:
    sequence:
      never_after: [read_credentials]
The never_after blocks transmit_data if read_credentials appears anywhere in session history. The taint flag adds a second independent guard. Two evaluators, different mechanisms, same attack vector. Genuinely preventable with the current engine.

Primary technical founder objection
"We can just use OPA." Correct answer: OPA has no concept of session history. A policy in OPA evaluates a single request in isolation. You cannot express "this tool may not run if that other tool ran earlier in the same session." TrueBearing's sequence engine is not replicable with OPA — the policy model is wrong for the problem. This answer exists in the pitch and is correct.

5. Alignment to the Pitch — 6.5/10
Claims that are fully backed
"Deterministic enforcement, not heuristic": True. Pure functions. Exact comparisons. No ML.
"Policy lives outside the agent and survives prompt and model upgrades": True architecturally.
"The policy engine emits OpenTelemetry traces for every decision": True as of Task 10.1.
"MCP-native": True. JSON-RPC 2.0 tools/call interception works regardless of sender.
Claims that need nuance
"Two-line integration": True after prerequisites. Not true from zero. This will come up in the first design partner evaluation when they can't get it running in 5 minutes.

"Framework-agnostic, works with LangGraph, CrewAI, OpenAI Agents SDK": True at the proxy/network layer. False for the SDK integration path (Anthropic-only auto-configuration). A developer who tries PolicyProxy(openai.OpenAI(), policy='./policy.yaml') gets the client back unchanged with no error — a silent no-op.

"never_when: recipient_is_external AND body_contains: /secret|key|token/" (Pitch 01): The pitch shows AND logic combining two content conditions. The implementation has OR logic — each never_when entry is independent. Multiple never_when entries deny if ANY fires. An engineer who reads the pitch and attempts to combine conditions will write a policy that behaves differently than expected. This is a credibility risk if the demo audience has read the pitch closely.

"Sub-5ms evaluation latency": True for native Go binary on typical sessions. The WASM target on 1000-event sessions measures 4.5ms p50 with non-deterministic p99 due to GC pauses. The pitch specifically calls out "WASM-compiled policy engine for sub-5ms evaluation latency" — this is not reliably true for WASM on heavy sessions.

Seed investor questions you're not prepared to answer
1. "Can you show me a customer using this today?" Not yet — the three distribution paths (brew, pip, npm) are broken. You can demo from source but can't point to a design partner who installed and deployed it independently.

2. "What happens to my agents if your proxy goes down?" The proxy is in the critical path. No failover, no circuit breaker, no bypass mode. Agents get connection refused. This is correct for the fail-closed security model but is an availability question every enterprise evaluator will ask.

3. "How does this integrate with our Okta / PagerDuty / Slack for escalation approvals?" Webhook fires a notification. Approval requires CLI access to the machine. There's no OAuth, no web dashboard, no Slack action for approval. This is the operational escalation gap.

6. Engineering Quality — 7.5/10
Code quality
From the TODO notes (which document every design decision): the codebase follows its stated conventions consistently. No panics in production code. Errors wrapped with context. Pure evaluator functions (no writes inside Evaluate()). Taint mutations happen in the pipeline orchestrator after the decision. interface{} prohibited in the evaluation pipeline (enforced — ToolCall.ArgumentsMap from the plan was deliberately omitted). WAL mode PRAGMAs on every DB open. No raw SQL outside internal/store. Design: comments on non-obvious choices. This is disciplined.

Extensibility
The Evaluator interface makes adding new predicates clean. Adding Content, Env, and RateLimit evaluators post-plan required: (1) extend types, (2) implement Evaluate(), (3) wire into pipeline. No engine core changes. The QueryBackend abstraction (Task 13.1) proved the architecture is testable and portable — evaluators don't know whether they're running on SQLite or WASM memory.

Concurrency model
SetMaxOpenConns(1) serializes all SQLite writes. Under concurrent load, write operations queue at the connection. The failure mode is latency increase, not correctness failure. At design partner volumes this is invisible. The sequence evaluator's benchmark at ~939µs on 1000-event sessions (under 2ms target) assumes sequential access. Under concurrent load with connection queueing, the tail latency will be higher than the benchmark shows.

Test coverage
Non-ceremonial. Every evaluator has: table-driven tests covering all deny paths, boundary conditions, shadow mode conversion, error injection (evaluator returns error → pipeline returns Deny), and a benchmark. The fuzz test on MCP parser is correct (proxies must never panic on malformed input). Integration tests use real SQLite (not mocks). The 5 design-partner scenario integration tests under -tags integration are living documentation of guarantees — an excellent architectural choice.

Minor friction: -tags integration required for the integration tests means go test ./... misses them. A CI job that runs only go test ./... will pass while an integration test is broken.

Biggest technical debt
The triple audit struct: audit.AuditRecord (signing/verification), store.AuditRecord (query/export), and cmd/audit/replay.go's local auditLogLine. Any schema change requires updating all three. Task 8.2 added json tags to store.AuditRecord to close the worst inconsistency, but the structural problem remains. This has already caused one production bug (PascalCase JSON prevented audit verify from working on live proxy output). Fix: define the canonical type in pkg/audit that both internal/store and internal/audit import, eliminating the circular import that created this split.

What's over-engineered for this stage
The WASM build (Task 13.1) consumed significant effort to deliver a target that doesn't fully meet the pitch's "sub-5ms" claim at 1000-event sessions. The native Go binary is sub-1ms p99 for all realistic session sizes. The WASM build adds 31 Node.js tests, a QueryBackend abstraction layer, two entry points, a pre-built .wasm binary in the SDK, and a TypeScript shim class — all to support a pitch claim that the native proxy already satisfies. Meanwhile, three distribution tasks (13.2, 13.3, 13.4) that directly block customer acquisition were left incomplete.

Gap Analysis — Top 5
1. Distribution is broken (P0, immediate)
brew install, pip install truebearing, and npm install @mercator/truebearing all fail right now. Homebrew formula has a placeholder SHA256 (000...000). PyPI name not reserved. npm @mercator org scope not created. The README advertises three install paths, all of which error. A design partner engineer who tries to follow the README before your first call will form a negative first impression before they see any code. Fix: cut a release tag, compute real sha256, reserve PyPI and npm names. This is a day of manual work, not engineering.

2. The never_when AND/OR mismatch with the pitch (P0, credibility)
Pitch 01 shows never_when: recipient_is_external AND body_contains: /secret|key|token/ — clearly AND logic. The implementation has OR logic: each never_when entry is independent, and any single firing entry denies the call. An engineer reading the pitch and attempting to combine two content conditions will write a policy that either over-denies (if OR was intended) or under-denies (if AND was expected). Before any design partner evaluation, either update the pitch to match the implementation, or implement AND logic with explicit all: / any: syntax. The mismatch is discoverable in the first 15 minutes of hands-on use.

3. SDK is Anthropic-only with silent no-op for other clients (P1, sales scope)
The pitch says "framework-agnostic, works with LangGraph, CrewAI, OpenAI Agents SDK." A developer who passes an OpenAI client to PolicyProxy gets it back unchanged — no enforcement, no error. This is a silent failure that will cause a support ticket or, worse, a deployed integration they believe is enforcing policy that isn't. Fix: raise an explicit ValueError for unrecognized clients with instructions on the manual proxy_url configuration path. The failure should be loud.

4. Admin escalation approval requires CLI (P1, enterprise)
The escalation model's human approval step requires the operator to run truebearing escalation approve <id> from a terminal with shell access. For a solo developer this is fine. For an enterprise where a compliance officer approves escalations but doesn't have SSH access to the proxy machine — this is an operational gap that will come up in the first enterprise evaluation. The webhook notification sends the alert; the approval mechanism doesn't support a web or Slack action. Fix for near-term: document the API surface for escalation approve as an HTTP endpoint operators can expose internally. Fix for post-MVP: the hosted control plane with a web UI.

5. Audit evidence format is raw DB rows, not the structured evidence object from the plan (P2, compliance)
The mvp-plan.md §11.2 defines an Evidence JSON format with evidence_id, schema_version, session_state snapshot, and policy.source — designed to be self-contained for auditor review. What was built is raw audit_log rows exported as JSONL. For a compliance prospect showing this to their auditor, the raw format is readable but not polished. An auditor who wants to understand a specific decision needs to correlate the audit record, the session events table, and the policy file. The plan's evidence object would have embedded all of that. This becomes a conversation issue with healthcare and regulatory prospects, not a blocker, but it's a gap.

Strengths to Lead With
1. The simulation engine
truebearing simulate --trace ./real-session.jsonl --policy ./new-policy.yaml against actual captured traffic is the demo-closer. No competitor has an offline replay engine that shows "if this policy had been active last week, here are the 3 calls that would have been blocked, with exact reasons." This converts skeptics. It's the concrete proof mechanism that makes "trust but verify" into "verify, then trust." It's also free for the prospect to use on their own traffic before committing.

2. The linter + explain combination
L013 cycle detection (DFS on the only_after dependency graph) is a non-obvious, technically rigorous feature that catches a specific class of policy mistakes that would be completely invisible to an operator. Combined with policy explain (plain-English rendering), this is a visible, immediate demonstration that TrueBearing treats policy authoring as a first-class engineering problem. Running policy explain in the first two minutes of a demo makes the DSL accessible to anyone in the room, including non-engineers.

3. The audit trail is genuinely tamper-evident
Ed25519 signing, policy fingerprint in every record, canonical JSON including the trace correlation ID, append-only storage, offline verification via audit verify. For any compliance-sensitive prospect, this is the artifact that converts the pitch from "interesting tool" to "the thing we need to satisfy our auditor." No comparable tool provides this. The export | verify pipeline in two commands with output that an auditor can independently validate is a differentiator that deserves 90 seconds in every demo.

Pre-Sales Readiness Verdict
For design partner technical evaluations: Yes, with prep work and caveats.
The proxy works. The policy engine is correct. The audit trail is real. The demo script covers six narrative acts with verified commands. But: the demo must be run from a pre-built binary because all three distribution paths are broken. The presenter must know this and script around it — do not send a design partner a README link and expect them to install it independently before your call.

For a seed investor meeting: Yes.
The codebase is real, the architecture is sound, 13 phases of incremental development with complete commit history and documented design decisions is a strong signal of engineering discipline. The WASM build and OTel integration demonstrate production intent. The test coverage (table-driven evaluator tests, fuzz tests, integration tests, benchmarks) is unusually thorough for a pre-seed product. Show the code, show the test suite, show go test ./... -bench=. output. These are not typical of a demo-only MVP.

Minimum delta before the first unsupervised design partner eval:

Fix the Homebrew SHA256 (one day: cut a release tag, compute hashes, update formula)
Reserve PyPI truebearing and npm @mercator/truebearing names (one hour each)
Fix the PolicyProxy silent no-op for non-Anthropic clients (raise a clear error)
Write one paragraph in the README explaining the escalation approval CLI requirement and the webhook path
Everything else — WASM, OTel, delegation chains, rate limiting — is additive value. These four items are correctness issues that break the advertised paths.

Recommended Demo Script (10 minutes, technical founder)
Pre-configure before the room: binary built, proxy.pem generated, data-agent registered against policy-packs/fintech/payments-safety.policy.yaml, proxy started, 3-4 tool calls sent through so audit query returns results.

Act 1 — The problem (90 seconds)

Don't start with TrueBearing. Start with the failure mode:

"In July 2025, Replit's agent deleted their production database. It was authorized to be there. Every single action was individually permitted. There was no permission check that could have stopped it. The failure was a sequence — read credentials, then connect, then delete — that nobody had declared forbidden."

Show a 3-line block:


read_db_credentials  → allowed
open_external_conn   → allowed
DROP TABLE users     → allowed — CATASTROPHIC. UNGOVERNED.
"OPA, IAM, RBAC — all of them ask: can entity X access resource Y right now? That's the wrong question for agents. Agents fail in sequences."

Act 2 — The policy (2 minutes)

Open policy-packs/fintech/payments-safety.policy.yaml. Don't explain it. Run:


truebearing policy explain policy-packs/fintech/payments-safety.policy.yaml
Let the plain-English output read itself. Then:


truebearing policy lint policy-packs/fintech/payments-safety.policy.yaml
"The linter catches mistakes before deployment. It detects circular dependencies that would permanently deadlock your agent — a class of error that's otherwise invisible."

"This YAML lives in your git repo. It goes through the same PR process, the same review, the same CI gates as your infrastructure code. When your agent's model changes, the policy doesn't. When you upgrade your prompt, the policy doesn't change."

Act 3 — Shadow mode and enforcement (4 minutes)

With the proxy already running in shadow mode, send a sequence-violating call (missing the required prerequisite):


curl -s -X POST http://localhost:7773 \
  -H "Authorization: Bearer $TRUEBEARING_AGENT_JWT" \
  -H "X-TrueBearing-Session-ID: demo-001" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute_wire_transfer","arguments":{"amount_usd":50000}}}'
Call passes through (shadow mode). Show:


truebearing audit query --decision shadow_deny
"Shadow mode: the violation is logged, the call is allowed. You run this for a week. You see what the policy catches. You tune it. No disruption to your agent."

Now flip one line in the policy: enforcement_mode: shadow → enforcement_mode: block. Send SIGHUP (no restart):


kill -HUP $(pgrep truebearing)
Re-run the same curl. This time the response is:


{"jsonrpc":"2.0","error":{"code":-32000,"message":"sequence: only_after not satisfied: [verify_invoice, manager_approval]"}}
"Deterministic. No ML. No probability. The rule is the rule. The same call that was shadow-logged is now hard-blocked, with an exact reason."

Act 4 — The audit trail (90 seconds)


truebearing audit export | truebearing audit verify
Every record shows OK. Change one character in a record. Show TAMPERED.

"Every decision is signed with Ed25519. An auditor can verify this log offline — they don't need TrueBearing installed. The policy fingerprint in every record proves which version of the policy was active for every decision. For EU AI Act Article 9, this is the evidence bundle regulators ask for."

Act 5 — The simulation (1.5 minutes)


truebearing simulate \
  --trace testdata/traces/payment-sequence-violation.trace.jsonl \
  --policy policy-packs/fintech/payments-safety.policy.yaml
Show the diff table with execute_wire_transfer marked DENY and the exact reason.

"This is real captured traffic replayed against a policy offline. If you have traces from last week, you can see what would have been blocked without running anything in production. This is how you validate a policy before flipping to block mode."

Close

"You're probably already thinking about a specific failure mode you've had or are worried about. Tell me what it is. We'll write the policy for it right now."

Hand them truebearing init. Let them answer five questions. Show the generated policy. Show policy explain on what they just wrote. That's the conversion moment — they authored something real in under 3 minutes.

added some todos from phase 14 onwards - line 2279- 2679 of @TODO.md 

check if its good enough and covers all gaps - suggest additional if you so deem necessary
The user wants me to read the new Phase 14+ tasks they've added to TODO.md (lines 2279-2679) and evaluate whether they cover all the gaps I identified in my evaluation. Let me read that section.

Read TODO.md (lines 2280-2679)
Read TODO.md (lines 2680-2729)
Now let me assess what's covered and what's missing:

What my evaluation identified as gaps:

Distribution broken (P0) - Covered by Manual tasks M1-M4 and M5-M6. Well specified.

never_when AND/OR mismatch (P0, credibility) - Covered by Task 14.4. Well specified including backward compatibility and linter addition.

SDK silent no-op for non-Anthropic clients (P1) - Covered by Tasks 14.1 (Python raise) + 14.2 (OpenAI support) + 17.3 (Node.js). Well specified.

Admin escalation approval requires CLI (P1) - NOT covered. The escalation model's team approval gap is absent from all phases.

Audit evidence format is raw rows, not structured evidence object (P2) - Covered by Task 15.2 (compliance evidence report generator). Well specified.

Triple AuditRecord struct technical debt - Covered by Task 16.1.

polByFingerprint memory leak - Covered by Task 16.2.

Audit write failure creates ungoverned window - Covered by Task 16.3.

The --version flag is missing entirely despite being mentioned in M2, which would break the Homebrew verification step. On the positive side, Task 14.3's structured retry feedback with reason_code, suggestion, and unsatisfied_prerequisites directly addresses the LLM self-correction problem I flagged, and Task 15.1's Mermaid sequence diagram adds valuable documentation that I hadn't anticipated.

Task 15.3 covers the regulated verticals through insurance, legal, and life sciences policy packs, while Task 15.4's competitive table against Salus is useful but needs clarification on what's actually shipped versus planned—some cells like "self-repair feedback" are only true after Task 14.3 lands. Task 17.1 improves onboarding for vertical-specific users, and Task 17.2 adds a lint shadow mode check.

But there's a numbering conflict I'm catching: Task 17.2 wants to add L014 for checking if a policy's been deployed, which requires database access and is context-aware rather than static analysis. However, L014 and L015 are already claimed by Task 9.1 for content predicate validation (unknown operators and invalid regex). That's a spec bug that needs fixing.

I'm also noticing a couple of gaps: the --version flag is referenced in M2 but there's no actual task to implement it, and the escalation team approval workflow—which is operationally significant—seems incomplete. enterprise prospects is still not addressed. Who can approve an escalation? Right now it requires CLI access. There's no HTTP API for escalation approval, no role-based approval routing, no "anyone on this team can approve" model. For design partners who have compliance teams doing approvals, this is a blocker.

The competitive table in Task 15.4 has aspirational cells—"Self-repair feedback" is marked as available for Mercator but only ships after Task 14.3 completes, so it needs a note or should reflect the current state instead.

There's also a lint rule numbering conflict: L014 and L015 are already assigned from Task 9.1, but Task 17.2 proposes a new L014 for shadow mode checking, which will collide. I need to use L019 or higher.

The truebearing --version flag is required for M2 verification but has no implementation task yet. The competitive table also references Salus by name, which violates the naming guidelines in CLAUDE.md §11 for code and documentation, though a README narrative section might be an exception worth checking.

There's a known correctness issue in the simulation engine: simulate/replay events get stamped with current timestamps by AppendEvent, which can cause rate-limit over-counting in offline contexts. This was flagged in the 9.3 notes but remains unaddressed. The docs URL in the SDK error messages will return a 404 until the redirect is in place, so Task 14.5 needs to call out that M6 must be completed first before those error message links become live.

Good. Let me assess coverage systematically against the gaps I identified, then flag what's still missing.

Coverage Assessment
What's well-covered
Gap 1 — Distribution broken: Fully addressed. M1–M4 are the right operations in the right dependency order. M5 (end-to-end timing on a clean machine) is exactly the right quality gate. M6 (docs redirect) is often forgotten and you got it.

Gap 2 — never_when AND/OR mismatch: Task 14.4 is correctly specified. Backward-compatible default of any, explicit match field, linter warning for ambiguous multi-predicate blocks, policy explain rendering. This is complete.

Gap 3 — SDK silent no-op: Tasks 14.1, 14.2, 17.3 together cover it — raise loudly, then add real OpenAI support, do the same in Node. Good sequencing.

Gap 5 — Raw audit rows instead of structured evidence: Task 15.2 is the right answer and better-specified than the plan's §11.2 evidence object. The Markdown report with five sections + signature verification summary is actually more useful to an auditor than the JSON evidence format the plan described.

Technical debt: Tasks 16.1 (triple struct), 16.2 (polByFingerprint LRU), 16.3 (audit write failure gap) are all correct and appropriately scoped.

New additions not in my evaluation that are good:

Task 14.3 (structured retry feedback): This is the right move. A human-readable deny reason is not enough for an LLM agent to self-correct. The reason_code + unsatisfied_prerequisites + suggestion structure is exactly what an agent framework needs to route the denial into the model's context. The 58% self-correction stat is compelling framing.
Task 15.1 (Mermaid sequence diagram): I didn't flag this but it's high-value. An auditor or compliance officer who can paste a diagram into Notion closes more deals than one who has to parse JSONL.
Task 17.1 (vertical-aware init): Correct DX improvement.
What's Still Missing
Critical gap not addressed: Escalation approval has no team model.
Every phase in 14–17 assumes a single operator approving escalations via CLI. The pitch promises human oversight. For a fintech or healthcare design partner, the person who approves a $50k wire escalation is not the same person running truebearing serve. There's no HTTP endpoint for escalation approval, no role separation, no "anyone on this team" model. This will be the first question from any multi-person team evaluating the product. Suggest:


### Task 15.5 — Escalation: HTTP approval endpoint
**File:** `internal/proxy/proxy.go` (or new `internal/proxy/admin.go`)
**Why:** The current escalation approval model requires CLI access to the machine
running the proxy. For any team where the approver (compliance officer, CFO) is
not the same person as the operator, this is a blocker.

**What to build:**
- Add a simple admin HTTP API (localhost-only, no auth for MVP):
  `POST /admin/escalations/{id}/approve` with JSON body `{"note": "..."}`
  `POST /admin/escalations/{id}/reject` with JSON body `{"reason": "..."}`
  `GET /admin/escalations?status=pending`
- These endpoints mirror the CLI commands exactly — they call the same
  internal functions.
- Bind to a separate port (default: 7774) or require `--admin-port` flag
  so it's clearly distinct from the proxy port.
- Document in the webhook notification payload: include the admin endpoint
  URL so the webhook recipient can approve with one curl command.
- Add tests: approve via HTTP transitions status; reject via HTTP transitions status.
This is ~3 hours of code and unlocks the team-approval story immediately, even without a web UI.

Lint rule number collision.
Task 17.2 proposes a new L014 for the shadow mode deployment check. L014 and L015 are already taken by Task 9.1 (content predicates: unknown operator, invalid regex). The collision isn't visible in the task description but will produce a bug when implemented. Rename to L019 or higher, or establish a numbering convention in the task description.

truebearing --version flag is missing but M2 depends on it.
M2 says "verify truebearing --version returns the correct version string" — but there is no coding task to implement this flag. Cobra's root command supports cmd.Version but it's not configured. This is a 15-minute task that blocks a manual distribution step. Add:


### Task 14.6 — Add `--version` flag to root command
**File:** `cmd/main.go`
**What to build:**
- Set `rootCmd.Version = "0.1.0"` (or inject via ldflags: `-ldflags="-X main.version=0.1.0"`).
- The release workflow already uses `-ldflags="-s -w"` — extend to include
  `-X github.com/mercator-hq/truebearing/cmd.version={{.Version}}`.
- `truebearing --version` should print `truebearing version 0.1.0`.
- M2 depends on this.
Task 15.4 competitive table has aspirational cells.
The table lists "self-repair feedback to agent: ✓" for Mercator. That's only true after Task 14.3 ships. If someone reads the README before 14.3 is merged and tries to find the data.reason_code field in a deny response, it won't be there. Either: (a) make Task 14.3 a prerequisite for Task 15.4, or (b) annotate the cell as "(after v0.1.1)" until 14.3 ships. This is a credibility risk, not a philosophical one.

Rate-limit accuracy in simulate/replay.
The Task 9.3 implementation notes flag this explicitly: "simulate/replay events in the in-memory store are stamped with time.Now() by AppendEvent, which may cause rate-limit over-counting." This makes truebearing simulate give incorrect results for policies with rate_limit predicates. No task addresses it. If the simulate demo is the trust-builder (it is), and simulate gives wrong answers for rate-limited policies, that's a demo risk. Suggest adding a task under Phase 16:


### Task 16.4 — Fix rate-limit timestamp accuracy in simulate/replay
**File:** `cmd/simulate.go`, `cmd/audit/replay.go`
**Why:** `RateLimitEvaluator.Evaluate()` uses `call.RequestedAt` but `AppendEvent`
overwrites `RecordedAt` with `time.Now()`. In simulate/replay, all events are
effectively timestamped "now", making rate-limit decisions incorrect for historical traces.
**What to build:**
- Pass `event.RecordedAt = call.RequestedAt.UnixNano()` explicitly when appending
  events in the simulate/replay in-memory store, bypassing AppendEvent's auto-timestamp.
- Add a test: a trace with 6 calls to `search_web` over 10 minutes should not
  trigger a rate_limit of 5/minute when simulated.
Task 14.2: OpenAI base_url equivalence assumption.
The spec says return openai.OpenAI(base_url=proxy_url, api_key=client.api_key). This assumes the OpenAI Python client routes MCP tool calls through base_url in a way that's compatible with TrueBearing's MCP interception. The OpenAI SDK's base_url overrides the API endpoint — if the design partner's agent uses OpenAI's MCP client (which may not exist yet in a standard form), the integration may not work. Flag this in the task notes as requiring a validation step: run an end-to-end test with a real OpenAI client against the proxy before marking the task complete.

Priority Order Recommendation
The tasks as written are correctly triaged. The only reordering I'd suggest:

Task	Current Phase	Recommend
Add --version flag	Missing	Add as 14.6, prerequisite for M2
Escalation HTTP API	Missing	Add as 15.5, before 15.4 competitive table
Fix rate-limit in simulate	Missing	Add as 16.4
Lint rule L014 renaming	Bug in 17.2	Fix rule number before implementation
Task 15.4 aspirational cells	Fix note in 15.4	Mark 14.3 as prerequisite
The phases themselves (14=P0, 15=P1, 16=P2, 17=P3) and the Manual block are the right structure. The five additions above close the remaining live gaps.