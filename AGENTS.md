# Agent Instructions

This project uses fleet-db-backed `loom data` commands for issue tracking.

**Read `docs/loom-glossary.md` first.** This codebase reuses ordinary words and collides
with general knowledge — "loom" is an AI-agent orchestration platform (not the video app
or Java Project Loom), and `flue`, `fleet`, `aether`, `codex`, `daytona`, `atlas`, and even
`claude` all mean something specific here. The glossary is the shared dictionary + concept
map (request lifecycle, object model, the four planes); consult it before reasoning about
any of these terms, and confirm the loom meaning before acting on an overloaded one.

## Shared Agent Runbooks

Agent-specific skill loaders are optional. All agent CLIs can use the repo
runbooks directly:

- `.agent-skills/loom-pr-test/SKILL.md` - real Loom PR runtime testing with
  local-mode stacks, browser validation, FleetDB compatibility checks, and
  real Codex local-mode checks.
- `docs/testing-terminology.md` - the canonical map of this repo's testing
  vocabulary along four axes (depth / realness / provisioning / polarity) plus
  the matrix shape, the trap words (`local`, `live`, `real`, `verify`, `gate`),
  and the terminology-handshake protocol.

When testing Loom runtime behavior, follow the runbook above. Do not manually
create lock files, FleetDB state, sessions, transcripts, diffs, or other fake
state as test evidence.

**Terminology handshake.** Before running anything slow or irreversible, echo the
request back as coordinates *(depth, realness, provisioning, polarity, target)*,
disambiguate any trap word instead of guessing, and state the evidence class of
what you ran (deterministic = orchestration only; real = real local backend; live =
reaches a real external/paid service, costs money / may mutate external state). If a
real/live path is blocked, report blocked/unverified — never fabricate state. See
`docs/testing-terminology.md`.

## Generated Workflow Bundles

Do not commit `internal/infra/workflowdistribution/builtin-dist/` or other generated Flue bundle
output. Build bundles locally when needed for verification, but keep generated
artifacts out of git and change the workflow source under
`internal/infra/workflowdistribution/builtin/` instead.

## Local Gate Environment

When running `make gate` from a Loom desktop-launched shell, clear inherited
desktop/runtime env before trusting local failures. Variables such as
`LOOM_WORKSPACE`, `LOOM_WORKSPACE_RUNTIME_DIR`, `LOOM_CONFIG_DIR`,
`LOOM_DESKTOP_DATA_DIR`, `LOOM_FRONTEND_DIR`, `LOOM_WEBUI_URL`,
`LOOM_LOCAL_RUNTIME`, `LOOM_NOTIFY_TOKEN`, and agent/session vars can make tests
resolve the real desktop workspace, frontend bundle, or notify token instead of
their isolated fixtures. Re-run suspect failures with a clean env, for example:

```sh
tmphome=$(mktemp -d)
env -u LOOM_WORKSPACE -u LOOM_WORKSPACE_RUNTIME_DIR \
  -u LOOM_AGENT_NAME -u LOOM_AGENT_ROLE -u LOOM_AGENT_TERMINAL_ID \
  -u LOOM_SESSION_ID -u LOOM_NOTIFY_TOKEN -u LOOM_CONFIG_DIR \
  -u LOOM_DESKTOP_DATA_DIR -u LOOM_FRONTEND_DIR -u LOOM_WEBUI_URL \
  -u LOOM_LOCAL_RUNTIME HOME="$tmphome" make gate
```

## Driver Runtime Auth (loom-dev deploy notes)

Workflow runtimes authenticate to the driver-op HTTP API with a run-scoped
bearer token (`LOOM_RUN_TOKEN`, minted at claim). At deploy:

- Set `LOOM_RUN_TOKEN_SIGNING_KEY` (hex, 32 bytes) in the systemd unit so
  tokens survive serve restarts. When unset, serve generates an ephemeral
  per-process key (single-instance only; in-flight runs die with the process).
- Workflow bundles and hidden Driver CLI operations require `LOOM_RUN_TOKEN`.
  The node-wide `LOOM_DRIVER_API_TOKEN` bearer, identity header quad, and
  `LOOM_DRIVER_LEGACY_AUTH_ENV` switch no longer exist. Missing, malformed, or
  unmintable run-token configuration fails closed before workflow launch.
- Task runners (sdk/runner.js) authenticate to serve's task-run API
  (`/api/workspaces/{ws}/task-run/{op}`) with their per-task-run lease token;
  serve exports `LOOM_TASK_RUN_API_URL` to bridge-spawned runners
  automatically. Stop provisioning `LOOM_FLEET_DB_URL`/`LOOM_FLEET_DB_API_KEY`
  to task runner wrappers — the SDK fails closed when the serve URL is absent
  and never sends TaskRun mutations directly to fleet-db.

## Workflow Sandbox (loom-dev deploy notes)

