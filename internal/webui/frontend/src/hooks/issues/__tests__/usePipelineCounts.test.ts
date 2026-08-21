import { describe, expect, it } from "vitest";

import type { Issue, LoomAgentStatus } from "@/types";

import { derivePipeline } from "../usePipelineCounts";

function issue(overrides: Partial<Issue>): Issue {
  return {
    id: "TASK-1",
    title: "Pipeline task",
    priority: 2,
    issue_type: "task",
    status: "open",
    created_at: "2026-08-21T15:00:00.000Z",
    updated_at: "2026-08-21T15:00:00.000Z",
    ...overrides,
  } as Issue;
}

function agent(overrides: Partial<LoomAgentStatus>): LoomAgentStatus {
  return {
    name: "agent-1",
    branch: "agent-1",
    status: "idle",
    ahead: 0,
    behind: 0,
    workspace: "workspace-1",
    ...overrides,
  };
}

describe("derivePipeline", () => {
  it("derives all pipeline rows from current issues and live agents", () => {
    const counts = derivePipeline(
      [
        issue({
          id: "PLAN-1",
          status: "in_progress",
          assignee: "planner",
        }),
        issue({ id: "BUILD-1", status: "in_progress" }),
        issue({ id: "BLOCK-1", status: "blocked" }),
        issue({ id: "GATE-1", status: "review", has_design: true }),
        issue({ id: "CLOSED-1", status: "closed" }),
        issue({ id: "EPIC-1", issue_type: "epic", status: "closed" }),
      ],
      [
        agent({
          name: "planner",
          active_task_id: "PLAN-1",
          active_phase: "planning",
        }),
        agent({ name: "ahead-branch", ahead: 2 }),
      ],
    );

    expect(counts).toEqual({
      backlog: 0,
      designing: 1,
      awaitingApproval: 1,
      building: 2,
      deferred: 0,
      awaitingMerge: 1,
      merged: 1,
      taskCount: 5,
    });
  });

  it("partitions every non-epic issue, including revision bounces and reviews", () => {
    const counts = derivePipeline(
      [
        issue({ id: "OPEN-1", status: "open" }),
        issue({ id: "REVISION-1", status: "open", labels: ["needs-revision"] }),
        issue({
          id: "CODE-1",
          status: "review",
          external_ref: "https://github.com/acme/repo/pull/1",
        }),
        issue({ id: "DEFER-1", status: "deferred" }),
        issue({ id: "CLOSED-1", status: "closed" }),
      ],
      [],
    );

    expect(counts).toMatchObject({
      backlog: 2,
      designing: 0,
      awaitingApproval: 1,
      building: 0,
      deferred: 1,
      merged: 1,
      taskCount: 5,
    });
    expect(
      counts.backlog +
        counts.designing +
        counts.awaitingApproval +
        counts.building +
        counts.deferred +
        counts.merged,
    ).toBe(counts.taskCount);
  });
});
