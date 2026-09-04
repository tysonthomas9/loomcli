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

### Repo scope

Within that workspace a hold covers **every repo by default**. `--repos` (wire
field `repos`) narrows it to the named repos; an empty or absent `repos` is
workspace-wide, which is exactly what every record written before the field
existed already means, so no migration exists or is needed.

Scope by **blast radius, not repo identity**. A deploy that restarts the
supervisors every agent runs under, or the issue backend every agent claims
through, stops the whole fleet however few repos it names, and must stay
unscoped. `--repos` is for a deploy whose blast radius really is that repo, and
for an operator quiescing one repo's branch.

#### Two-level gate

The gate runs **before** the ready query — that ordering is the feature — so at
gate time there is no candidate and therefore no `source_repo` to compare
against. Agent repo *binding* is the only repo fact available that early, and
most fleets bind nothing. So a repo scope is applied in two places:

- **Level 1 — the agent gate.** An **unscoped** hold gates exactly as it always
  has: no ready query, no `ClaimIssue`, no recovery, no session. A **scoped**
  hold also gates outright when the agent is *statically* bound and every repo
  it is bound to is held. Otherwise the agent proceeds, carrying the held set
  for Level 2.
- **Level 2 — the candidate filter.** Held repos are dropped from the ready
  results **before** task selection. This is a hard pre-filter, not a scoring
  nudge: repo affinity in routing is soft (a mismatch still scores 5 and is
  still selectable), so a scope can never be expressed there.

The held set is read **once per cycle**, at the gate. Re-reading it in the claim
path would run the snapshot's throttled disk reload and one-shot expiry side
effects a second time, and the gate and the filter could then disagree across an
expiry boundary.

#### A scoped hold is not safe while fleet-db is down

Level 2 necessarily costs **one ready query per agent cycle**. Only an
**unscoped** hold keeps the zero-backend-call invariant, and only an unscoped
hold is therefore usable while fleet-db itself is being redeployed — which is
the case the hold was built for. `loom daemon status` prints this caveat under a
scoped hold.

#### Rules that follow

- **An issue with an empty `source_repo` is never held.** It names no repo, so
  it cannot be matched against a repo-named hold, and guessing would gate work
  the hold never claimed to cover.
- **Resume is exempt.** `claimResumeTask` re-acquires the agent's *own*
  interrupted task; that is not starting new work, and the hold's whole promise
  is to leave in-flight work alone. A **requested** task is new work and is
  filtered like the queue.
- **Emptied by the hold is not an empty board.** When candidates existed before
  the filter and none after, the agent reports `ClaimsHeld`, not `NoWork`, so
  `gated_agents` and the status banner still read "held".
- **Unknown repo names are rejected**, not ignored: a typo'd `--repos flet-db`
  would otherwise hold nothing while reporting a quiesce. Names are trimmed,
  de-duplicated and sorted before they are persisted.
- **The fail-safe hold is unscoped.** A corrupt `claim-hold.json` may have named
  repos that can no longer be read, so the synthesized hold is the strictest one
  available.
- **`running` and `--wait-idle` follow the scope.** A workspace-wide wait under
  a scoped hold could never reach idle, because agents on un-held repos keep
  claiming. An agent whose repo cannot be determined — a repo-unbound agent —
  is counted as running, fail-safe. On a repo-unbound fleet that degenerates to
  the fleet-wide wait; that is correct, not a bug.

## Commands

```
loom daemon hold --reason "..." [--ttl 60m] [--actor NAME] [--repos a,b] [--wait-idle] [--timeout 30m] [--force]
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
- `--repos` limits the hold to the named repos; omit it for a workspace-wide
  hold. See **Repo scope** above, including why it is unsafe while fleet-db
  itself is down.
- Re-holding as the same actor is an idempotent refresh: `Since` is preserved,
  while `Reason`, `ExpiresAt` **and `Repos`** are updated — so re-holding with a
  different scope narrows or widens the hold rather than being ignored.

## No daemon, or a daemon that is still starting

A hold outlives the daemon by design, so **`release` does not require one**.
With no daemon process alive, `release` clears `.loom/claim-hold.json` directly
and exits 0 — and exits 0 just the same when there is no file, so a deploy
script's `trap ... EXIT` is idempotent. `hold` has no offline path: a hold
written to a file no daemon will read is a quiesce that silently did not
happen, so it still exits non-zero when nothing is running.

The offline release enforces the **same ownership rule** as the socket path,
with the same wording — a foreign holder needs `--force`, and the refusal names
the holder and its `since`. A record that cannot be parsed is releasable with
`--force` only, and says so. A record that cannot be read at all (it is `0600`;
the release is running as another UID) reports the path and the owning UID
rather than claiming the daemon is not running.

A daemon that IS running may still be unreachable for a few seconds: it writes
`daemon.pid` before `cmdstore.OpenStore` and the supervisor start, and only
binds `daemon.sock` afterwards — measured at **9 seconds** apart. Both `hold`
and `release` therefore wait up to **30s** for the socket, polling every 500ms,
and print one line to stderr while they do:

```
Waiting up to 30s for the daemon control socket at /…/.loom/daemon.sock (daemon PID 4711 is starting)...
```

The wait ends early when the socket answers, or when the daemon PID stops being
alive. It is never entered when no daemon process is alive at all — that case
takes the offline path immediately instead of stalling for 30s. If the wait
times out against a live PID, the error names both the PID and the socket path
and states that the hold file was **deliberately not cleared**: that daemon may
already be holding a hydrated copy, so clearing the file would not release it.

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
