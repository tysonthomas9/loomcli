# Loom + fleet-db test stack on GCP

A pipeline for standing up a disposable Loom stack on a GCP VM, proving it
works, and tearing it down. Four containers on one VM — Redis, fleet-db, loom
(`serve` + `daemon`), and a Caddy image serving the SPA — with workspace file
blobs in GCS.

```
make up NAME=loom-pr512      # build + push images, apply, wait until healthy
make smoke NAME=loom-pr512   # end-to-end proof, non-zero exit on failure
make tunnel NAME=loom-pr512  # IAP tunnels + the browser URL
make down NAME=loom-pr512    # destroy everything the stack created
```

`NAME` is the isolation boundary, in three senses: it prefixes every resource,
it is the network tag the firewall rules target so concurrent stacks cannot
reach each other's ports, and it selects a **Terraform workspace** so each stack
has its own state. That last one matters -- without it `make down NAME=b` reads
whatever state the directory happens to hold and destroys stack A.

Local tunnel ports are derived from the name too, in Terraform, and exported as
outputs; `make tunnel` reads them rather than recomputing them, and refuses to
touch a port it did not open.

## Prerequisites

- `terraform`, `gcloud`, `docker`, `git`
- A fleet-db checkout beside this repo (or set `FLEETDB_SRC`)
- `roles/owner`, or enough to create VMs, buckets, secrets, HMAC keys and IAM
- APIs enabled: `compute`, `secretmanager`, `iap`, `artifactregistry`, `storage`

`make preflight` checks the binaries, verifies you can actually reach the
project, and creates the Artifact Registry repo if it is missing. It runs
automatically as part of `make up`.

Two defaults worth overriding explicitly:

- `PROJECT` falls back to your gcloud default project, which is often stale or
  belongs to another org. Preflight now fails with one clear line instead of an
  IAM error halfway through an apply, but passing `PROJECT=<id>` is safer.
- `FLEETDB_SRC` defaults to a sibling `fleet-db` checkout. **In a git worktree
  that path does not exist**, so set it:
  `make up FLEETDB_SRC=~/codebase/code-agents/loom-aug/fleet-db`.

Either `tofu` or `terraform` works; `TF_BIN` picks the binary and prefers
OpenTofu when both are present.

## Running real agents (codex)

By default the stack runs `localdogfood`, a deterministic backend that needs no
credentials. To drive real agents:

```
gcloud secrets create loom-codex-auth --replication-policy=automatic \
  --data-file="$HOME/.codex/auth.json"

make up NAME=loom-pr512 CODEX=1
```

`CODEX=1` changes three things, and each is load-bearing:

- **The codex CLI is a build arg, not part of the image.**
  `test/local-mode/Dockerfile` defaults `INSTALL_CODEX=false`, so an image built
  without it makes the entrypoint fail with *"codex binary is not installed"*.
  `CODEX=1` passes `--build-arg INSTALL_CODEX=true` and tags the image
  `<sha>-codex` so the two variants cannot be mistaken for each other.
- **`auth.json` is mounted, not baked.** It is fetched from Secret Manager at
  boot to `/opt/loom/codex/auth.json` (0600) and mounted read-only at
  `/codex-host`. The entrypoint copies it to `/root/.codex/auth.json`, which is
  writable, so codex can refresh its token. Mounting straight onto `/root/.codex`
  breaks the refresh on first expiry.
- **The `plan` role is relaxed to writable.** Explained below.

Terraform never creates the codex secret: it holds a personal OpenAI credential
and has no business in Terraform state. Preflight fails with the exact `gcloud`
command if it is missing.

### Why the plan role must be writable under codex

loom seeds the built-in `plan` role with `read_only: true` on every workspace.
That knob is inert on `localdogfood`. Under codex it becomes
`--sandbox read-only`, which is **bubblewrap — and bubblewrap does not run
inside a stock Docker container.** Measured here, one wall behind the other:

| Container config | Result |
| --- | --- |
| default | `bwrap: No permissions to create a new namespace` — Docker's seccomp profile denies `clone`/`unshare` with `CLONE_NEWUSER` without `CAP_SYS_ADMIN` |
| `seccomp=unconfined` | `bwrap: Failed to make / slave: Permission denied` — AppArmor's `docker-default` denies `mount` |
| `seccomp=unconfined` + `apparmor=unconfined` | would work |

