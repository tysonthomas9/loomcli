# Defects found while planning lead conversation resume

> **Status:** Current · *audited 2026-08-03* — an open defect list, not a design.
> Entries stay until fixed or filed; companion to the decision log below.

**Found:** 2026-07-22
**Context:** surfaced while working the `LOOMCLI-169` wayfinder map. See
`docs/design/2026-07-22-lead-conversation-resume.md` for the decisions themselves.

None of these are decisions on the map's route, and none is owned by a map
ticket — they are pre-existing defects the planning work walked into. They are
recorded here because the planning tracker is a local fleet-db snapshot that is
neither committed nor backed up; this file is the git-durable copy until they are
filed on a real tracker.

Each entry states its **evidence class** per the `AGENTS.md` terminology
handshake: *deterministic* (source reading), *real* (local binary/filesystem, no
external service), *live* (real API calls).

---

## 1. A live worktree can be destroyed under a running agent, bricking it

**Severity: high — verified irrecoverable failure mode.** **Evidence: real** (git
probes on a throwaway repo).

`ensureReviewer` re-syncs the PR worktree on **every mount of the discussion
panel** — connector head read, fetch, `reset --hard`, `git clean -fdx`
(`internal/webui/handlers/prreview/reviewer.go:190-198`,
`internal/localworkspace/localworkspace.go:249-300`) — while
`ensureAgentTerminalSession` returns the existing tab early whenever the PTY is
alive (`internal/webui/handlers/terminal/agent_session.go:99-113`). The agent
keeps running in that directory throughout.

Observed by running the same operations on a throwaway repo:

- `git clean -fdx` deleted the agent's untracked **and gitignored** files — scratch
  notes, generated artifacts, ignored local state.
- `git worktree remove --force` **succeeded while a live process held the
  directory as its cwd**. The directory vanished; the process survived.
- Every subsequent command from that dangling cwd failed with
  `getcwd: cannot access parent directories`.

**Failure mode:** the agent is *bricked but alive*. The conversation is intact and
the PTY is still `PTYAlive`, so the early return hands that dead tab back forever
and nothing ever rebuilds it. No recovery short of the user closing the tab.

**Note:** `stale_subject`/HTTP 409 does **not** protect against this. It detects
only a race *inside a single ensure call* — the head SHA is read and then passed
as `expected` to the worktree sync moments later, so it matches unless the PR
moves in between.

**Suggested fix:** when a live agent tab exists for the reviewer,
`prepareReviewerCheckout` should skip the sync and report the state instead of
performing it.

---

## 2. A resumed codex lead is marked as having received an assignment it never got

**Severity: high — silent.** **Evidence: deterministic**, verified by reading all
three sites.

Three behaviours combine:

1. `markLeadAssignmentDelivered` writes `lead_assignment_delivered_version` during
   **prompt construction**, before the backend process launches
   (`internal/cli/agent/lead/lead.go:227,253`).
2. `DeliverCurrentAssignment` short-circuits to `Delivered` and returns **without
   delivering anything** when that version matches
   (`internal/leadcontrol/delivery.go:130-133`).
3. `codexTUIArgs` drops the prompt positional on a resume
   (`internal/leadcontrol/codex_runtime.go:198-199`) — deliberately, so the role
   prompt is not re-issued every restart.

So on a resumed codex lead the assignment rides a prompt that is never passed,
having already been marked delivered, and the confirmed-delivery path is then
permanently suppressed for that version.

**Related, same area:** `lead_assignment_acknowledged_version` is **read but never
written**. Its only reader is the monitor
(`internal/cli/serve/metricscmd/monitor_store_data_source.go:262`); the constant
`MetadataDeliveryAcknowledged` (`internal/leadcontrol/codex_metadata.go:28`) is
never used to write. The monitor has a permanently unreachable "acknowledged"
branch — it can render a state the system cannot enter.

---

## 3. `OrchestrationSessionFor` truncates by one ordering and selects by another

**Severity: medium — already at the threshold on at least one host.**
**Evidence: deterministic** + **real** (local snapshot inspection).

`internal/store/orchestration.go:30-33` lists sessions with `Limit: 8`, which both
store backends serve **ordered by CreatedAt DESC** before truncating. The
selection loop at `:46` then picks the best survivor by **UpdatedAt**.

A session created 9th but recently active is therefore invisible to the join: it
is evicted from the window before the selector ever sees it. On the host where
this was found, the `LOOMCLI` workspace's `lead` agent already had **exactly 8**
orchestration sessions.

