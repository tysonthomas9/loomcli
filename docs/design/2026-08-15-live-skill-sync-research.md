# Live skill sync research: making skill edits visible to all agents immediately

Status: research notes (feat/skills-crud-v1 worktrees; loomcli on v5, fleet-db
on main). Follows the `docs/design/` date-prefixed convention like
`2026-08-13-skills-crud-research.md`.

Question under research (Tyson, 2026-08-14): newly installed/edited Skills
should show up IMMEDIATELY in ALL agents in the loom system, instead of the
current snapshot-at-session-start semantics. This doc pins down (1) why the
current behavior is what it is, (2) what infrastructure exists to change it,
(3) whether a running backend session would even see a live file sync, and
(4) concrete design options. **No option is selected here — that is a
grilling decision.**

All file paths are repo-relative to either
`/Users/tyson/codebase/code-agents/loom-aug/.worktrees/skills-crud/loomcli` or
`…/.worktrees/skills-crud/fleet-db` (marked "fleet-db:"). Claims are marked
VERIFIED (read in source / official doc fetched 2026-08-14) or INFERRED.

---

## 1. Summary

- Skill materialization (`skillmat.Materialize`) runs at exactly three
  moments: worker spawn, `loom lead` start, and webui terminal-tab creation.
  Nothing re-runs it while an agent session is alive, and idle workers skip
  it too because materialization sits *behind* the task-claim gate (§2).
- The snapshot semantics are a **locked decision** (#6 in
  `00-grilling-decisions.md`, restated in fleet-db ADR-005 decision 3):
  "spawns get current content; mid-run agents keep their materialized copy."
  A separate security review blocker (B2) hardened the codebase specifically
  to *never* mutate a live agent's worktree, with a regression test (§4).
