# Loom + fleet-db test stack on GCP

A pipeline for standing up a disposable Loom stack on a GCP VM, proving it
works, and tearing it down. Four containers on one VM — Redis, fleet-db, loom
(`serve` + `daemon`), and a Caddy image serving the SPA — with workspace file
blobs in GCS.

```
make up NAME=loom-pr512 TUNNEL_PORT_BASE=18100  # build + push, apply, wait
make smoke NAME=loom-pr512   # end-to-end proof, non-zero exit on failure
make tunnel NAME=loom-pr512 TUNNEL_PORT_BASE=18100  # IAP tunnels + browser URL
make down NAME=loom-pr512    # destroy everything the stack created
```

`NAME` is the isolation boundary, in four senses: it prefixes every resource,
it names a **VPC of its own** so stacks cannot see each other at all, it is the
network tag the firewall rules target, and it selects a **Terraform workspace**
so each stack has its own state. That last one matters -- without it
`make down NAME=b` reads whatever state the directory happens to hold and
destroys stack A.

Terraform state is stored in the shared, versioned GCS bucket
`<PROJECT>-loom-tfstate`; `make state` creates it if needed. Each stack remains
isolated by its workspace prefix, while the bucket's IAM boundary protects the
credentials held in state from the local checkout.

The per-stack VPC is what makes concurrent stacks actually work, not just a
tidier boundary. Sharing the project's `default` network meant sharing its
address space, and Cloud NAT refuses two gateways with overlapping subnet
coverage in one network and region -- so the first `make up` succeeded and the
second failed on `NAT gateway ... cannot have overlapping subnetwork ranges`.
Separate networks make the subnet CIDR a constant (`var.subnet_cidr`, the same
`10.90.0.0/24` for every stack) instead of something to allocate. The cost is
quota: a project allows **5 VPC networks** by default, one of which is
`default`, so about four concurrent stacks before you need a quota bump. That
surfaces as a clear error at apply time.

Local tunnel ports are owned by Terraform and exported as outputs; `make tunnel`
reads both the local and remote ports rather than recomputing them, and refuses
to touch a port it did not open. The default local ports are name-derived for
Set `TUNNEL_PORT_BASE`/`tunnel_port_base` to a unique UI port for every stack
tunnelled from the same workstation; API and fleet-db use the next two ports.
The value is required by the Makefile and Terraform so the deployment never
pretends to allocate a collision-free local port automatically.

## Prerequisites

- `terraform` (or `tofu`), `gcloud`, `docker` (or `podman`), `git`
- A fleet-db checkout containing the wired workspace-file API and S3 store
  (or set `FLEETDB_SRC`); `make preflight` verifies those files
- `roles/owner`, or enough to create VMs, buckets, secrets, HMAC keys and IAM
- APIs enabled: `compute`, `secretmanager`, `iap`, `artifactregistry`, `storage`

`make preflight` checks the binaries, verifies you can actually reach the
project, and creates the Artifact Registry repo if it is missing. It runs
automatically as part of `make up`.

If Docker is unavailable but a Podman machine is running, use
`CONTAINER_CLI=podman`; the Makefile logs Podman into Artifact Registry with
the active gcloud access token before pushing images.

The `loom` Artifact Registry repository is a shared project bootstrap resource:
preflight creates it once (concurrent creators tolerate `ALREADY_EXISTS`), and
`make down` deliberately leaves it in place. The VM's reader binding is scoped
to that repository, not the whole project.

Two defaults worth overriding explicitly:

- `PROJECT` falls back to your gcloud default project, which is often stale or
  belongs to another org. Preflight now fails with one clear line instead of an
  IAM error halfway through an apply, but passing `PROJECT=<id>` is safer.
- `FLEETDB_SRC` defaults to a sibling `fleet-db` checkout. **In a git worktree
  that path does not exist**, so set it. The checkout must include the
  workspace-file API (`internal/api/workspace_files.go`) wired from
  `cmd/fleet-db/main.go` and the S3 store used for GCS interoperability:
  `make up FLEETDB_SRC=/path/to/compatible/fleet-db`.

Either `tofu` or `terraform` works; `TF_BIN` picks the binary and prefers
OpenTofu when both are present.

## Running real agents (codex)

By default the stack runs `localdogfood`, a deterministic backend that needs no
credentials. To drive real agents:

```
gcloud secrets create loom-codex-auth --replication-policy=automatic \
  --data-file="$HOME/.codex/auth.json"

make up NAME=loom-pr512 TUNNEL_PORT_BASE=18100 CODEX=1
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

Two things keep those ports closed, and it is worth knowing which does what.

The **network** is the primary boundary: each stack gets its own custom-mode
VPC, which ships no permissive rules at all. Nothing else in the project shares
it, so there is nothing to be reachable from.

The **deny** rule is the second layer, and it exists because allow rules close
nothing — GCP firewall rules are additive. It matters most when stacks shared
the project's `default` network, which carries `default-allow-internal` at
priority 65534: any other VM could reach fleet-db, which runs with
`--auth-dev-mode` and `--authz-enabled=false`, so reachable meant fully
readable and writable. Measured from a peer VM back when the stack lived on the
default network:

```
loom-tftest  8280/8282/8283/22  -> blocked      (this stack, deny rule)
peer VM      8090               -> REACHABLE    (no deny rule, same VPC)
```

On a per-stack VPC that exposure is gone by construction, so treat the numbers
above as the reason the rule is there rather than a description of today's
topology. The rule stays because it is free and still bites if this network is
ever peered, or if someone adds a broad allow while debugging.

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
| `google_service_account` | Narrow: its own bucket, its own secrets, the shared image repository, logs, metrics |
| `google_storage_bucket` | Content-addressed workspace files, uniform access, no versioning |
| `google_storage_hmac_key` | GCS S3-interop credential for fleet-db |
| 5 × `google_secret_manager_secret` | Redis password, workspace-file token, run-token signing key, S3 id + secret |
| `google_compute_network` + `google_compute_subnetwork` | Custom-mode VPC per stack; `private_ip_google_access` on |
| 3 × `google_compute_firewall` | Two IAP allows (priority 1000) plus the deny (1100) that closes everything else |
| Cloud Router + NAT | Only when the VM has no external IP |
| Artifact Registry `loom` repository | Shared project bootstrap, created by preflight and retained across stack teardown |

Secrets are generated by Terraform, stored in Secret Manager, and fetched by
the VM at boot. None is baked into an image or the compose file. This includes
loom's `LOOM_RUN_TOKEN_SIGNING_KEY`, whose persistence lets in-flight run tokens
survive a serve restart. Rotating one is a new secret version plus
`systemctl restart loom-stack` — no redeploy.

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
  image reference without forcing replacement updates metadata, reports
  success, and deploys nothing. A `terraform_data` hash of the rendered boot
  config drives `replace_triggered_by` so a changed digest replaces the VM.
- **Secrets fetched as `printf '%s' "$(secret X)"` fail OPEN.** `set -e` sees
  printf's status, not the substitution's, so a Secret Manager blip wrote an
  empty `.env` over a good one -- and an empty Redis password removes auth.
  Assign first, reject empties, write through a temp file and rename.
- **Tag each image from its own commit.** fleet-db tagged with loom's SHA meant
  a fleet-db change did not move the tag and the stack kept the stale image.
- **A commit id names a commit, not a working tree.** Building from a dirty
  checkout pushed modified source under the clean `<sha>` tag, so `loom_image`
  never changed, the VM was never replaced, and `wait-healthy` passed against
  the *old* container -- indistinguishable from a change that had no effect.
  The tag now carries a hash of the actual delta (`git diff HEAD` plus the
  hashed contents of untracked files, which land in the build context too) and
  the generated UI bundle contents, which are ignored by git but copied into
  the UI image.
- **Allow rules do not close anything.** GCP firewall rules are additive; a deny
  rule is what made "IAP only" true while stacks shared the default VPC.
- **Cloud NAT will not tolerate overlapping coverage.** One
  `ALL_SUBNETWORKS_ALL_IP_RANGES` gateway per stack on a shared network means
  the second stack cannot be applied at all. Give each stack its own VPC and
  the question disappears.
- **A gate must read the stack, not the command line.** `make smoke` rebuilt
  its expectations from Make arguments, so the documented `make smoke NAME=...`
  after `make up CODEX=1` asserted the wrong plan-role state and failed a
  healthy stack. Every expectation now comes from `tofu output`. The same rule
  covers ports: `wait-healthy` hardcoded 8282, so overriding `iap_web_ports`
  bought a 15-minute timeout against a stack that was fine.
- **`docker inspect` reports exit code 0 for a RUNNING container.** The
  `role-init` check polled for a terminal state and then read `.State.ExitCode`
  unconditionally, so a sidecar that never finished was indistinguishable from
  one that succeeded. Reject the running state explicitly, and give the check a
  window longer than the sidecar's own timeout so the two do not race.
- **A one-shot that exits non-zero is invisible unless something reads it.**
  `role-init` failing left systemd reporting success, so an `ExecStartPost`
  now inspects its exit code -- and its liveness -- and fails the unit.
- **Image tags are lookup labels, not deployment identity.** `make apply`
  resolves each pushed tag to its Artifact Registry digest and passes
  `image@sha256:...` to Terraform. The UI base and Redis base are also pinned,
  and the Codex CLI build uses an explicit version.
- **`smoke` has to be re-runnable.** Asserting `publish tree == 201` passed once
  and failed on every later run, because a re-publish of identical
  content-addressed bytes returns 200.
- **codex's read-only sandbox cannot run in a stock container.** Two walls, one
  behind the other; see above. Keep `plan_role_read_only=false` under codex.
- **`docker compose up --wait` returns before loom's daemon spawns agents.**
  The loom healthcheck now waits for the seeded agents and a daemon session as
  well as `/api/config`, so anything that must happen before the first agent
  run is ready before `make up` returns.
- **`up -d --wait` fails on a one-shot container that exits 0.** Start, then
  wait on the long-running services by name.
- **A failed readiness check leaves paid infrastructure behind.** `make up`
  prints the exact `make down NAME=... PROJECT=...` command to use after a
  post-apply failure; cleanup is explicit so operators can inspect a failed VM
  before destroying it.
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
