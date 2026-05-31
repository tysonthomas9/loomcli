# TS-SDK Epic Runner E2E — Findings & Handoff

Status: **passing as of 2026-05-31**. The pure TypeScript-first path now drives
the Slack-clone epic end-to-end with real Codex in Podman:
`loom check` -> `loom apply nova` -> daemon -> repeated `loom run epic-runner`.

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
- **The workflow executes end-to-end.** The successful Podman run printed
  `PASS tsfirst Codex epic runner Slack-clone E2E`; task A moved
  `open -> in_progress -> closed`, then task B moved `open -> in_progress -> closed`.
- **Worktree provisioning is covered.** `loom apply nova` prepares
  `workspace/worktrees/slack-src/nova`, and each `taskRuns.ensure` worker gets
  its own repo-scoped worktree under `workspace/worktrees/slack-src/<worker>`.
- **Dependency ordering is honored.** Task B remains blocked/open until task A closes,
  then the TypeScript reconcile loop dispatches the dependent worker.
- **Remote assertions pass.** The harness verifies `origin/loom-slack-tsfirst-target`
  contains `epic-runner-slack/task-a.txt`, `task-b.txt`, and `order.log` in order.

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
5. **Source-defined agent worktree gap** — `loom apply nova` registered an agent but
   did not prepare the repo-scoped worktree the daemon requires. Fixed by provisioning
   the single declared repo during `loom apply`.
6. **Task-worker repo gap** — `taskRuns.ensure` created ephemeral workers with no repo
   scope, so daemon start could not resolve their worktrees. Fixed by carrying the child
   work item's `source_repo` into TaskRun metadata and creating workers with the matched
   workspace repo.
7. **Legacy repo-label over-filtering** — using the single `repos` affinity as the
   legacy `repo` field made claims require both `source_repo=slack-src` and a
   nonexistent `repo:slack-src` label. Fixed by using `repos` for source-repo routing
   and worktree resolution without mutating the legacy label field.

## 4. Execution model now verified

The TypeScript-first SDK path can be a standalone execution driver when paired with a
running workspace daemon:

1. `loom check` validates `.loom/agents/*.ts` and `.loom/workflows/*.ts`.
2. `loom apply nova` registers the source-defined agent and prepares its repo worktree.
3. `loom daemon` supervises the applied agent and dynamic task workers.
4. Repeated `loom run epic-runner --input ...` calls reconcile the WorkflowContext,
   create task runs, and enqueue daemon start commands.
5. The daemon starts repo-scoped ephemeral workers, which claim, implement, push, and
   close each task.

The imperative `loom epic run` path remains useful as an all-in-one runner, but this e2e
now proves the pure TypeScript-first loop independently.

## 5. Artifacts (all on disk, syntax-clean)

- `e2e/epic_runner_real_codex_tsfirst_slack.sh` — Slack TS-SDK runner (all fixes 1–5).
- `e2e/epic_runner_real_codex_tsfirst.sh` — octocat variant.
- `e2e/run_epic_runner_real_codex_tsfirst_slack_podman.sh` — Slack podman wrapper.
- `e2e/run_epic_runner_real_codex_tsfirst_podman.sh` — octocat podman wrapper.

## 6. How to reproduce the passing run

    cd loomcli
    CODEX_HOME=/Users/tyson/.codex EPIC_RUNNER_TIMEOUT=900s RECONCILE_INTERVAL=3 \
      bash e2e/run_epic_runner_real_codex_tsfirst_slack_podman.sh
    # -> requires real Codex auth; successful run prints:
    # PASS tsfirst Codex epic runner Slack-clone E2E

## 7. The TS-SDK code (reference)

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
