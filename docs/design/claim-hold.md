# Claim hold

A persistent, workspace-level, explicitly-owned refusal to **start** new work.

The daemon stops claiming tasks and spawning agents; every run already in
flight continues completely untouched. It exists so an operator — or the
union-autodeploy script — can quiesce a workspace before redeploying loom
binaries without killing running agents.

## What it is not

The hold is built on the **claim path only**:

- no yield file is written (`.agent.yield` is never created by a hold);
- no SIGTERM or SIGKILL is sent, and no deadline is imposed on a running run
  (`max_run_duration` remains the only bound on an in-flight agent);
- `DrainWithGrace` / `RequestYield` / `StopAgent` are neither called nor
  changed;
- `desired_state` is untouched, and the hold performs **zero** fleet-db calls —
  which is the point: it must work while fleet-db is the thing being redeployed.

The blunt instruments it replaces: `loom agent yield` is a SIGTERM with a 60s
deadline, and `desired_state` parking is a per-agent fleet-db round trip the
server never reads back.

## Semantics

`Supervisor.gateClaimsHeld` is the **first** statement of
`preFlightSetup` — the single funnel for every spawn origin (the supervise
loop, control-plane `start`, lead dispatch, ephemeral agents). A held workspace
therefore issues no `Ready` query, no `ClaimIssue`, runs no recovery and
creates no session.

A gated agent records the domain outcome `ClaimsHeld`, whose policy
disposition is `RetryUncounted` with a fixed **15s** re-check. Nothing moves:
`RestartCount`, `RateRetryCount`, `NoWorkCount`, `BlockCount`, `StopReason`
and `LastNoWork` all stay at their pre-hold values, and `ClaimsHeld` is never
quarantine-eligible. Releasing the hold resumes exactly where the fleet left
off.

Resume/checkpoint recovery is gated too — starting a resumed run is still
starting a run. The fleet lock TTL and the checkpoint survive, and the agent
resumes after release. Nothing is exempt.

`loom daemon start <agent>` and a control-plane/lead `start` are refused
loudly while held, so the command is marked failed rather than retried.

## Scope

A hold applies to **one daemon / one workspace**, not to the fleet. `loom
daemon status` says so explicitly; do not read the banner as fleet-wide.

## Commands

```
loom daemon hold --reason "..." [--ttl 60m] [--actor NAME] [--wait-idle] [--timeout 30m] [--force]
loom daemon release [--actor NAME] [--force]
```

- `--reason` is **required** for `hold`: an unexplained hold is the failure
  mode this design exists to prevent.
- Actor resolution: `--actor` > `$LOOM_ACTOR` > the OS user > `unknown`.
- `--ttl` defaults to 60m and is clamped to `[1m, 24h]`. `--ttl 0` is
  indefinite and prints a warning — a forgotten hold should self-release rather
  than park the fleet for hours.
- `--wait-idle` sets the hold **first**, then polls every 5s until no agent
  process is left running. On timeout it exits non-zero **without** releasing
  (the operator decides) and prints the still-running agents with their task
  IDs.
- Releasing a hold owned by a different actor requires `--force`; the refusal
  names the holder and its `since`.
- Re-holding as the same actor is an idempotent refresh: `Since` is preserved,
  `Reason` and `ExpiresAt` are updated.

If the daemon is not running, both commands exit non-zero with
`daemon is not running`. A workspace with no daemon has no claimers, so a
deploy script may legitimately treat that as "already quiesced".

## Loudness

- While held, at most one INFO line every 5 minutes:
  `claims held actor=… reason=… since=… expires=… gated_agents=N running=M`.
- A hold older than 2h renders with an escalated marker in `loom daemon status`
  (`WARN HELD 2h14m — forgotten?`), as does an indefinite one
  (`expires never (no backstop)`).
- An expired hold logs one WARN, clears the file, and agents resume on their
  next 15s re-check.

## On disk

`.loom/claim-hold.json`, mode `0600`, written atomically (temp + rename), and
— unlike `daemon.pid` and `daemon-agents.json` — **not** removed on shutdown:
surviving a daemon restart is the entire point.

```json
{"held":true,"actor":"union-autodeploy","reason":"deploy union tips",
 "since":"2026-08-19T00:41:00Z","expires_at":"2026-08-19T01:41:00Z"}
```

The file is **authoritative**, not just a startup seed. Every pre-flight gate
goes through `ClaimHoldSnapshot`, which re-reads `claim-hold.json` at most once
every **3 seconds** and adopts whatever it finds: a foreign write replaces the
in-memory hold, and deleting the file releases it. So

```sh
rm .loom/claim-hold.json
```

lifts the gate within roughly 15s (a 3s reload plus the agent's 15s re-check)
on a daemon that is already running, and the daemon logs one line —
`claim hold reloaded from disk`. That is the release path for a process that
cannot reach the control socket.

The daemon's **own** writes are not treated as external changes: the store
records the `(mtime, size)` of everything it writes, so only someone else's
write (or an `rm`) triggers adoption. There is no watcher goroutine and no
fsnotify — the reload rides the pre-flight path that was already reading the
hold. Last writer wins.

Adoption never resurrects an expired record, and it never re-persists: the
value came from the file in the first place.

Failure modes:

- **Corrupt / unparsable file** → fail safe but bounded: treated as held by
  actor `unknown` with a synthetic **15-minute** expiry, logged at ERROR. Never
  silently ignored, never held forever.
- **Persist failure** (read-only FS, full disk) → the hold IS applied in
  memory and the operation reports `success:false`, so the operator learns it
  will not survive a restart rather than silently losing it.
- **Reload failure** (stat or parse error) → **fails closed**: the in-memory
  hold is kept and one WARN is logged. A hold is never dropped because the
  filesystem hiccuped.
- **Expiry uses the local wall clock**, so a machine sleeping past the expiry
  releases early. Accepted; the 5-minute log line makes it observable.

## Control socket

```
-> {"operation":"claims_hold_set","args":{"held":true,"actor":"union-autodeploy",
     "reason":"deploy union tips","ttl_seconds":3600,"force":false}}
<- {"success":true,"data":{"hold":{...},"running":[{"agent":"coder","task_id":"PUPPET-77",
     "pid":41233,"started_at":"..."}],"gated":6}}

-> {"operation":"claims_hold_get"}
<- {"success":true,"data":{"hold":{...}|null,"running":[...],"gated":0}}
```

`held:false` releases. `running` lists agents with a live process — what
`--wait-idle` polls; `gated` counts agents whose last transition was a hold
gate. The state file mirrors both as `claim_hold` and per-agent
`claims_gated`.

## Deploy recipe

```sh
trap 'loom daemon release --actor union-autodeploy' EXIT
loom daemon hold --actor union-autodeploy --reason "deploy $SHA" --ttl 45m --wait-idle --timeout 30m
# ... deploy ...
```

The TTL is the backstop if the script dies before its trap runs.

## Known gap

`internal/cli/automode` is a separate legacy claim loop. This hold does **not**
gate it.