**This stack deliberately does not make that trade.** Clearing both means
stripping the container's real isolation to enable a weaker sandbox inside it —
and codex's read-only sandbox also carries `--unshare-net`, while the planner
reaches Loom over HTTP. So even the fully-relaxed version buys a planner that
cannot claim, read, or complete a task.

The failure is silent, which is what makes it expensive: the agent exits 0
having accomplished nothing, the daemon retries it, and the stack stays green
while no task ever moves.

### The relax has to happen before the first agent spawns

It runs as a `role-init` sidecar **inside** the compose stack, not from systemd.
The role does not exist until loom creates the workspace, so the sidecar starts
alongside loom and polls every 200ms. Doing it after the stack reports healthy
is too late — measured before the fix, the first planner session was created at
22:46:23 and compose did not report loom healthy until 22:46:24, so run #1 was
always poisoned. After the fix:

```
role-init finished:  00:00:46.136
first sessions:      00:00:48
bwrap failures:      0   (was 11 on run #1)
```

One consequence worth knowing: `docker compose up -d --wait` fails the unit when
*any* container exits, including a one-shot that exits 0. The systemd unit
therefore starts everything with `up -d` and waits in a second step that names
only the long-running services.

## Access

The VM has **no public IP** by default and access is via IAP tunnels, so
reachability follows IAM.

Container ports publish on all interfaces, not `127.0.0.1`. That is deliberate:
IAP forwards to the VM's network interface, so a loopback-only bind makes the
tunnel fail with `failed to connect to backend`. Redis publishes no host port at
all. Cloud NAT gives the VM outbound access for image pulls.

What closes those ports is a **deny** rule, not the allow rules. GCP firewall
rules are additive, and a stock default VPC carries `default-allow-internal` at
priority 65534 — so any other VM in the network could reach fleet-db, which runs
with `--auth-dev-mode` and `--authz-enabled=false`. The stack adds a deny at
priority 1100, below the IAP allows at 1000 and above the default rules.
Measured from a peer VM in the same VPC:

```
loom-tftest  8280/8282/8283/22  -> blocked      (this stack, deny rule)
peer VM      8090               -> REACHABLE    (no deny rule, same VPC)
```

The priority gap is deliberate: a deliberate allow an operator adds at
priority < 1100 still takes effect.

`enable_external_ip = true` gives the VM a public address, but on its own that
changes nothing reachable: the deny rule still covers `0.0.0.0/0`. To actually
serve traffic without IAP you also need your own allow rule at priority < 1100,
which is the point — opening these ports should be an explicit act.

## What it creates

| Resource | Notes |
|---|---|
| `google_compute_instance` | `e2-standard-2`, Ubuntu 24.04, cloud-init brings the stack up |
| `google_service_account` | Narrow: its own bucket, its own secrets, logs, metrics |
| `google_storage_bucket` | Content-addressed workspace files, uniform access, no versioning |
| `google_storage_hmac_key` | GCS S3-interop credential for fleet-db |
| 4 × `google_secret_manager_secret` | Redis password, workspace-file token, S3 id + secret |
| 3 × `google_compute_firewall` | Two IAP allows (priority 1000) plus the deny (1100) that closes everything else |
| Cloud Router + NAT | Only when the VM has no external IP |

Secrets are generated by Terraform, stored in Secret Manager, and fetched by
the VM at boot. None is baked into an image or the compose file. Rotating one is
a new secret version plus `systemctl restart loom-stack` — no redeploy.

Redis runs AOF (`--appendonly yes --appendfsync everysec`) into a named volume,
so a restart keeps fleet-db's workspaces, roles and issues. It did not always:
with persistence off, the restart above silently discarded all of them while
loom's filesystem volumes survived, leaving the two out of step. Measured across
`systemctl restart loom-stack`: 375 keys before, 384 after, issue count
unchanged. Replacing the VM is still a clean slate — that is the redeploy path.

## Why the UI needs its own image

`loom serve` answers only `/api/*`, `/healthz`, `/sse/*` and `/ws-tab/*`. The
frontend is a separate static bundle, which the repo's compose file mounts from
a local checkout. A provisioned VM has no checkout, so the bundle has to travel
inside an image — `ui/Dockerfile` builds it, and `make images` does that for
you after `make build-frontend`.

The Caddy config sets `flush_interval -1`. Without it, responses are buffered
and both SSE and the terminal WebSocket appear to hang.

