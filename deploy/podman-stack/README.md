# podman-stack — local distributed-topology e2e stack

A LOCAL, resource-capped podman stack that emulates the DISTRIBUTED Loom
topology (RUNTIME-AND-DEPLOYMENT.md T3/T4 shape) for real platform e2e
testing — loom-dev deploys are off-limits for that. This is also the first
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

Prereqs: podman (machine running on macOS), Go toolchain, rsync, curl, jq,
openssl, and a host-built flue dist (`cd flue && pnpm install && pnpm build`).

## Startup ordering (fleet-db before loomcli)

`depends_on` conditions enforce: `redis (healthy)` → `fleet-auth-seed
(completed)` → `fleet-db (healthy /readyz)` → `loom-serve (healthy
/api/health)` → `worker`, `stub-upstream`.

**fleet-db MUST be ready before loom serve starts.** If `LOOM_FLEET_DB_URL`
does not answer at serve startup, serve falls back to spawning an *embedded*
fleet-db subprocess inside its own container — the stack then "works" but you
are silently testing the wrong topology (and the embedded instance has no
auth seeding). Never start `loom-serve` with `--no-deps`.

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
| `LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS` | `2` | attempts before a TaskRun parks failed |
| `LOOM_STACK_DRIVER_SANDBOX` | *(empty)* | `LOOM_DRIVER_SANDBOX` on serve; see sandbox posture |
| `LOOM_STACK_WORKER_BACKEND` | *(empty)* | AI backend for `loom worker` replicas |
| `LOOM_STACK_FRONTEND_URL` | `http://127.0.0.1:18282` | CORS origin(s), comma-separated |
| `FLEET_LOG_LEVEL` | `info` | fleet-db log level |

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
- **serve "works" without fleet-db** — you started loom-serve with
  `--no-deps` or fleet-db was down at serve start; serve spawned an embedded
  fleet-db. Restart loom-serve after fleet-db is healthy.
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
