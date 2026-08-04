# Testing Terminology

> **Status:** Current · *audited 2026-08-03*

`AGENTS.md` § Shared Agent Runbooks makes a **terminology handshake** mandatory before running
anything slow or irreversible. This page is the dictionary that handshake
refers to. Everything here is derived from the Makefile, the Playwright config,
the compose stacks and the harness READMEs; each claim cites `path:line`.

Companion pages: [docs/testing/README.md](testing/README.md) is the index of
the testing docs; [docs/loom-glossary.md](loom-glossary.md) is the domain
dictionary (workspace, role, agent, lead, fleet/fleet-db, flue, aether).

## The handshake

Before you run a slow or irreversible test, restate the request as five
coordinates and one evidence class:

```
(depth, realness, provisioning, polarity, target) → evidence class
```

Example: *"`make test-e2e-real-smoke` = (e2e, real Loom/fleet-db stack with **no
AI backend**, self-contained, happy-path, browser + API contracts) → evidence
class `real`."* The target sets `RUN_INTEGRATION_TESTS=1` and runs the
`integration-smoke` + `api-smoke` Playwright projects (`Makefile:417-419`)
against a FleetDB-backed `loom serve` (`scripts/start-e2e-server.sh:173`,
`:166`) — no Codex/Claude CLI is launched, so this is *not* the "real backend"
(real agent CLI) rung of Axis 2.

Then, if the request contained a trap word (`local`, `live`, `real`, `verify`,
`gate`), say which sense you took it in instead of guessing.

**Evidence classes** (`AGENTS.md` § Shared Agent Runbooks, "Terminology handshake"):

| Class | Means | Example |
|---|---|---|
| `deterministic` | Orchestration only. No real AI backend, no external service. Proves Loom's plumbing, not model behavior. | `make local-mode-up` (`Makefile:168`), whose backend is the `loom-backend-localdogfood` script |
| `real` | A real local Loom stack — real Loom services, real FleetDB, real daemon supervision. The real *AI backend* (a live Codex/Claude CLI) is a separate, optional ingredient: `test-e2e-real-smoke` runs browser + API contracts against a FleetDB-backed `loom serve` with **no** AI backend (`scripts/start-e2e-server.sh:173`); `local-mode-codex-up` is the one that adds a real Codex CLI (`test/local-mode/docker-compose.codex.yml:14`). | `make test-e2e-real-smoke` (`Makefile:417`) — Loom/fleet-db stack, no AI backend; `make local-mode-codex-up` (`Makefile:174`) — real Codex CLI |
| `live` | Reaches a real external/paid service. Costs money, may mutate external state. | `make test-e2e-github-webhook-live` (`Makefile:403`) opens and closes a real PR |

**Fail closed.** If a `real` or `live` path is blocked (missing auth, missing
binary, no podman), report *blocked* / *unverified*. Never fabricate state, and
never present a `deterministic` run as evidence for a `real` claim
(`.agent-skills/loom-pr-test/SKILL.md:66`).

## Axis 1 — depth

How much of the system the test spans. This is the Makefile ladder.

| Depth | What runs | Entry point |
|---|---|---|
| unit | One package / one component, no network | `make test` → `scripts/test.sh` (`Makefile:45`); `make test-frontend` → vitest (`Makefile:374`) |
| integration | Multiple packages, still in-process; Go `integration` build tag | `make test-integration` (`Makefile:50`, sets `TEST_TAGS=integration`) |
| e2e | Real processes/browser. Go `e2e` build tag, or Playwright | `make test-all` (`Makefile:55`); `make test-e2e` for route-mocked chromium (`Makefile:381`) |
| full-stack | A whole containerised Loom deployment | `make local-mode-up` (`Makefile:168`), `make test-distributed-smoke` (`Makefile:237`) |

Twelve Go files carry `//go:build e2e`, including
`internal/cli/automode_e2e_test.go`,
`internal/webui/handlers/webhooks/webhooks_e2e_test.go` and
`internal/stackpublish/stackpublish_e2e_test.go`.

There is no `bench` build tag in this repo. Two benchmarks exist, both
untagged: `internal/types/id_generator_test.go:197` and `:206`.

## Axis 2 — realness

What is standing in for the model and for the backend. Four rungs, all
implemented:

| Realness | Mechanism | Citation |
|---|---|---|
| mocked | Playwright intercepts every route; no server at all | `--project=chromium` (`internal/webui/frontend/playwright.config.ts:128`), mocks in `internal/webui/frontend/tests/helpers/api-mock.ts` |
| deterministic | Real Loom stack, scripted model substitute | `LOOM_BACKEND: localdogfood` (`test/local-mode/docker-compose.yml:80`), script at `test/local-mode/loom-backend-localdogfood` |
| real backend | Real agent CLI process under Loom supervision | `LOOM_BACKEND: codex` (`test/local-mode/docker-compose.codex.yml:14`); needs credentials at `${LOCAL_MODE_CODEX_HOME:-$HOME/.codex}` (`:25`) |
| live | Reaches an external/paid service, gated behind explicit env | `LOOM_E2E_GITHUB_REPO` (`Makefile:403-406`), `LOOM_STACK_E2E` + `LOOM_STACK_E2E_REPO` (`Makefile:411-414`), `DAYTONA_API_KEY` (`Makefile:186-194`) |

The deterministic rung is the one that gets misreported. A `localdogfood` run
is a **real Loom stack** and a **fake AI backend**; it is stack validation
only.

## Axis 3 — provisioning

Who supplies the server the test talks to.

| Provisioning | Who starts the server | Entry points |
|---|---|---|
| self-contained | Playwright, via `scripts/start-e2e-server.sh` on `E2E_PORT` 8090 (API) and `E2E_FRONTEND_PORT` 3100 (Vite preview) | `internal/webui/frontend/playwright.config.ts:70-95`, `:26-28`; `make test-e2e-api` (`Makefile:387`), `test-e2e-real-smoke` (`:417`), `test-e2e-integration` (`:437`) |
| local-server | You already have `loom serve` running; `LOOM_LOCAL_SERVER=1` makes Playwright health-check it instead of starting one | `internal/webui/frontend/tests/e2e/global-setup.ts:51-66`; the `*-local` targets `Makefile:392`, `:422`, `:432`, `:442` |
| containerised | A compose stack owns every process | `test/local-mode/`, `test/fleetdb/`, `test/distributed/`, `e2e/Dockerfile` |

A fourth, legacy path exists: `PODMAN_COMPOSE` set makes global-setup drive a
compose stack (`internal/webui/frontend/tests/e2e/global-setup.ts:83-101`). It is off by default —
without it, global-setup logs `webServer mode — Playwright manages server
lifecycle` and returns (`:68-81`).

## Axis 4 — polarity

Whether the test asserts the happy path or a failure mode. `AGENTS.md`
names this axis in its `docs/testing-terminology.md` bullet; the code supports exactly this two-value reading and nothing
finer, so do not invent more:

- **happy-path** — the system does the thing. `loom-backend-playground` is the
  happy-path mock backend (`test/playground/README.md:10-12`).
- **failure-mode** — the system refuses, kills, sweeps or times out correctly.
  `test/playground/` is explicitly a "daemon-lifecycle **failure-mode**
  harness … mock backends that crash, hang, or run slow on demand"
  (`test/playground/README.md:3-8`). Other failure-mode assertions: the
  `sandbox_required` denial gate (`AGENTS.md` § Workflow Sandbox,
  `scripts/test-step9-sandbox.sh`) and the `--network=none` blocking legs of
  the egress tests (`AGENTS.md` § Egress modes (SB4)).

A failure-mode test that "passes" because nothing ran is not a pass. See
`docs/testing/e2e-preflight.md` §Test-runner conventions.

## Trap words

Five words in this repo mean different things in different sentences. Say which
one you mean.

### `local`

| Sense | Meaning | Citation |
|---|---|---|
| local mode | One machine, containerised, but still FleetDB-backed as the control plane | `docs/testing/local-mode-podman-e2e.md:13-16` |
| local server | `LOOM_LOCAL_SERVER=1` — Playwright uses a `loom serve` you started, instead of starting one | `Makefile:392`, `internal/webui/frontend/playwright.config.ts:9` |
| embedded local | No `LOOM_FLEET_DB_URL`; loom spawns fleet-db + miniredis itself | `bootstrap.StartEmbedded`, `internal/bootstrap/embedded.go:310` |
| localredis | The in-process miniredis manager and its JSON snapshot | `internal/webui/localredis/manager.go` |

