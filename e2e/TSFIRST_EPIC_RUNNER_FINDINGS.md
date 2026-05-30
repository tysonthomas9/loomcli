# TS-SDK Epic Runner E2E — Findings & Handoff

Status: **blocked on a design question** (see §5). The TypeScript SDK code works and
dispatches correctly; the daemon execution path needs pre-provisioned worktrees that
the `loom run` path does not create on its own.

## 1. Goal

Add a new e2e that drives an epic to completion using the **TypeScript SDK**
(`loom check` / `loom apply` / `loom run` against `.loom/*.ts`) instead of the
imperative `loom epic run`, in a podman container with real Codex auth — seeded
with the Slack-clone fixture (`scripts/fixtures/slack-src`).

## 2. What works (verified)

- **TS SDK definitions compile and are correct.** `loom check` compiles
  `.loom/agents/nova.ts` + `.loom/workflows/epic-runner.ts`; the workflow resolves to
  `runner: "workflow-context-v1"` with the right `tools` and `singleton_policy`.
  Verified on host Node 22 AND the container's Node 20 regex-fallback path.
- **The workflow dispatches correctly.** Every reconcile pass logs
  `ready=1 open=2 blocked=1 ensured=1` and creates the ephemeral task-worker agent
  (`taskRuns.ensure` -> `dispatchTaskRun` -> `AgentCommands().Create`). Dependency
  ordering is honored (task B stays blocked until A completes).
- **Both repos build clean** (`go build ./...`), including the uncommitted defs work.

## 3. Bugs found and fixed along the way (all in the e2e harness, not the SDK)

1. **fleet-db arch mismatch** — wrapper mounted a wrong-arch binary -> `exec format error`.
   Fixed: wrappers now build a Linux ELF for the image arch and hard-verify before running.
2. **Seed branch collision** — seeded the repo ON `$DEFAULT_BRANCH`, so
   `loom workspace create`'s `git worktree add -b $DEFAULT_BRANCH` failed
   ("branch already exists"). Fixed: seed on `main`, push target branch to the remote.
3. **Daemon started before any agent** — `loom daemon` exits immediately if the
   workspace has no agents. Fixed: `loom apply nova` first (fatal gate), then daemon.
4. **Per-process fleet-db** — each `loom run` spun its own embedded fleet-db and tore it
   down, so daemon + run passes did not share state. Fixed: one long-lived
   `loom serve --no-daemon` owns the embedded fleet-db; everything else reuses it via
   `$LOOM_CONFIG_DIR/fleet-db/runtime.json`. (Mirrors `test/local-mode/docker-compose.yml`.)
5. **Agent had no repo** — `defineAgent` without `repos` -> daemon can't derive a worktree.
   Partially addressed: added `repos: ['<repo>']` to nova. **Not sufficient — see §4.**

## 4. The actual blocker (root cause, traced to code)

Even with all of the above fixed, `task_a` never closes. Across 4 podman runs + 1 free
host run, the daemon fails at startup with:

    Error: creating daemon: agent[0] worktree "nova": 'nova' is not a worktree, repo, or workspace name

Trace:
- `internal/cli/config/daemon_config.go:LoadDaemonConfig` builds the daemon's agent list
  and sets each agent's `Worktree` via `deriveWorktree(agent)` (explicit worktree, else
  the single repo name).
- The supervisor then **resolves that worktree as an existing git worktree** via
  `internal/cli/workspace/worktree_repo.go:94`, which errors if the worktree directory
  does not exist on disk.
- In the working `loom epic run` path, the epic runner **provisions worker worktrees**.
  The `loom apply` + `loom daemon` + `loom run` path **never creates nova's worktree**,
  so the daemon errors and exits — leaving no supervisor to execute dispatched task runs.

**Conclusion:** `loom run <workflow>` (the TS-SDK path) dispatches task runs but does not,
on its own, provision the worktrees the daemon needs to execute them. The pure-TS-SDK
loop is currently a reconcile/dispatch primitive, not a full execution driver.

## 5. Open design question (needs an answer before more runs)

Is `loom run <workflow>` intended to drive **end-to-end execution** standalone, or is
`loom epic run` still the execution driver while the TS-SDK only **defines** the workflow?

- If standalone: there is a missing worktree-provisioning step (a flag, a daemon mode, or
  wiring) — the SDK path needs it to be self-sufficient.
- If `loom epic run` is the driver: the e2e should be **hybrid** — define agent+workflow in
  TS via `loom apply`, then execute with `loom epic run` (which provisions worktrees).

## 6. Artifacts (all on disk, syntax-clean)

- `e2e/epic_runner_real_codex_tsfirst_slack.sh` — Slack TS-SDK runner (all fixes 1–5).
- `e2e/epic_runner_real_codex_tsfirst.sh` — octocat variant.
- `e2e/run_epic_runner_real_codex_tsfirst_slack_podman.sh` — Slack podman wrapper.
- `e2e/run_epic_runner_real_codex_tsfirst_podman.sh` — octocat podman wrapper.

## 7. How to reproduce the blocker

    cd loomcli
    bash e2e/run_epic_runner_real_codex_tsfirst_slack_podman.sh
    # -> requires real Codex auth and reaches the daemon-worktree blocker described above

## 8. The TS-SDK code (reference)

`.loom/agents/nova.ts`:

    import { defineAgent, runtime } from '@loom/runtime';
    export default defineAgent({
      name: 'nova',
      backend: 'codex',
      repos: ['<repo>'],
      runtime: runtime.local({ repos: ['<repo>'] }),
    });

`.loom/workflows/epic-runner.ts`:

    import { defineWorkflow } from '@loom/runtime';
    export default defineWorkflow({
      name: 'epic-runner',
      singleton: (input) => `epic:${input.parentId}`,
      tools: ['workItems.readyChildren', 'workItems.listChildren', 'taskRuns.ensure'],
      async run(ctx) {
        const parentId = String(ctx.input.parentId || '');
        if (!parentId) throw new Error('epic-runner requires input.parentId');
        const ready = await ctx.workItems.readyChildren(parentId);
        for (const issue of ready) {
          await ctx.taskRuns.ensure({ workItemId: issue.id, role: 'task', reason: issue.title });
        }
        const children = await ctx.workItems.listChildren(parentId);
        return { ensured: ready.length, openRemaining: children.filter((i) => i.status !== 'closed' && i.status !== 'done').length };
      },
    });