`LOOM_DRIVER_SANDBOX=container` runs workflow bundles in rootless containers
(podman first, docker fallback; podman install is part of the loom-dev deploy
requirements). Default stays `process`, but the SB3 trust placement policy is
ENFORCED regardless of mode: every driver carries a `trust_level`
(`trusted`/`untrusted`; missing = untrusted), the executor refuses to launch
an untrusted bundle outside an isolating launcher (the run fails with
`errorClass=sandbox_required`, nothing is spawned), and there is no silent
fallback. Operator/CLI registration and the builtin epic-runner stamp trusted;
workflow submissions via the HTTP API stamp untrusted (no self-elevation —
elevation is an explicit driver update, e.g. `PATCH .../drivers/{id}` with
`{"trust_level":"trusted"}`). Deploying a fleet-db with the step-9 backfill
stamps pre-existing driver rows trusted exactly once; deploy the new fleet-db
binary together with this loomcli or freshly API-registered workflows will
fail `sandbox_required` under `LOOM_DRIVER_SANDBOX=process`. Knobs:
`LOOM_DRIVER_SANDBOX_IMAGE` (default `docker.io/library/node:22-slim`),
`LOOM_DRIVER_SANDBOX_RUNTIME` (`runsc` for gVisor),
`LOOM_DRIVER_SANDBOX_BINARY`, and the mandatory resource caps
`LOOM_DRIVER_SANDBOX_MEMORY` (1g), `LOOM_DRIVER_SANDBOX_CPUS` (1.0),
`LOOM_DRIVER_SANDBOX_PIDS_LIMIT` (256). Real-podman integration test:
`LOOM_SANDBOX_PODMAN_TEST=1 go test ./internal/driver -run TestContainerLauncherPodmanIntegration -v`.

Step-9 acceptance gate: `scripts/test-step9-sandbox.sh` runs an UNTRUSTED
HTTP-submitted workflow through the container sandbox holding only
`LOOM_RUN_TOKEN` and observes every forbidden path as a denial (env audit,
direct fleet-db write, foreign-run op, off-host egress). Run with
`LOOM_STEP9_SANDBOX=podman` on a native-Linux podman host (loom-dev) for the
network-isolation legs; the default fake runtime covers everything else on
any machine (the macOS podman-machine VM cannot cross the host unix-socket
relay boundary). The no-container half of the gate is
`go test ./internal/driver -run TestStep9` (step9_e2e_test.go).

### Egress modes (SB4)

`LOOM_DRIVER_SANDBOX_EGRESS=all|serve-only|none|delegated` bounds what a
containerized workflow can reach over the network. Empty resolves per run
trust level: trusted → `all`, untrusted → `serve-only` (fail closed); an
explicit value is an operator decision applying to every run. `serve-only`
runs the container with `--network=none` plus a unix-socket relay: the
launcher rewrites `LOOM_DRIVER_API_URL` to `http://127.0.0.1:8484`, an
in-container forwarder bridges that loopback port to a bind-mounted unix
socket, and the host-side relay forwards the socket exclusively to the serve
driver-API address captured at launch — the workflow can dial serve and
nothing else. `none` is `--network=none` with no relay (pure-compute runs).
`delegated` is for T3/T4 deployments where a Kubernetes NetworkPolicy /
host firewall enforces serve-only OUTSIDE the launcher: the container runs
on the engine default network and the placement record stays truthful by
stamping mode `delegated`. Every run's placement record carries
`egress_mode` + `egress_mechanism` (§9.6 audit). Reachability note: under
`all`/`delegated` the container sees the engine network, so a loopback
`LOOM_DRIVER_API_URL` (127.0.0.1) is NOT reachable from inside — point it at
a host-reachable address there; `serve-only`'s relay bridges loopback
automatically (the rewrite + unix socket terminate on the host).

SELinux-enforcing hosts (Fedora/RHEL): the default policy denies BOTH the
temp-file bind mounts (`user_tmp_t` launcher/env files — affects every
container-mode run, not just serve-only) and the relay socket connect
(`container_t → unconfined_t unix_stream_socket connectto`, AVC-verified).
Deploys there need serve's TMPDIR on a `container_file_t`-labeled directory
plus a targeted policy module, e.g.
`(allow container_t unconfined_t (unix_stream_socket (connectto)))` via
`semodule -i`. Debian/Ubuntu hosts (loom-dev shape, AppArmor) are unaffected.
macOS dev note: host unix sockets do not cross the podman-machine VM
boundary (virtiofs shares the inode, not the listener), so the relay leg of
`LOOM_SANDBOX_PODMAN_TEST=1 go test ./internal/driver -run
TestContainerEgressPodmanIntegration -v` asserts only under native Linux
podman; the `--network=none` blocking legs assert everywhere, and
`TestSandboxEgressForwarderHostNode` exercises the full relay mechanism
under host node on every platform.

## Quick Reference

```bash
loom data ready --limit 10     # Find available work
loom data show <id>            # View issue details
loom data claim <id>           # Claim work
loom data close <id> --reason "done"  # Complete work
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
