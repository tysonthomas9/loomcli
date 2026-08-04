# podman-stack — local distributed-topology e2e stack

> **Status:** Current · *audited 2026-08-03*. Ports, env names, resource caps,
> startup ordering, and the acceptance stages were checked against
> `compose.yaml`, `compose.codex.yaml`, `env.template`, `build.sh`, and
> `scripts/test-podman-stack.sh`. The one unverifiable claim (the "T3/T4
> shape") is marked inline below.

A LOCAL, resource-capped podman stack that emulates the DISTRIBUTED Loom
topology for real platform e2e testing — loom-dev deploys are off-limits for
that. (UNVERIFIED: this shape is cited elsewhere as the "T3/T4 shape" from
`RUNTIME-AND-DEPLOYMENT.md`; that file does not exist in this repo, has no git
history here, and the tiers are not defined anywhere in-tree.) This is also the first
**codified** deployment of the serve + fleet-db pairing (the loom-dev box is
hand-rolled binaries + systemd).

```
host (macOS) ── podman machine (Linux VM)
   │
   ├─ 127.0.0.1:18282 ─► loom-serve   control plane + driver-op HTTP API +
   │                                  driver executor + embedded task workers
   ├─ 127.0.0.1:18280 ─► fleet-db     issue/coordination DB (Redis backend)
   ├─ 127.0.0.1:18299 ─► stub-upstream  connector-egress recorder
   │
   └─ private network only:  redis, worker(s), fleet-auth-seed (one-shot)
```

## Quick start

```sh
# one command — generates secrets, builds, boots, smokes, tears down
loomcli/scripts/test-podman-stack.sh

# keep it running for interactive testing
KEEP_STACK=1 loomcli/scripts/test-podman-stack.sh

# manual lifecycle
cd loomcli/deploy/podman-stack
cp env.template .env && chmod 600 .env   # fill the SECRETS section
./build.sh
podman compose --env-file .env up -d
podman compose --env-file .env down --volumes
```

Prereqs: `go`, `podman`, `rsync` (hard-required by `build.sh:36-38`), plus
curl, jq and openssl for the driver script — and **sibling `fleet-db` and
`flue` checkouts** with a host-built flue dist.

`build.sh` resolves both siblings one level up from the loomcli repo —
`$LOOMCLI_REPO/../fleet-db` and `$LOOMCLI_REPO/../flue`
(`build.sh:28-29`) — and dies if either is missing (`build.sh:41-42`). Other
harnesses in this repo default to `../../fleet-db`, so set the vars rather than
relying on layout:

```sh
export FLEET_DB_REPO=/path/to/fleet-db
export FLUE_REPO=/path/to/flue
(cd "$FLUE_REPO" && pnpm install && pnpm build)   # build.sh:47-50 requires dist/flue.js
```

## Startup ordering (fleet-db before loomcli)

`depends_on` conditions enforce: `redis (healthy)` → `fleet-auth-seed
(completed)` → `fleet-db (healthy /readyz)` → `loom-serve (healthy
/api/health)` → `worker` (`compose.yaml:66`, `:84`, `:115`, `:198`).
`stub-upstream` is **not** in that chain — its service block carries no
`depends_on` (`compose.yaml:214-234`), so it boots in parallel with redis.
The real coupling runs the other way: `compose.e2e.yaml:33` points serve's
`LOOM_CONNECTOR_GITHUB_BASE_URL` at `http://stub-upstream:8080`.

**fleet-db MUST be ready before loom serve starts.** `compose.yaml:129` pins
`LOOM_FLEET_DB_URL: http://fleet-db:8080` on loom-serve, which puts serve in
**cloud mode** (`bootstrap.DetectMode` returns `ModeCloud` whenever that var
is non-empty, `internal/bootstrap/mode.go:51-58`). In cloud mode serve is a
pure fleet-db HTTP client (`internal/bootstrap/openstore.go:101-102`,
`:112-120`) — it never spawns an embedded fleet-db, and
`Containerfile.loom-serve` ships no fleet-db binary for it to spawn anyway.
The client is built without a connectivity probe, so serve can come up and
answer `/api/health` while fleet-db is down; every store-backed call
(workspaces, issues, sweepers, worker registration) then fails with
connection errors. Never start `loom-serve` with `--no-deps`: you get a
serve that looks healthy but errors on all fleet-db traffic.
(The embedded fleet-db fallback is real code, but only in **local mode** —
`LOOM_FLEET_DB_URL` unset, `internal/bootstrap/openstore.go:122-143` — which
this stack never enters, and where `StartEmbedded` is the sole embedded
entry point, `internal/bootstrap/openstore.go:128`.)

The one-shot `fleet-auth-seed` service writes the API key and the actor's
global role directly into Redis (`fleet-db:auth:apikey:<key>`,
`fleet-db:acl:global-roles:<actor>`) before fleet-db serves traffic — the
only bootstrap path that needs no pre-existing admin credential when real
auth is enabled.

## Auth posture

- **fleet-db**: `FLEET_AUTH_ENABLED=true`, `FLEET_AUTH_DEV_MODE=false`,
  `FLEET_AUTHZ_ENABLED=true`. All writes require `X-API-Key`; the seeded
  actor (`FLEET_SEED_ACTOR`) holds a **global admin role** because serve's
  background sweepers — notably the await-timeout sweeper — need
  admin/maintainer rights across workspaces.
- **loom serve**: `LOOM_DRIVER_LEGACY_AUTH_ENV=0` — **token-only posture**.
  Workflow runtimes receive ONLY the run-scoped `LOOM_RUN_TOKEN` (minted from
  `LOOM_RUN_TOKEN_SIGNING_KEY`); the deprecated node-wide
  `LOOM_DRIVER_API_TOKEN` bearer and lease/fencing identity vars are NOT
  exported. Bundles must be built against the token-aware SDK v2.
- **workers**: `LOOM_WORKER_TOKEN` shared-secret bearer against
  `/api/internal/workers/`; `LOOM_FLEET_API_KEY` gates fleet worker
  registration.
- **connectors**: AES-256-GCM vault keyed by `LOOM_CONNECTOR_VAULT_KEY`
  (standard base64, 32 bytes). Serve refuses to enable connectors without it.

## Secrets

All secrets live in `.env` (gitignored, 0600), generated **per run** by
`scripts/test-podman-stack.sh` — never committed, never echoed. Formats:

| Var | Format | Generate |
|---|---|---|
| `LOOM_FLEET_DB_API_KEY` | opaque, `fldb_` prefix by convention | `printf 'fldb_%s' "$(openssl rand -hex 24)"` |
| `LOOM_RUN_TOKEN_SIGNING_KEY` | hex, 32 bytes (64 chars) | `openssl rand -hex 32` |
| `LOOM_CONNECTOR_VAULT_KEY` | std base64, 32 bytes | `openssl rand -base64 32` |
| `LOOM_FLEET_API_KEY` | opaque | `openssl rand -hex 24` |
| `LOOM_WORKER_TOKEN` | opaque | `openssl rand -hex 24` |
| `LOOM_STACK_STUB_SECRET` | opaque | `openssl rand -hex 16` |

## Environment reference (non-secret)

| Var | Default | Meaning |
|---|---|---|
| `LOOM_STACK_PROJECT` | `loom-podman-stack` | compose project name |
| `LOOM_STACK_SERVE_PORT` | `18282` | host loopback port → loom serve |
| `LOOM_STACK_FLEET_DB_PORT` | `18280` | host loopback port → fleet-db (driver-script seeding + negative auth tests only) |
| `LOOM_STACK_STUB_PORT` | `18299` | host loopback port → stub-upstream `/__requests` |
| `LOOM_STACK_WORKSPACE` | `PODSTACK` | workspace key the driver creates and workers join |
| `FLEET_SEED_ACTOR` | `loom-serve@podman-stack.local` | actor bound to the seeded API key |
| `FLEET_SEED_ROLE` | `admin` | global role for the actor (admin/maintainer needed by the await sweeper) |
| `LOOM_DRIVER_TASK_WORKER_CONCURRENCY` | `2` | TaskRun worker loops embedded in serve |
| `LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS` | `2` | attempts before a TaskRun is blocked after failure |
| `LOOM_STACK_DRIVER_SANDBOX` | *(empty)* | `LOOM_DRIVER_SANDBOX` on serve; see sandbox posture |
| `LOOM_STACK_WORKER_BACKEND` | *(empty)* | AI backend for `loom worker` replicas |
| `LOOM_STACK_FRONTEND_URL` | `http://127.0.0.1:18282` | CORS origin(s), comma-separated |
| `FLEET_LOG_LEVEL` | `info` | fleet-db log level |
| `LOOM_STACK_TASK_RUNNER_CMD_JSON` | *(empty)* | `LOOM_DRIVER_TASK_RUNNER_CMD_JSON` on serve — JSON argv for the driver task-runner invoker (`compose.yaml:162`). Empty = no runner wired |
| `LOOM_STACK_CODEX_RW_DIR` | `/home/node/.codex-rw` | writable `CODEX_HOME` the serve entrypoint mirrors the read-only host `~/.codex` into (`compose.yaml:163`) |
| `LOOM_STACK_FLUE_AGENT_MODEL` | *(empty)* | `LOOM_FLUE_AGENT_MODEL` on serve, for codex-backed runs (`compose.yaml:167`) |
| `LOOM_STACK_CODEX_HOST_DIR` | **required** by `compose.codex.yaml` only | host `~/.codex` dir bind-mounted read-only at `/home/node/.codex`; declared `:?` so compose aborts if unset (`compose.codex.yaml:41`) |

The last four are the A1 codex-review knobs. They are inert in the base
stack (empty/defaulted) and are set for you by
`scripts/test-a1-review-multi-container.sh:350-355`, which layers the
A1-only overlay `compose.codex.yaml` on top of `compose.yaml` (`:59`).
That overlay supplies the host `~/.codex` read-only bind and the codex
runner command; the base stack and the acceptance suite must boot without
any host `~/.codex` dependency (`compose.yaml:176-181`).

Wiring inside compose (fixed, not in `.env`): serve gets
`LOOM_ISSUE_BACKEND=fleetdb`, `LOOM_FLEET_DB_URL=http://fleet-db:8080`,
`LOOM_REDIS_ADDR=redis:6379`, `LOOM_FLEET_MODE=true`,
`LOOM_DRIVER_EXECUTOR=1`, `LOOM_BIND_ADDR=0.0.0.0`,
`LOOM_SDK_ROOT=/opt/loom-sdk`, and
`LOOM_REAL_FLUE_CMD_JSON=["node","/opt/flue/packages/cli/bin/flue.mjs"]`
(baked into the image). Workers get
`LOOM_WORKER_CONTROL_PLANE=http://loom-serve:8080` and a per-replica agent
name (`worker-<hostname>` via the entrypoint).

## Resource budget

Zero-config is capped — every container has explicit `mem_limit` / `cpus` /
`pids_limit` (fork-bomb history; nothing uncapped, ever).

| Service | Memory | CPUs | PIDs | Notes |
|---|---|---|---|---|
| redis | 256m (192mb maxmemory) | 0.5 | 128 | AOF persistence on named volume |
| fleet-auth-seed | 64m | 0.25 | 32 | one-shot, exits |
| fleet-db | 256m | 0.5 | 128 | matches k8s reference limits |
| loom-serve | 2g | 2.0 | 512 | spawns flue builds + workflow node processes |
| worker (×1) | 1g | 1.0 | 256 | per replica when scaled |
| stub-upstream | 128m | 0.25 | 64 | request log capped at 200 entries |
| **Total (1 worker)** | **≈3.7g** | **4.5** | **1120** | fits the default 8GiB/6-CPU podman machine |

Scaling workers (`podman compose up -d --scale worker=2`) adds 1g / 1.0 /
256 per replica — keep total memory ≤ ~75% of the podman machine.

## Sandbox posture (step-9 relevance)

Default `LOOM_STACK_DRIVER_SANDBOX=` (empty) → serve uses the **process
launcher**, and the capped `loom-serve` container itself is the isolation
boundary for workflow runtimes. With `LOOM_DRIVER_LEGACY_AUTH_ENV=0` the
runtime env is still token-only, so the auth legs of the step-9 gate are
meaningful here.

`LOOM_STACK_DRIVER_SANDBOX=container` would require podman **nested inside**
the serve container — rootless-nesting works on native Linux hosts, not
through the macOS podman machine. The network-isolation legs (serve-only
egress, off-host probe denial) belong to a dedicated in-VM/native-Linux lane
(`scripts/test-step9-sandbox.sh` with `LOOM_STEP9_SANDBOX=podman`), not to
this stack.

## Images

Built by `./build.sh` (host-cross-compiled, CGO_ENABLED=0, arch = podman
machine arch — no Go toolchain in any image):

| Image | Base | Contents |
|---|---|---|
| `localhost/loom-stack/fleet-db` | alpine:3.21 | `fleet-db`, `fdb` binaries; busybox wget for healthchecks |
| `localhost/loom-stack/loom-serve` | node:22-slim | `loom` binary, `/opt/loom-sdk`, `/opt/flue` (source + host dist + in-image linux `pnpm install --prod`), git/curl |
| `localhost/loom-stack/loom-worker` | node:22-slim | `loom` binary (`loom worker` route), git/curl |
| `localhost/loom-stack/stub-upstream` | node:22-slim | single-file recorder server |

## Troubleshooting

- **`podman is not reachable` / `Cannot connect to Podman`** — the podman
  machine isn't running: `podman machine start`, then `podman info`.
  Check `podman machine list` shows `Currently running`.
- **arch mismatch (`exec format error` in containers)** — the Go binaries
  were built for the wrong arch. `build.sh` targets the machine arch from
  `podman info --format '{{.Host.Arch}}'`; override with
  `LOOM_STACK_GOARCH=arm64|amd64` if you run a non-default machine.
- **port conflicts** — all publishes are loopback-only on 18280/18282/18299.
  The driver script pre-checks and aborts; change `LOOM_STACK_*_PORT` in
  `.env`. (Port discipline: stay out of the 16xxx/180xx ranges used by the
  other test harnesses.)
- **serve healthy but issues/workspaces 401/403** — the auth seed didn't run
  (e.g. you recreated the redis volume without re-running
  `fleet-auth-seed`): `podman compose --env-file .env up fleet-auth-seed`.
- **serve healthy but every issue/workspace call fails with connection
  refused** — fleet-db is down or you started loom-serve with `--no-deps`.
  Serve never falls back to an embedded fleet-db while `LOOM_FLEET_DB_URL`
  is set (see [Startup ordering](#startup-ordering-fleet-db-before-loomcli));
  the store client is created without a probe, so serve comes up regardless.
  Bring fleet-db healthy, then restart loom-serve.
- **flue build errors inside serve** (`dist/flue.js` missing at image build)
  — build flue on the host first: `cd flue && pnpm install && pnpm build`,
  then re-run `./build.sh`.
- **`variable is not set` from compose** — `.env` is missing or incomplete;
  run the driver script once or copy `env.template`.
- **workers crash-looping right after up** — expected until the driver
  creates `LOOM_STACK_WORKSPACE` in fleet-db; `restart: on-failure:10`
  covers the window. If it persists, check `LOOM_WORKER_TOKEN` matches on
  serve and worker (same `.env`).
- **host feels strained** — verify caps are applied:
  `podman stats --no-stream`. Nothing in this stack may run uncapped; if a
  service shows unlimited memory, your compose provider ignored
  `mem_limit`/`pids_limit` — use a recent podman (5.x) with the built-in
  `podman compose`.

## Acceptance suite (compose.e2e.yaml + scripts/test-podman-stack.sh)

`scripts/test-podman-stack.sh` is the staged acceptance driver. It layers
`compose.e2e.yaml` on top of `compose.yaml`:

- **session auth**: `LOOM_AUTH_URL` points at a host-side JWKS stub the
  driver runs per-suite (RS256 keypair minted into its mktemp dir); serve
  starts with `--auth-allow-insecure` so the http:// JWKS URL is allowed on
  the container network. All protected serve calls in the suite carry a
  minted session JWT; the approvals endpoint (§7 step 8) requires it.
- **deterministic task runner**: `e2e/task-runner.mjs` (mounted read-only at
  `/opt/e2e`) executes TaskRuns instantly and appends the executed task ids
  to `/work/e2e/task-runner.log` for DAG-order assertions.
- **fast stale recovery**: `LOOM_DRIVER_STALE_TASK_MAX_AGE=20` so the
  mid-epic serve-restart stage recovers interrupted tasks inside the budget.

Stages: S0 boot/health/auth-posture · S1 trust placement (untrusted
submission refused `sandbox_required` → operator promote via fleet-db PATCH)
+ distributed epic drain + SSE watch + outbox exactly-once · S2 signed
webhook → Router v2 fan-out with the deliveries[]-only 202 wire · S3
connector vault/grants/audit against stub-upstream · S4 events.await
suspend → serve restart → session approval resume · S5 mid-epic serve
restart with zero duplicate completed TaskRuns · S6 in-container network
legs + OOMKilled audit.

Knobs: `LOOM_STACK_CONNECTOR_STRICT=0` tolerates a missing github→stub
egress seam (denial + audit legs stay strict);
`LOOM_STACK_EXPECT_NETWORK_ISOLATION=1` upgrades the S6 legs from
auth-denial to required network-denial once the exec plane moves to an
internal network; `LOOM_STACK_REQUIRE_WORKER=0` downgrades worker
registration evidence; `KEEP_STACK=1` keeps the stack and the suite's tmp
workspace for post-mortem.

## Related

- [`../README.md`](../README.md) — the real production deployment reference
  (this stack is a **test** topology, not a deployment target)
- [`../../AGENTS.md`](../../AGENTS.md) — driver-runtime auth
  (`LOOM_RUN_TOKEN`, `LOOM_DRIVER_LEGACY_AUTH_ENV`) and the sandbox / egress
  posture this stack exercises
- [`../../sdk/README.md`](../../sdk/README.md) — the token-only SDK the workflow
  bundles here are built against
- [`../../docs/testing/README.md`](../../docs/testing/README.md) — where this
  stack sits among the other harnesses
- [`../../docs/loom-glossary.md`](../../docs/loom-glossary.md) — **fleet mode**
  vs **fleet-db**, **control plane**, **worker**, **task run**, **connector**
- [`../../e2e/README.md`](../../e2e/README.md) — the single-container E2E image
  (different job: no distributed topology)
