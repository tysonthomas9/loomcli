// Minimal fan-out workflow: request a task run per item, then poll each to a
// terminal status. Run under a registered driver bundle — the driver injects
// LOOM_RUN_TOKEN / LOOM_DRIVER_API_URL / LOOM_DRIVER_WORKSPACE, so there is
// no configuration here.
//
// See README "Quickstart: a workflow" for the push-based variant
// (epics.watch); taskRuns.await below is the simple polling form.
import { createLoomClient } from "@loom/sdk/driver";

export default async function run() {
  const loom = createLoomClient();
  const taskIds = Array.isArray(loom.input.taskIds) ? loom.input.taskIds : [];
  if (taskIds.length === 0) {
    return loom.failed({ summary: "input.taskIds is required", errorClass: "bad_input" });
  }

  // Fan out: one task run per task id.
  // `local-task-runner` is the real local runner: it runs the user-selected
  // backend CLI (claude/codex/opencode/gemini/cursor) over the worktree and
  // requires that CLI + its auth locally, failing closed otherwise. Pass
  // `loom.input.runner` (e.g. "daytona-task-runner") to run elsewhere.
  const runs = [];
  const runner = loom.input.runner || "local-task-runner";
  for (const taskId of taskIds) {
    runs.push(await loom.taskRuns.request({ taskId, runner }));
  }

  // Fan in: wait for each run to settle (client-side polling).
  const failed = [];
  for (const run of runs) {
    const settled = await loom.taskRuns.await({
      taskRunId: run.taskRunId,
      timeoutMs: 30 * 60 * 1000,
    });
    if (!settled || settled.status !== "completed") failed.push(run.taskRunId);
  }

  if (failed.length > 0) {
    return loom.needsReview({ summary: `task runs not completed: ${failed.join(", ")}` });
  }
  return loom.completed({ summary: `all ${runs.length} task runs completed` });
}