---

## 4. `git` invocations leak past their context deadline and never report as timeouts

**Severity: medium.** **Evidence: real** (probe against a blackholed IP).

`exec.CommandContext` kills `git`, but `CombinedOutput` blocks on pipes that
`git-remote-https`/curl — a grandchild — still holds. Measured with a 1.2 s
context deadline:

```
elapsed              = 1m15.0s      ← ctx said 1.2s
Is(DeadlineExceeded) = false
ctx.Err()            = context deadline exceeded
```

Two consequences:

- `writePRReviewError`'s `context.DeadlineExceeded` → 504/retryable branch
  (`internal/webui/handlers/prreview/errors.go:53-54`) is **unreachable for git**;
- a synchronous check on the launch path can block a terminal-open handler far
  past its own deadline (`reviewerGitTimeout` is 60 s,
  `internal/webui/handlers/prreview/reviewer.go:35,215-220`).

**Fix:** set `cmd.WaitDelay`. The repo already does this in five places
(`internal/driver/task_bridge.go:384`, `internal/driver/bundled_runner.go:94`,
`internal/driver/sandbox/container.go:382`, `internal/driver/sandbox/launcher.go:245`,
`internal/cli/backends/backend_external.go:77`) — but **no git runner sets it**
(`localworkspace.go:431`, `internal/stackpublish/gitutil.go:18`,
`internal/driver/run.go:687`). Two git runners take no context at all
(`internal/cli/serve/opsimpl/repair_checkout.go:575`,
`internal/gitbranch/branch.go:234`).

---

## 5. A codex lead with a missing rollout is bricked, not cold-started (latent)

**Severity: medium — latent, not currently biting.** **Evidence: real** (codex-cli
0.145.0 on this host).

`priorCodexThreadID` returns the stored `codex_provider_thread_id` with **no
existence check** (`internal/leadcontrol/codex_runtime.go:211-233`), and
`codexTUIArgs` emits it as `resume <SESSION_ID>`. Verified in a real terminal:

```
$ codex resume 00000000-0000-4000-8000-000000000000
ERROR: No saved session found with ID 00000000-...
```

It exits straight back to the prompt — no picker, no dismissable error pane. A
non-zero exit propagates out of `RunCodexLeadRuntime`, and
`internal/cli/agent/lead/lead.go:143-147` prints "Error running agent" and drops
the lead into a shell.

**Why it is latent rather than live:** codex is not pruning. All 9 stored thread
ids on this host still have rollouts, against 3448 rollout files retained. The
triggers are therefore *not* time-based — a changed `CODEX_HOME`/`HOME` between
restarts, a purged or migrated codex home, or manual cleanup.

**Contrast with claude, which does have a clock on it:** claude's
`cleanupPeriodDays` sweep is running (oldest transcript on this host exactly 30
days old), and `claude --resume <unknown>` hard-fails identically. `LOOMCLI-183`
owns the uniform rule; both backends fail the same way for different reasons.

---

## 6. `CLAUDE_CONFIG_DIR` is honoured on launch but ignored when reading transcripts

**Severity: medium — breaks every containerised claude lead's chat view.**
**Evidence: deterministic.**

- `scripts/dev-container-start.sh:98` sets `CLAUDE_CONFIG_DIR=/root/.claude-rw`.
- loom's own claude path **honours** it
  (`internal/cli/backends/backend_claude.go:137-140`).
- harness-wrapper's transcript reader **hardcodes** `~/.claude/projects`.

So claude writes its transcript under one root and the reader looks under
another, producing exactly "the launch-pinned id names a file that will never
exist".

**This is probably the real cause of the symptom that "claude rotates its session
id" was invented to explain** (`internal/leadcontrol/harness_metadata.go:24-30`,
`internal/webui/handlers/prreview/harness_read.go:95-107`). `LOOMCLI-181` tested
rotation live on claude 2.1.218 — first boot, folder-trust dialog accepted — and
observed **no rotation**: the resume appended to the same `<pinned>.jsonl`. The
`newestClaudeSessionSince` mtime scan searches the same wrong root in the
container case, so it does not compensate either.

**Once the locator is fixed, the mtime-scan compensation should be revisited** —
it may be dead weight papering over this bug. See D6.

---

## Incidental

`docs/testing-terminology.md` — cited by `AGENTS.md:21` and by `LOOMCLI-181` —
**does not exist**. The evidence-class vocabulary (deterministic / real / live)
survives only in the AGENTS.md terminology-handshake paragraph.
