---
name: local-runtime-verification
description: >-
  Verify Loom runtime behavior against an isolated local-mode stack with
  explicit ownership, evidence, served-artifact identity, and cleanup. Use
  before claiming local UI, daemon, session, workflow, or agent-runtime proof.
  Use loom-pr-test for the underlying product-flow details.
---

# Local Runtime Verification

Use this skill for a Loom runtime claim that touches the local-mode stack,
browser UI, daemon, FleetDB state, sessions, transcripts, diffs, scheduling,
or a real agent backend. It hardens `.agent-skills/loom-pr-test`; follow that
runbook as well for the product flow being tested.

## Evidence Coordinates

Before any slow, stateful, or externally billed command, state these five
coordinates in the run ledger and the user update:

- **Depth:** focused, integration, or end-to-end.
- **Realness:** deterministic orchestration, real local backend, or live
  external backend.
- **Provisioning:** existing owned stack, newly created isolated stack, or no
  stack.
- **Polarity:** prove behavior, reproduce a defect, or verify a fix.
- **Target:** exact branch/worktree, workspace, task/session IDs, and UI/API
  URLs.

`make local-mode-up` is deterministic orchestration proof. It does not prove
real model behavior. `make local-mode-codex-up` uses the authenticated Codex
CLI and can use a paid external service; disclose that boundary before
starting it. Do not call an unverified path "real" or "live."

## Mandatory Ownership Preflight

Worktrees do not isolate host ports, Compose projects, image tags, browser
profiles, processes, or volumes. Never use the default
`loomcli-local-mode` project or ports 8280/8282/8283 unless the user explicitly
asks for that exact shared stack.

1. Choose a short unique `RUN_NAME`, for example `journey-<branch>-<date>`.
2. Choose `LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<RUN_NAME>` and a
   matching free port triplet such as 8480/8482/8483. Before reserving it,
   inspect every candidate port:

   ```sh
   lsof -nP -iTCP:8480 -sTCP:LISTEN
   lsof -nP -iTCP:8482 -sTCP:LISTEN
   lsof -nP -iTCP:8483 -sTCP:LISTEN
   podman ps --format '{{.Names}}\t{{.Ports}}\t{{.Labels}}'
   docker ps --format '{{.Names}}\t{{.Ports}}\t{{.Labels}}'
   ```

   If ownership is unclear or a listener exists, pick another triplet. Do not
   stop, replace, or reuse a foreign resource.
3. Create a private browser profile and named browser session under
   `/tmp/loom-agent-browser/<RUN_NAME>/`. Never use `agent-browser close --all`.
4. Record the project, ports, compose runner/files, image tags, profile, and
   process/container names in a ledger at
   `/tmp/loom-local-runtime/<RUN_NAME>/ledger.md`.
5. An existing stack may be reused only when the current run created it and
   the ledger proves its project, ports, and worktree identity.

## Start and Bind the Exact Stack

Always carry the same Compose coordinates through start, logs, verification,
and teardown. A representative deterministic run is:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<RUN_NAME> \
LOCAL_MODE_FLEETDB_PORT=8480 \
LOCAL_MODE_API_PORT=8482 \
LOCAL_MODE_UI_PORT=8483 \
LOCAL_MODE_COMPOSE_UP_FLAGS='--build -d' \
make local-mode-up
```

For a real Codex claim, use the same coordinates with
`make local-mode-codex-up`; disclose that it uses the current Codex
authentication and may incur paid-service use. If credentials are unavailable,
report the live proof blocked. Do not substitute hand-written session,
FleetDB, transcript, diff, lock, or Redis state.

Run the matching verifier against the owned API port:

```sh
LOCAL_MODE_API_URL=http://127.0.0.1:8482 make local-mode-verify
LOCAL_MODE_API_URL=http://127.0.0.1:8482 make local-mode-codex-verify
```

Do not accept a passing verifier as proof that it exercised the intended
worktree or browser bundle. Bind all browser/API calls to the recorded ports.

## Served Artifact Identity

When a UI claim depends on a new frontend build, prove the browser saw that
build before testing behavior:

1. Record the URL and content hash of the served JS/CSS assets used by the
   page.
2. Record the local built asset path and hash expected from the target
   worktree.
3. Fail the proof as stale or unverified if the relevant hashes do not match.

Do not repair a stale served bundle by copying `dist/` or editing containers
unless the user explicitly asked for deployment work. A stale artifact is a
failed verification prerequisite, not product evidence.

## State Mutation and Browser Rules

- Use product APIs/UI/CLI flows only. Never seed or edit Redis, FleetDB
  backing data, locks, sessions, transcripts, diffs, or workspace state files.
- State-changing API calls, task transitions, and agent starts require either
  explicit user authorization or a documented harness setup that owns the
  fixture. Record the request, response/status, before state, and required
  restoration state in the ledger.
- Restore every mutated fixture before reporting, then verify the restoration
  through the product API. If a lease/reaper changes it concurrently, report
  that interference instead of silently retrying until green.
- Use a dedicated browser profile and session. Record screenshots, DOM/API
  observations, and timestamps; close only the named session created by this
  run.

## Runtime Readiness Is More Than HTTP 200

Before asserting a stack is ready, collect all applicable evidence:

- FleetDB and Loom API reachability.
- The configured workspace/agent/task exists through public APIs.
- The intended daemon configuration was accepted and applied, not merely
  stored. Capture the relevant health/config response and daemon logs when a
  configuration affects the claim.
- UI and API agree on the material state.
- Session/transcript/diff/artifact claims are confirmed through FleetDB-backed
  product endpoints or UI, not local filesystem discovery.

If an HTTP health endpoint is green while the daemon rejects configuration,
report the runtime proof as failed. Include the rejection reason and logs.

## Required Report

Every final result must include:

1. Evidence coordinates and the exact command path.
2. Compose project, ports, URLs, browser profile/session, and artifact hashes
   when UI code was in scope.
3. Which observations are terminal/API/browser-confirmed versus inferred.
4. The backend classification: deterministic, real local, or live external.
5. Fixture mutations and verified restoration, if any.
6. Verifier output, failures, mismatches, and evidence gaps.
7. Cleanup outcome, including the exact project torn down or an explicit reason
   it remains running, plus the retained evidence-root path.

Never summarize a deterministic run as real-agent proof, a stale bundle as UI
proof, or an agent narrative as terminal-confirmed evidence.

## Evidence Retention

Runtime cleanup and evidence retention are separate operations. Before teardown,
write the final ledger, command outcomes, hashes, screenshots, and API/browser
observations under `/tmp/loom-local-runtime/<RUN_NAME>/`. After teardown, keep
that evidence root intact and report its path to the user. Do not delete it
until the user has received the handoff or explicitly asks to discard it.

It is fine to remove only the browser profile and other disposable resources
created by the run after their paths and relevant artifacts have been recorded
in the retained ledger. If an artifact is too large or sensitive to retain,
record its hash, size, reason for removal, and the removal time in the ledger.

## Cleanup

Unless the user asks to keep the stack, tear down only the recorded project
with the same compose runner and override files used at startup:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<RUN_NAME> \
LOCAL_MODE_COMPOSE_FILES='<same override files>' \
make local-mode-down
```

Then confirm that only containers, listeners, profiles, and disposable runtime
files created by this run were removed. Retain the evidence root described
above. Never prune globally, remove foreign volumes, or terminate an unknown
process.