### `live`

| Sense | Meaning | Citation |
|---|---|---|
| live (evidence class) | Reaches a real external/paid service | `AGENTS.md` § Shared Agent Runbooks |
| live updates | SSE pushed a change without a page refresh | `docs/testing/dogfood-playwright-coverage.md` Real-Stack Suites |

### `real`

| Sense | Meaning | Citation |
|---|---|---|
| real backend process | Real Loom serve + real FleetDB behind the browser/API specs | `make test-e2e-real-smoke`, `Makefile:417` |
| real AI backend | A real Codex/Claude CLI actually generating output | `make local-mode-codex-up`, `Makefile:174` |

`test-e2e-real-smoke` is "real" in the *first* sense only. A deterministic
`localdogfood` stack is "real" in neither
(`.agent-skills/loom-pr-test/SKILL.md:66`).

### `verify`

| Sense | Meaning | Citation |
|---|---|---|
| the verifier scripts | `make local-mode-verify` → `test/local-mode/verify-local-mode.sh`; also `local-mode-routing-verify` and `local-mode-webhook-verify` | `Makefile:206`, `:214`, `:221` |
| generic assertion | Any check in any test | — |
| do-not-self-verify | The rule that the CLI must not be used to confirm its own writes — cross-check against fleet-db over HTTP | `docs/testing/e2e-preflight.md` §Test-runner conventions |

### `gate`

| Sense | Meaning | Citation |
|---|---|---|
| the quality gate | `make gate`, a plain alias for `make check`: `check-go` (15 steps) and `check-frontend` (6 steps) in parallel | `Makefile:578`, `:554`, `:502`, `:538` |
| the e2e gates | `make gate-e2e` = gate + real smoke; `make gate-e2e-full` = gate-e2e + container tests | `Makefile:581`, `:587` |
| acceptance gate | A named FleetDB acceptance gate (G0–G8) | [docs/testing/fleetdb-acceptance-gates.md](testing/fleetdb-acceptance-gates.md) |
| step-9 sandbox gate | The untrusted-workflow denial gate | `AGENTS.md` § Workflow Sandbox, `scripts/test-step9-sandbox.sh` |

## Harness directory map

| Path | What it is |
|---|---|
| `test/local-mode/` | Full-stack dogfood compose (deterministic, Codex, Claude, Daytona variants) plus `verify-local-mode.sh`, `verify-agent-routing.py`, `verify-webhook.sh` |
| `test/playground/` | Daemon-lifecycle failure-mode harness: mock backends that crash, hang or run slow, plus scenario scripts (`test/playground/README.md`) |
| `test/fleetdb/` | FleetDB-only stacks — the empty new-user UI stack (`test/fleetdb/README.md`) and the UI regression stack driven by `make test-fleetdb-ui` (`Makefile:113`) |
| `test/distributed/` | Shared Redis + fleet-db, two `loom serve` processes, two supervisor heartbeat loops, one smoke runner (`test/distributed/README.md`) |
| `test/support/` | Shared harness helpers (`first-run-onboarding.mjs`) |
| `e2e/` | Alpine container with `loom`, Chromium, Playwright, agent-browser and stub `claude`/`codex`/`opencode` CLIs (`e2e/README.md:1-40`) |
| `smoke-test/` | One script: `smoke-test-slack-epic-runner-stack.sh` |
| `scripts/` | `test.sh` (the Go runner), the `check-*.sh` gate scripts, `test-step9-sandbox.sh`, `start-e2e-server.sh` |
| `.agent-skills/loom-pr-test/SKILL.md` | The runbook for real Loom PR runtime testing |

## Related

- [docs/testing/README.md](testing/README.md) — index of the testing docs
- [docs/testing/test-infrastructure.md](testing/test-infrastructure.md) — CI, scripts, Makefile targets, coverage thresholds
- [docs/testing/fleetdb-acceptance-gates.md](testing/fleetdb-acceptance-gates.md) — the named acceptance gates
- [docs/loom-glossary.md](loom-glossary.md) — domain vocabulary
- `AGENTS.md` — the handshake requirement itself