- Both major backends now document **live mid-session pickup** of skill
  changes: Claude Code watches skill directories ("picks up the change within
  the current session, without a restart"), and Codex's current docs say
  "Codex detects skill changes automatically." So live file sync *would* be
  visible to a running session — with a symlink-watching caveat for Claude
  Code that needs empirical testing (§5).
- Plenty of plumbing exists to carry a "skills changed" signal: fleet-db
  already emits `skill.create/update/delete` events onto a per-workspace SSE
  stream; the daemon already polls fleet-db every 1s (agent commands) and
  every 30s (config reconciler); the lead process already runs a 30s
  heartbeat loop and a 2s inbox-drain loop that could piggyback a
  re-materialize (§6). What does **not** exist: any Go-side SSE consumer of
  fleet-db events in loomcli, and any per-target lock making concurrent
  `Materialize` calls safe (§4.3).
- Five design options are laid out in §8 with per-option latency, effort,
  and which locked decisions each one reopens.

---

## 2. Finding 1 — Why idle worker restarts skip materialization

VERIFIED, full trace. Empirical observation to explain: workers re-materialize
only when they pick up actual work; the 30s idle "restart" cycles do not.

The per-agent supervise loop is `Supervisor.superviseAgent`
(`internal/cli/daemon/supervisor/supervisor.go:258-334`). Each iteration:

1. `preFlightSetup(ap)` (supervisor.go:293, impl at 405-443) runs **before**
   any spawn. Its last gate is `s.claimTask(ap, epicID)`
   (supervisor.go:438-440): if no task is claimed, `preFlightSetup` returns
   false.
2. `claimTask` (`internal/cli/daemon/supervisor/claim.go:43-92`) queries the
   ready queue; with nothing claimable it records a NoWork preflight error —
   `setPreflightError(... NoWorkOutcome, "no claimable tasks")`
   (claim.go:90-91) — and returns false.
3. Back in `superviseAgent`, the `!preFlightSetup` branch
   (supervisor.go:293-304) goes to `shouldRestart` → `applyNoWorkRestart`
   (`restart.go:76-79`, impl 234-242) → `sleepBeforeRestart`
   (supervisor.go:853-891; this is the `"waiting before restart"` log line at
   supervisor.go:860). `computeBackoff` returns the fixed NoWork poll
   interval (`restart.go:295-297`; default 30s via `getNoWorkBackoff`,
   restart.go:429-435) → `continue`.
4. `spawnAndWait` (supervisor.go:306) — and therefore `spawnAgent` → the
   `materializeSkills` call at `internal/cli/daemon/supervisor/spawn.go:261`
   (impl spawn.go:312-324, `skillmat.Materialize` under
   `controlPlaneOperationTimeout = 2s`, supervisor.go:231) — **is only
   reached when `preFlightSetup` succeeded, i.e. after a successful task
   claim.**

So the exact reason: **materialization lives inside `spawnAgent`, and the
NoWork path exits the loop iteration at the claim gate, before `spawnAgent`
is ever called.** The idle cycle spawns no process and touches no files.

Nuance worth stating: for workers this staleness is *cosmetic between runs* —
every actual run does materialize fresh content at spawn (spawn.go:261). The
worker-side gap is (a) the on-disk worktree looks stale while idle, and
(b) a fleet-db outage at spawn degrades to the stale copy
(`StoreUnavailableError` → warn and continue, spawn.go:262-269;
classification in `internal/skillmat/materialize.go:165-177`). The gap that
actually persists for a whole session is the **lead** (§3) and a live webui
terminal tab.

## 3. Finding 2 — The other two call sites and their once-only nature

VERIFIED.

- **Lead**: `runLead` calls `materializeLeadSkillsAtStart` once at
  `internal/cli/agent/lead/lead.go:118` (impl 290-321; role resolution via
  the registered Agent's `RoleName`, defaulting to `"lead"`, 303-310), then
  blocks for the whole session in `backends.RunControlledLeadRuntime`
  (lead.go:138-147) or `cli.InvokeAgent` (lead.go:149). A lead session that
  runs for days keeps its start-time snapshot.
- **Webui terminal**: `materializeInteractiveSkills`
  (`internal/webui/handlers/terminal/agent_session.go:142-155`) runs at tab
  creation, gated `roleKind == domain.RoleKindInteractive && !livePTYPresent`
  (agent_session.go:120-124). The gate is deliberate — see §4.2. Target dir
  is `agentLaunchCwd` → `localworkspace.RememberedAgentWorktree`
  (agent_session.go:361-370).

## 4. Finding 3 — Safety constraints on mutating files under a live agent

### 4.1 The locked decision (exact wording)

`/Users/tyson/codebase/code-agents/loom-aug/.scratch/skills-crud/issues/00-grilling-decisions.md`,
decision #6 (VERIFIED, quoted in full):

> 6. **Mutable + event history.** One record; spawns get current content;
>    mid-run agents keep their materialized copy; rollback via the event log.
>    — Rejected: immutable versions with pinning and draft→published
>    lifecycle.

Restated in fleet-db `docs/adr/ADR-005-skills-entity.md` decision 3:
"Mutable records, no version pinning. One record per skill; agents get
whatever is current at spawn; mid-run agents keep their materialized copy."
The ADR's stated positive: "spawn-time-current semantics match how roles
already behave." Its expiry clause: "Re-evaluate the no-versioning and
dual-naming decisions after one quarter of real multi-agent skill usage."

Ticket 03 (`.scratch/skills-crud/issues/03-runtime-materialization.md:26`)
turned the decision into an implementation rule: "**Mid-run agents are never
touched.**"

### 4.2 The B2 security blocker enforces it in code

The 2026-08-14 codex adversarial review of the materializer
(03-runtime-materialization.md:94-101) flagged as a BLOCKER that the
interactive path could mutate a LIVE agent's worktree ("the old agent runs
while its skills are deleted and rewritten"), explicitly "violating decision
#8." The fix (03-runtime-materialization.md:130-132) is the
`!livePTYPresent` gate at agent_session.go:120-124, guarded by test
`TestEnsureAgentTerminalSessionSkipsSkillRewriteWhenStaleTabPTYIsLive`.
Any live-sync feature deliberately reintroduces the behavior this blocker
removed, so it must re-argue that safety case, not just delete the gate.

### 4.3 What `skillmat.Materialize` actually guarantees (and doesn't)

VERIFIED from `internal/skillmat/materialize.go`:

- **Idempotent fast path**: marker hash match + recorded-path equality +
  on-disk shape re-verification → no-op (materialize.go:128-143,
  `projectionMatches` 502-525). Cost of a redundant call: one
  `Skills().List` + a stat pass.
- **Not atomic per file or per tree**: on any change, `cleanupPrevious`
  deletes *every* previously recorded path first (materialize.go:151,
  375-396), then `writeProjection` recreates the tree (154, 398-449). Files
  are created in place with `O_CREAT|O_EXCL|O_NOFOLLOW`
  (`securefs_unix.go:118-124`) — no temp+rename. Only the **marker** is
  written atomically via temp file + rename (`writeMarkerAtomically`,
  materialize.go:451-469). So a concurrent reader can observe: a window with
  SKILL.md missing, a partially rewritten tree, or a dangling
  `.claude/skills/<name>` symlink (the symlink entries are also
  deleted-then-recreated; link target `../../.agents/skills/<name>`,
  materialize.go:222-228).
- **Crash mid-rewrite self-heals**: a partial run's exact desired files are
  adopted on the next call (`entryExactlyMatches` skip, materialize.go:415-421;
  review round 2 notes in 03-runtime-materialization.md:133-139).
- **No locks of any kind**: there is no per-skill or per-target write lock in
  the package (VERIFIED by reading the whole file). Today that is safe
  because every target has exactly one materializing actor (daemon
  serializes per agent inside `superviseAgent`; lead materializes once;
  webui is gated off live PTYs, and worker vs interactive target sets are
  disjoint by role kind). **Any fanout design that adds a second writer
  (e.g. a syncer racing a spawn) needs to add per-target serialization**
  (e.g. an flock next to the marker) or two concurrent reconciles can
  interleave cleanup/write and trip the collision detector.
- **A running process with SKILL.md open**: on Unix, unlinking a file a
  process has open leaves that fd readable (POSIX semantics — INFERRED,
  standard behavior, not loom-specific). The real hazard is not corruption
  but *instruction drift*: Claude Code re-reads SKILL.md content at
  invocation time (progressive disclosure, §5), so a mid-task reconcile
  changes what the agent's next skill invocation says — precisely what
  decision #6's "mid-run agents keep their materialized copy" exists to
  prevent.
- **Git status noise: none.** `ensureGitExcludes` appends
  `.agents/skills/` and `.claude/skills/` to the target's
  `.git/info/exclude` on every materialization (materialize.go:640-696,
  wanted entries at 677), and no-ops outside a git work tree
  (materialize.go:641-650). A mid-task reconcile would not dirty a worker's
  `git status`. VERIFIED.
- **Zero-skill runs still create both directories**: `writeProjection`
  unconditionally `MkdirAll`s `.agents/skills` and `.claude/skills`
  (materialize.go:399-403) even with an empty projection. This matters for
  §5: the watched directory always exists before the backend starts.

## 5. Finding 4 — Backend discovery timing (do live file changes even help?)

This determines whether syncing files under a RUNNING session accomplishes
anything.

### 5.1 Claude Code: live change detection is official and documented

Source: https://code.claude.com/docs/en/skills (fetched 2026-08-14).
VERIFIED, quoted verbatim from the "Live change detection" section:

> Claude Code watches skill directories for file changes. When you add,
> edit, or remove a skill under `~/.claude/skills/`, the project
> `.claude/skills/`, or a `.claude/skills/` inside an `--add-dir` directory,
> Claude Code picks up the change within the current session, without a
> restart. If you create a top-level skills directory that didn't exist when
> the session started, restart Claude Code so it can watch the new
> directory.

And its scope note: "Live change detection covers `SKILL.md` text only."
Discovery locations ("Where skills live" / "Discovery from parent and nested
directories"): project skills load from `.claude/skills/` in the starting
directory and every parent up to the repo root; nested `.claude/skills/`
below cwd load lazily when Claude touches files there.

Consequences for loom (part VERIFIED, part INFERRED):

- The "directory didn't exist at session start" caveat is already neutralized
  by loom: materialization always creates `.claude/skills/` pre-spawn, even
  for zero skills (§4.3), so the watch target exists.
- Symlinks are supported as skill entries: "A `<skill-name>` entry in the
  enterprise, personal, or project locations can be a symlink to a directory
  elsewhere on disk. Claude Code follows the symlink and reads `SKILL.md`
  from the target directory" (same page, VERIFIED quote).
- **Open question the docs do not answer**: whether the *file watcher*
  resolves through the symlink, i.e. whether editing
  `.agents/skills/<name>/SKILL.md` (the real file) fires a change event for
  the watched `.claude/skills/` tree. INFERRED risk: OS watchers
  (FSEvents/inotify) do not watch through symlinks unless the implementation
  resolves them. Mitigating fact: loom's reconcile deletes and recreates the
  `.claude/skills/<name>` symlink itself on every projection change
  (§4.3), which *is* a directory-entry event in the watched location — so
  pickup is plausible even with a naive watcher, but this is INFERRED and
  needs one empirical test before any design relies on it.

### 5.2 Codex: current official docs claim automatic change detection

Source: https://developers.openai.com/codex/skills.md → 308 redirect →
https://learn.chatgpt.com/docs/build-skills.md (fetched 2026-08-14).
VERIFIED quotes:

> "Codex detects skill changes automatically. If an update doesn't appear,
> restart Codex."

> "Codex scans `.agents/skills` in every directory from your current working
> directory up to the repository root."

(plus user scope `$HOME/.agents/skills`, admin scope `/etc/codex/skills`,
and OpenAI-bundled system skills.)

**This contradicts the working assumption that codex "does not auto-discover
at all."** Either that assumption predates a codex release, or it reflects
observed behavior diverging from docs. The doc is the primary source and it
claims mid-session detection (with restart as the fallback); the empirical
claim should be re-tested against the codex version the fleet actually runs
before a design bakes in "codex needs a restart." Note codex reads
`.agents/skills/` directly (no symlink indirection — loom's canonical dir),
so the §5.1 symlink-watch caveat does not apply to codex. The
`.agents/skills` convention itself is cross-agent (Codex, Cursor, OpenCode,
Amp, Gemini CLI, Copilot read it natively —
`docs/design/2026-08-13-vercel-skills-research.md:85,140-145`).

Net: **live file sync would plausibly reach RUNNING sessions on both major
backends** — the opposite of what the snapshot design assumed it could rely
on. The remaining protection for mid-run stability is loom *choosing* not to
rewrite the files, not the backends ignoring rewrites.

## 6. Finding 5 — Existing push/notify/poll infrastructure inventory

Everything below is VERIFIED against source. For each: could it carry a
workspace-scoped "skills changed" signal to (a) the daemon, (b) running lead
sessions?

1. **fleet-db skill events + SSE stream — the natural signal source.**
   Every skill write is event-sourced: `SkillService.executeSkillCommand`
   appends a `models.Event` (fleet-db
   `internal/service/skill_service.go:129-134`) with
   `EntityType: models.EntitySkill` (lines 217, 345, 468) and actions
   `skill.create/update/delete` (+ `skill_pack.*`) (fleet-db
   `internal/models/event.go:82-89`; `EntitySkill` at 165). These flow onto
   the per-workspace SSE endpoint
   `GET /api/v1/{workspace}/events/stream` (fleet-db
   `internal/api/stream.go:40`; every mutation event has SSE type
   `"mutation"`, stream.go:26) which supports `entity_type=skill` filtering
   (`internal/api/event_filter.go:19-30`) and Last-Event-ID resume. Fan-out
   is multiplexed by `EventHub` over `RedisEventStore.Subscribe` (fleet-db
   `internal/storage/event_hub.go:10-12,143`;
   `internal/storage/redis_eventstore.go:156-157`).
   → (a)/(b): the signal **already exists server-side**; what's missing is
   any Go-side consumer in loomcli. Grep found no loomcli Go code fetching
   `/events/stream` — the only SSE consumers are the webui *frontend*
   (`internal/webui/frontend/src/api/common/sse.ts`) against loomcli's own
   realtime hub, not fleet-db's stream.

2. **Redis in the local-mode stack — fleet-db's storage, not a loom bus.**
   `test/local-mode/docker-compose.yml:25-35` runs `redis:7-alpine`;
   fleet-db is launched with `--redis-addr redis:6379` (lines 52-53). Redis
   is fleet-db's primary store and its event-stream substrate (XREAD-based
   Subscribe above, plus `internal/storage/redis_pubsub.go` durable topics).
   loomcli never talks to Redis directly — only HTTP to fleet-db. → Not a
   channel loom can use except *through* fleet-db APIs.

3. **Daemon agent-command poller — an existing 1s command fan-in.**
   `Daemon.startAgentCommandPoller` ticks every second and lists
   `AgentCommands()` from fleet-db
   (`internal/cli/daemon/daemon_command_poller.go:18-36`), acking and
   dispatching typed commands `start|stop|restart|yield` per target agent
   (daemon_command_poller.go:83-121; `domain.AgentCommand` in
   `internal/domain/control_plane.go:198-218`, with `TargetNodeID`
   routing). → (a): a `sync_skills` command type slots in here with ~1s
   latency and node targeting for free. → (b): no — leads don't poll agent
   commands.

4. **Daemon config reconciler — an existing 30s convergence loop.**
   `configPollingLoop` polls fleet-db-backed daemon config every 30s and
   diffs by hash (`internal/cli/daemon/daemon_reconciler.go:22-60`). → (a):
   the shape to copy (or extend) for a "skills projection hash changed →
   re-materialize idle worktrees" sweep. → (b): daemon-only.

5. **Lead heartbeat loop — an existing 30s tick inside the lead process.**
   `heartbeatLeadSession` ticks every `leadHeartbeatInterval = 30s`
   (`internal/cli/agent/lead/lead.go:36,450-466`) with a live store handle.
   → (b): the cheapest place to re-run `Materialize` for the lead's workDir
   (hash no-op makes redundant ticks cheap, §4.3).

6. **Lead inbox drain loop — an existing 2s tick inside the lead runtime.**
   `drainLeadMessageQueue` ticks every `leadMessageDrainInterval = 2s`
   (`internal/leadcontrol/delivery.go:53,376-404`), started by both
   controlled runtimes (`codex_runtime.go:76`, `harness_runtime.go:137`).
   It delivers queued `AgentInboxMessages` into the live session
   (per-provider `leadTurnDeliverer`, delivery.go:62-80). → (b): two uses —
   piggyback a re-materialize on the tick, or deliver an actual *message*
   ("skills updated: X") into the live conversation so the agent knows,
   which file sync alone cannot do.

7. **loomcli local events (`events-*.jsonl`) — one-way local sink.**
   `events.Bus` writes daemon lifecycle events to
   `<events_dir>/events-YYYY-MM-DD.jsonl` (`internal/events/emitter.go`;
   filename at `internal/events/jsonl_writer.go:155`); emitted from e.g.
   `spawnAgent` (spawn.go:304). Consumers: metrics/replay/otelexport. → Not
   a delivery channel to agents; could *record* sync events for
   observability only.

8. **notify.token — wrong direction.** It authenticates *agent →
   webui* session-status notifications
   (`internal/cli/backends/backend_session_env.go:134-146`; token written by
   webui `internal/webui/app/server_app.go:426`; used in
   `internal/cli/automode/automode_task.go:31,63`). → Not usable for
   webui/daemon → agent signaling.

9. **loomcli webui realtime hub — serve → browser only.**
   `internal/webui/server/realtime/handler.go` + `hub.go:266-268
   (Broadcast)` push loom-serve mutation events to the browser UI. → Useful
   for a "sync now" button's UX feedback; not a channel to agents.

10. **serve-side sweeper loops** (`internal/cli/serve/serve_loops.go:1-8` —
    stale-task, outbox, cron, delivery-retry, await-timeout; all 2s
    RunOnce-per-tick). → Precedent shape for a serve-side sweeper, but serve
    doesn't know worktree paths on other nodes; less apt than 3/4.

## 7. Finding 6 — Registry of live materialization targets

How would a syncer enumerate "every target dir"? What exists (VERIFIED):

- **Workers**: in-memory only. `Supervisor.NewAgent` resolves
  `ap.WorktreePath` via `workspace.ResolveAgentTarget`
  (`internal/cli/daemon/supervisor/supervisor.go:123-145`); exposed in
  daemon status (`internal/cli/daemon/daemon_state.go:140`). Not persisted
  to fleet-db. A daemon-resident syncer has the full list for free; an
  external syncer does not.
- **Interactive agents (webui terminals)**: the per-user, per-machine state
  cache `~/.loom/state.json` maps (workspace, agent) → worktree:
  `RememberAgentWorktree` / `RememberedAgentWorktree`
  (`internal/localworkspace/localworkspace.go:547-580`;
  `internal/bootstrap/statecache.go:19`, path at
  `internal/bootstrap/paths.go:61`). Writers:
  `internal/cli/agentdef/agentdef_cmd.go:274`,
  `internal/webui/svcimpl/agent_service.go:434`,
  `internal/webui/handlers/prreview/reviewer.go:317`. `RememberedAgentWorktree`
  validates the dir still exists with a `.git` entry (localworkspace.go:573-578).
- **Leads**: fleet-db `AgentSession` rows with `Kind: orchestration` and
  `metadata["lead_workdir"]` (`createLeadSession`,
  `internal/cli/agent/lead/lead.go:371-390`), kept fresh by the 30s
  heartbeat (lead.go:450-466) and marked completed on exit (lead.go:414-431).
  So "running lead sessions + their workdirs" is queryable from fleet-db —
  with the caveat that `lead_workdir` is a path on the lead's *host*; only a
  same-host process can act on it. Live webui tabs also persist
  `Launch.Cwd` in tabmeta (agent_session.go:355-357).

There is no single registry; a fan-out design composes: daemon (its own
workers) + lead processes (their own workDir) + optionally a host-local
sweep over `state.json` for remembered interactive worktrees.

---

## 8. Design options

Common facts used below: `Materialize` is idempotent and cheap when clean
(§4.3); the signal source exists (§6.1); running Claude/codex sessions
plausibly see file changes (§5, with the symlink-watch caveat); mutating a
target that might have a live process requires reopening decision #6/#8 and
the B2 gate (§4.2), plus adding per-target write serialization (§4.3).

### Option A — Re-materialize on every idle worker poll cycle

**What/where**: run `s.materializeSkills(ap)` on the NoWork path too — e.g.
at the top of `preFlightSetup` (supervisor.go:405, before the claim gate) or
in the `!preFlightSetup` branch (supervisor.go:293-304). ~5 lines plus
tests; reuses the existing 2s-timeout wrapper (spawn.go:312-324).
**Latency**: ≤ `no_work_backoff` (30s default, restart.go:429-435).
**Safety**: does NOT touch live agents — the NoWork cycle by definition has
no running child process, so decision #6/#8 are untouched.
**Reach**: workers' on-disk worktrees converge while idle; running sessions
unaffected (there are none on this path). Does nothing for lead/webui — and
note the worker gap is mostly cosmetic anyway, since every spawn already
materializes fresh (§2). Adds one `Skills().List` per agent per 30s of
fleet-db load.
**Effort**: XS. **Reopens**: nothing.

### Option B — Event-driven fanout (fleet-db skill events → daemon + leads)

**What/where**: add a Go SSE client for
`GET /api/v1/{ws}/events/stream?entity_type=skill` (new code in
`internal/infra/fleetdb`, none exists today — §6.1). Daemon subscribes and
re-materializes worker worktrees on skill events, skipping agents with a
live process (respecting #6) or including them (reopening #6). Lead
processes subscribe likewise (they hold a store handle, lead.go:281-286) and
re-materialize their own workDir.
**Latency**: sub-second push, plus SSE reconnect/backoff machinery.
**Safety**: for running targets this is exactly the mutation the B2 blocker
removed; needs the per-target lock (§4.3) because a push can race a spawn's
own materialization; needs the "SKILL.md briefly absent" window accepted or
fixed (per-file temp+rename or per-skill staging).
**Reach**: full — including RUNNING lead sessions, which per §5 would
actually see the change (verify the symlink-watch caveat first for Claude).
**Effort**: L (new SSE client, two runtimes wired, locking, degraded-mode
story when the stream drops). **Reopens**: decision #6 (mid-run clause),
ticket-03's "mid-run agents are never touched", the B2 rationale.

### Option C — Materialize-on-read / pre-turn hook in agent runtimes

**What/where**: workers need nothing (each run is one-shot; spawn already
materializes — §2). For the lead, two sub-variants:
(C1) hook the controlled runtime's turn injection: run `Materialize` before
`deliverTurn` in `internal/leadcontrol/delivery.go` — but that only covers
*injected* inbox turns, not the user's own PTY keystrokes, so coverage is
partial; (C2) use the backend's own hook system (e.g. Claude Code
`UserPromptSubmit` hook invoking `loom skill materialize <dir>`), which
covers every turn but means loom starts managing backend hook config it does
not manage today, per-backend.
**Latency**: exactly one turn — the freshest possible semantics ("every turn
sees current skills") and mutation happens at a turn boundary, the least
dangerous instant.
**Safety**: still mutates a live session's files, but never mid-turn;
decision #6 is still reopened, in its most defensible form.
**Reach**: leads (and any hooked interactive backend). **Effort**: M
(C1) / M-L and per-backend (C2). **Reopens**: decision #6 mid-run clause;
C2 adds a new "loom manages backend settings" surface never decided on.

### Option D — Explicit "sync now" (CLI + UI), no semantic change

**What/where**: a human-invoked fan-out: (1) new CLI verb (e.g.
`loom skill push` — note `loom skill sync` is taken for pack→DB sync,
`internal/cli/skill/pack_cmd.go:72-74`); (2) daemon leg via a new
`sync_skills` `AgentCommand` consumed by the existing 1s poller (§6.3);
(3) lead leg via the lead's own loop (Option E2) or an inbox message; (4)
host-local leg sweeping `RememberedAgentWorktree` entries (§7); (5) a webui
button reusing the same path, with feedback over the realtime hub (§6.9).
**Latency**: ~1-2s after the click, but only when a human asks.
**Safety**: still technically mutates live worktrees, but as a deliberate
human action — analogous to "rollback via the event log" being manual;
easiest to argue as *not* reopening #6 ("mid-run agents keep their copy
*unless the operator pushes*"). The B2 gate would need an explicit
carve-out for the operator path, and the per-target lock is still required.
**Reach**: everything registered; running sessions see it per §5.
**Effort**: M. **Reopens**: decision #6 only via an explicit,
operator-initiated exception; decision #14's "materialized copies hidden"
UI stance is untouched.

### Option E — Piggyback the loops that already exist (cheapest convergence)

Found in code, no new transport:
- **E1 (workers)**: extend the daemon config reconciler's 30s hash loop
  (§6.4) — or simply Option A, which is the same effect with less plumbing.
- **E2 (leads)**: call `materializeLeadSkillsWith` from the lead heartbeat
  tick (lead.go:450-466) or the 2s inbox drain tick (§6.6). The hash
  fast-path makes the steady-state cost one `Skills().List` per tick;
  choose the 30s heartbeat to keep fleet-db load negligible.
**Latency**: ≤30s everywhere (≤2s if the drain loop is chosen for leads).
**Safety**: E2 mutates a live lead's worktree on a timer — the bluntest
form of reopening decision #6 (no turn-boundary alignment, no operator
intent); the SKILL.md-absent window (§4.3) can coincide with a skill
invocation. Per-target locking still needed (heartbeat vs a webui
materialize on the same dir).
**Reach**: full for workers+leads. **Effort**: S. **Reopens**: decision #6
mid-run clause, ticket-03 rule, B2 rationale — same as B but without B's
infrastructure cost or push latency.

### Cross-option notes

- A + E2 (or A + C1) compose: A closes the worker gap for free; the real
  decision is only *how* the lead converges (timer, turn boundary, push, or
  human action).
- Any option that touches live targets should first empirically answer the
  §5.1 symlink-watch question; if Claude Code's watcher misses
  through-symlink edits, the reconcile's symlink recreate (§4.3) probably
  covers it, but if not, the materializer could switch `.claude/skills` from
  symlinks to real copies — which would reopen decision #7's layout choice.
- If mid-run updates become possible at all, the "broken workspace skill
  affects all next spawns" negative in ADR-005 becomes "affects all
  *running* agents within seconds" — the blast-radius argument that
  motivated version pinning (rejected in #6) gets strictly stronger.

---

## 9. Open questions for grilling

1. **Reopen decision #6's mid-run clause?** Every option except A/D-as-
   operator-action changes "mid-run agents keep their materialized copy."
   This is a locked decision (00-grilling-decisions.md #6, ADR-005 decision
   3) whose file says reopening requires a fresh grilling round. ADR-005
   already schedules a re-evaluation "after one quarter of real multi-agent
   skill usage."
2. **Reopen decision #8 / ticket-03's "mid-run agents are never touched"
   and the B2 blocker fix?** The `!livePTYPresent` gate
   (agent_session.go:120-124) and its regression test encode the old answer;
   a live-sync feature must consciously supersede the security-review
   rationale, not silently delete the gate.
3. **What is the actual product requirement — files current, or agent
   *aware*?** File sync makes the next skill invocation current (§5); it
   does not tell a mid-conversation agent its instructions changed. If
   awareness is wanted, the lead inbox (§6.6) is the channel — a different
   feature than sync.
4. **Convergence contract**: "immediately" = push (<1s, Option B), a bounded
   poll (≤30s, A/E), turn-boundary (C), or on-demand (D)? This choice picks
   the option.
5. **Per-target write locking** (new decision): required before any second
   materializing actor exists (§4.3). Marker-adjacent flock vs daemon-only
   ownership vs "only ever one writer per target by construction."
6. **Per-file atomicity** (new decision): is the delete-then-recreate window
   (§4.3) acceptable under a live agent, or must the materializer move to
   per-file temp+rename / per-skill staging first? (Touches the reviewed,
   DONE ticket-03 implementation.)
7. **Empirical backend verification** before committing: (i) does Claude
   Code's watcher pick up an edit made through
   `.agents/skills/<name>/SKILL.md` behind the `.claude/skills` symlink?
   (ii) does the fleet's pinned codex version match its docs' "detects skill
   changes automatically" claim (§5.2 contradicts the prior belief that
   codex never auto-discovers)? A "no" on (i) pressures decision #7's
   symlink layout (copies instead of links).
8. **Scope of targets**: workers + leads only, or also remembered
   interactive worktrees with no live session (`state.json` sweep, §7)?
   Cross-host leads are reachable only via their own process (lead_workdir
   is host-local).
9. **Naming/CLI surface** if Option D: `loom skill sync` is already the
   pack-sync verb (pack_cmd.go:72-74); a distinct verb is needed, and
   decision #12's "manual sync, cadence driven externally" wording gets a
   sibling for worktree push.
