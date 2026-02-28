# Policy Pack: DevOps — Production Guard

This policy pack enforces sequence guards and taint isolation for CI/CD deployment workflows.
It is designed for agents that have write access to production infrastructure: cloud providers,
container orchestrators, PaaS deployment APIs, or infrastructure-as-code runners.

---

## Policies in this pack

### `production-guard.policy.yaml`

**Pattern:** Sequential deploy chain with force-push isolation and human escalation on production promote.

**What it enforces:**

| Rule | Type | Effect |
|---|---|---|
| `deploy_staging` requires `run_tests` + `build_artifact` | Sequence | No untested or unbuilt code reaches staging |
| `run_integration_tests` requires `deploy_staging` | Sequence | Integration tests run against the staged artifact |
| `deploy_production` requires full chain + `sign_off_deploy` | Sequence | Production deploy cannot skip any pipeline stage |
| `deploy_production` blocked if `force_push_override` was called | Taint guard | Force-pushed code cannot reach production without sign-off |
| `sign_off_deploy` clears force-push taint | Compliance gate | Explicit approval certifies code review even after force push |
| Every `deploy_production` escalates to human | Escalation | Final circuit breaker regardless of sequence state |
| `rollback_production` requires `open_incident_ticket` first | Sequence | Rollbacks are always traceable to an incident |
| `deploy_production` at tool-level `block` | Enforcement override | Hard enforcement on production, even in global shadow mode |

**Risk mitigated:** Production deploy before tests pass, deploy of unreviewed code, untracked rollbacks, accidental production promote by an agent in a bad state.

---

## The deploy sequence model

This policy enforces a strict left-to-right pipeline:

```
run_tests → build_artifact → deploy_staging → run_integration_tests → sign_off_deploy → deploy_production
```

Any attempt to skip a stage is denied. An agent cannot deploy to production without first
proving each prior stage completed in the same session.

---

## When to use each rule

### `only_after` — Sequential pipeline stages

Use when each stage of a pipeline must produce a verified artefact before the next stage runs.
Typical deploy pipelines are inherently sequential — production should never be ahead of staging.

```yaml
deploy_production:
  sequence:
    only_after:
      - run_tests
      - build_artifact
      - deploy_staging
      - run_integration_tests
      - sign_off_deploy
```

### Force-push taint (`taint.applies` + `never_after`)

Use when a tool can bypass a safety gate (force push bypasses branch protection, an admin
override bypasses a change freeze, a manual unlock bypasses an automated check). Taint the
session immediately when the bypass is used, and require explicit sign-off before high-risk
actions are permitted again.

```yaml
force_push_override:
  taint:
    applies: true
    label: force_push_detected

deploy_production:
  sequence:
    never_after:
      - force_push_override
```

### Sign-off gate (`taint.clears`)

Use when an explicit approval from a qualified reviewer can certify that the session state
is safe to proceed, even if an earlier bypass was used. The gate must be a real tool in your
system that records the approver's identity — TrueBearing verifies it was called, not who called it.

```yaml
sign_off_deploy:
  taint:
    clears: true
```

### Production escalation (`escalate_when`)

Use when every production action should have a human in the loop, regardless of pipeline state.
The `==` operator with a string value is the correct pattern for environment matching:

```yaml
deploy_production:
  escalate_when:
    argument_path: "$.target_environment"
    operator: "=="
    value: "production"
```

### Rollback protection (`only_after` on rollback tool)

Use to ensure that every rollback is associated with an incident ticket. Untracked rollbacks
make post-mortems impossible and disguise systemic reliability problems.

```yaml
rollback_production:
  sequence:
    only_after:
      - open_incident_ticket
```

---

## Environment isolation (post-MVP)

A future TrueBearing DSL version will add the `require_env:` predicate, which blocks a tool
from running unless the proxy was started with a matching `--env` flag:

```yaml
# Post-MVP syntax — not yet available
deploy_production:
  require_env: production
```

Until then, enforce environment isolation at the infrastructure level:

- Run **separate proxy instances** for staging and production environments.
- Use **separate policy files** per environment (e.g., `staging-guard.policy.yaml`, `production-guard.policy.yaml`).
- Issue **separate agent JWTs** per environment. A JWT issued against a staging policy cannot be used with a production proxy.
- Use your infrastructure's network controls to ensure the staging agent cannot reach the production MCP endpoint.

---

## Adoption guide

1. **Copy** `production-guard.policy.yaml` into your project directory.
2. **Rename tools** to match your actual tool names (e.g., `gh_actions_trigger`, `kubectl_apply`, `argo_sync`).
3. **Adjust the pipeline** — add or remove stages in `only_after` lists to match your actual CI/CD flow.
4. **Lint:** `truebearing policy lint ./production-guard.policy.yaml`
5. **Explain:** `truebearing policy explain ./production-guard.policy.yaml`
6. **Register:** `truebearing agent register deploy-agent --policy ./production-guard.policy.yaml`
7. **Serve:** `truebearing serve --upstream <your_mcp_url> --policy ./production-guard.policy.yaml`
8. **Shadow mode first:** run in shadow mode against your staging pipeline to verify the sequence is correct.
9. **Block:** flip `enforcement_mode: shadow` → `enforcement_mode: block` for production.

---

## Customisation examples

**Simpler pipeline (no staging environment):**
```yaml
deploy_production:
  enforcement_mode: block
  sequence:
    only_after:
      - run_tests
      - build_artifact
      - sign_off_deploy
```

**Require two independent sign-offs:**
```yaml
deploy_production:
  sequence:
    only_after:
      - run_tests
      - build_artifact
      - deploy_staging
      - run_integration_tests
      - first_approver_sign_off
      - second_approver_sign_off  # dual-control approval
```

**Database migration guard:**
```yaml
may_use:
  - run_migration
  - verify_migration
  - rollback_migration
  - check_escalation_status

tools:
  run_migration:
    enforcement_mode: block
    sequence:
      only_after:
        - verify_migration  # dry-run must have passed
    escalate_when:
      argument_path: "$.is_destructive"
      operator: "=="
      value: true
```
