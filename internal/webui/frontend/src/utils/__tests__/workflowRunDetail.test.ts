import { describe, expect, it } from "vitest";

import type { WorkflowRun } from "@/api/workflows";
import {
  linkedSessionsForRun,
  mergeWorkflowRun,
  mergeWorkflowRunPage,
  workedTaskIdsForRun,
} from "@/utils/workflowRunDetail";

function run(overrides: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    workspace_key: "WS",
    run_id: "run-1",
    driver_id: "review-loop-agent",
    driver_version_id: "v1",
    status: "running",
    created_at: "2026-07-14T20:00:00Z",
    updated_at: "2026-07-14T20:00:01Z",
    ...overrides,
  };
}

describe("workflow run detail merging", () => {
  it("retains enriched output, steps, and payload across a sparse terminal row", () => {
    const enriched = run({
      payload: { epicId: "EPIC-1" },
      output: {
        taskRunId: "task-run-1",
        sessionId: "session-1",
        backend: "codex",
      },
      steps: [
        {
          id: "step-1",
          step_kind: "task_run",
          task_run_id: "task-run-1",
          task_id: "TASK-1",
          status: "running",
        },
      ],
    });
    const sparseTerminal = run({
      status: "completed",
      summary: "All tasks complete",
      output: {},
      steps: [],
      finished_at: "2026-07-14T20:05:00Z",
      updated_at: "2026-07-14T20:05:00Z",
    });

    const merged = mergeWorkflowRun(enriched, sparseTerminal);

    expect(merged.status).toBe("completed");
    expect(merged.summary).toBe("All tasks complete");
    expect(merged.finished_at).toBe("2026-07-14T20:05:00Z");
    expect(merged.output).toEqual(enriched.output);
    expect(merged.steps).toEqual(enriched.steps);
    expect(merged.payload).toEqual(enriched.payload);
  });

  it("retains detail enrichment when refreshing a sparse history page", () => {
    const enriched = run({
      steps: [
        {
          id: "step-1",
          step_kind: "task_run",
          task_run_id: "task-run-1",
          task_id: "TASK-1",
          status: "completed",
        },
      ],
    });

    const merged = mergeWorkflowRunPage(
      [enriched],
      [run({ status: "completed", updated_at: "2026-07-14T20:05:00Z" })],
    );

    expect(merged).toHaveLength(1);
    expect(merged[0]?.status).toBe("completed");
    expect(merged[0]?.steps).toEqual(enriched.steps);
  });
});

describe("linkedSessionsForRun", () => {
  it("returns every task session from a multi-step workflow run", () => {
    const links = linkedSessionsForRun(
      run({
        output: {
          taskRunId: "task-run-2",
          sessionId: "custom-session-2",
          taskId: "TASK-2",
        },
        steps: [
          {
            id: "step-1",
            step_kind: "task_run",
            task_run_id: "task-run-1",
            task_id: "TASK-1",
            status: "completed",
          },
          {
            id: "step-2",
            step_kind: "task_run",
            task_run_id: "task-run-2",
            task_id: "TASK-2",
            status: "running",
          },
        ],
      }),
    );

    expect(links).toEqual([
      {
        taskRunId: "task-run-1",
        taskId: "TASK-1",
        sessionId: "flue-task-run-1",
      },
      {
        taskRunId: "task-run-2",
        taskId: "TASK-2",
        sessionId: "custom-session-2",
      },
    ]);
  });
});

describe("workedTaskIdsForRun", () => {
  it("preserves canonical step order and deduplicates task IDs", () => {
    expect(
      workedTaskIdsForRun(
        run({
          payload: { event: { taskId: "TASK-3" } },
          output: { issueId: "TASK-2" },
          steps: [
            {
              id: "step-1",
              step_kind: "task_run",
              task_run_id: "task-run-1",
              task_id: "TASK-1",
              status: "completed",
            },
            {
              id: "step-duplicate",
              step_kind: "task_run",
              task_run_id: "task-run-2",
              task_id: "TASK-1",
              status: "completed",
            },
          ],
        }),
      ),
    ).toEqual(["TASK-1"]);
  });

  it("does not misattribute trigger targets, output aliases, or summaries as worked tasks", () => {
    expect(
      workedTaskIdsForRun(
        run({
          payload: { input: { issue_id: "TASK-INPUT" } },
          output: { task_id: "TASK-OUTPUT" },
          summary: "completed TASK-SUMMARY",
        }),
      ),
    ).toEqual([]);
  });
});