## Gotchas encoded here

These each cost real debugging time and are now handled in code:

- **`FLEET_WORKSPACE_FILE_STORE` defaults to `local`.** Omit it and all eight S3
  variables are read and silently ignored; blobs land on the container disk.
- **`FLEET_WORKSPACE_FILE_TOKEN_SECRET` must be base64url of ≥32 bytes.** Wrong
  format disables the workspace file API with only a log line. The random
  provider emits standard base64, so `secrets.tf` translates the alphabet.
- **Trailing newlines in secrets.** A password stored with a newline reaches
  Redis verbatim and fails as `WRONGPASS`. The fetch script uses `end=""`.
- **Upload grant headers are mandatory.** Skipping them gives GCS
  `MalformedSecurityHeader` on `x-amz-checksum-sha256`.
- **Workspace creation is `POST /api/v1/admin/workspaces`**, not
  `/api/v1/workspaces`.
- **HMAC keys outlive hand-rolled stacks.** Managing the key in Terraform means
  `destroy` deactivates and removes it instead of leaving a live credential
  behind.
- **The repo `.dockerignore` excludes `internal/webui/frontend/dist/`** -- the
  exact bundle the UI image serves. Building that image from the repo root fails
  the `COPY` no matter how recently you ran `make build-frontend`, so the build
  streams a tar of just the two paths it needs as the context instead.
- **cloud-init runs once per instance, and metadata is mutable.** Changing an
  image tag without forcing replacement updates metadata, reports success, and
  deploys nothing. A `terraform_data` hash of the rendered boot config drives
  `replace_triggered_by` so a changed tag replaces the VM.
- **Secrets fetched as `printf '%s' "$(secret X)"` fail OPEN.** `set -e` sees
  printf's status, not the substitution's, so a Secret Manager blip wrote an
  empty `.env` over a good one -- and an empty Redis password removes auth.
  Assign first, reject empties, write through a temp file and rename.
- **Tag each image from its own commit.** fleet-db tagged with loom's SHA meant
  a fleet-db change did not move the tag and the stack kept the stale image.
- **Allow rules do not close anything.** GCP firewall rules are additive; only a
  deny rule makes "IAP only" true on the default VPC.
- **A one-shot that exits non-zero is invisible unless something reads it.**
  `role-init` failing left systemd reporting success, so an `ExecStartPost`
  now inspects its exit code and fails the unit.
- **`smoke` has to be re-runnable.** Asserting `publish tree == 201` passed once
  and failed on every later run, because a re-publish of identical
  content-addressed bytes returns 200.
- **codex's read-only sandbox cannot run in a stock container.** Two walls, one
  behind the other; see above. Keep `plan_role_read_only=false` under codex.
- **`docker compose up --wait` returns before loom's daemon spawns agents.**
  Anything that must happen before the first agent run belongs in the stack, not
  in `ExecStartPost`.
- **`up -d --wait` fails on a one-shot container that exits 0.** Start, then
  wait on the long-running services by name.
- **`ports:` publish on all interfaces, never `127.0.0.1:`.** IAP forwards to
  the VM's network interface, so a loopback bind makes every tunnel fail with
  `failed to connect to backend`. The firewall is what closes these ports.

## What `make smoke` proves

21 checks, non-zero exit on any failure. The workspace-file half exercises all
five operations and compares bytes after a GCS round trip, NUL bytes and exec
bit included. The second half covers what storage says nothing about: the
seeded workspace, the built-in roles, the effective `plan` `read_only` value,
registered agents, sessions the daemon actually spawned, and the SPA plus its
Caddy proxy and deep-link fallback.

That second half exists because the gate used to pass with a dead daemon, an
unrelaxed plan role, and a completely broken UI. Verified negatively too:
setting `plan read_only=true` makes it fail on exactly that check.

## Scope

This deploys the **VM** topology, which is the one that runs loom end to end.
Cloud Run works for fleet-db but not for loom: the daemon needs always-on CPU
for its 1s and 30s tickers, a unix control socket, and persistent worktrees, and
`loom serve` reads the daemon's state file off the local filesystem. Splitting
them is a code change, not a config change.

## Cost

`e2-standard-2` is roughly $49/mo running and $2/mo stopped. `make down` is the
cheap option for a test stack; stop the VM if you want to keep its disk.
