import { describe, it, expect } from "vitest";

import type { Issue, LoomAgentStatus } from "@/types";
import {
  buildWorkerHistoryByEpic,
  buildWorkerByTaskId,
  countTaskStatusesWithWorkers,
  effectiveTaskStatus,
  formatQueueLabel,
  groupAgentTasksByEpic,
  groupOpenByEpic,
  isWorkerTerminalOpenable,
  leadDeliveryStateLabel,
} from "../AgentWorkPanel";

// Minimal Issue factory — fills in just the fields the grouping function reads.
function issue(overrides: Partial<Issue> & Pick<Issue, "id">): Issue {
  return {
    id: overrides.id,
    title: overrides.title ?? `${overrides.id} title`,
    priority: overrides.priority ?? 2,
    created_at: "2026-01-01T00:00:00Z" as Issue["created_at"],
    updated_at: "2026-01-01T00:00:00Z" as Issue["updated_at"],
    ...overrides,
  } as Issue;
}

function buildMap(...issues: Issue[]): Map<string, Issue> {
  return new Map(issues.map((i) => [i.id, i]));
}

function agent(
  overrides: Partial<LoomAgentStatus> & { name: string },
): LoomAgentStatus {
  return {
    name: overrides.name,
    branch: overrides.branch ?? "main",
    status: overrides.status ?? "idle",
    ahead: overrides.ahead ?? 0,
    behind: overrides.behind ?? 0,
    workspace: overrides.workspace ?? "default",
    ...overrides,
  } as LoomAgentStatus;
}

describe("groupAgentTasksByEpic", () => {
  it("returns empty result when no agent is selected", () => {
    const m = buildMap(issue({ id: "T1", assignee: "nova" }));
    const res = groupAgentTasksByEpic(m, undefined);
    expect(res.totalTasks).toBe(0);
    expect(res.groups).toEqual([]);
    expect(res.counts).toEqual({ active: 0, done: 0, open: 0, blocked: 0 });
  });

  it("filters issues to those assigned to the agent", () => {
    const m = buildMap(
      issue({ id: "T1", assignee: "nova", parent: "EPIC-1", status: "open" }),
      issue({ id: "T2", assignee: "falcon", parent: "EPIC-1", status: "open" }),
      issue({ id: "T3", assignee: "nova", parent: "EPIC-1", status: "open" }),
      issue({ id: "EPIC-1", title: "Epic", issue_type: "epic" }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.totalTasks).toBe(2);
    expect(res.groups[0]?.tasks.map((t) => t.id).sort()).toEqual(["T1", "T3"]);
  });

  it("excludes the epic record itself from being counted as a task", () => {
    const m = buildMap(
      issue({ id: "EPIC-1", assignee: "nova", issue_type: "epic" }),
      issue({ id: "T1", assignee: "nova", parent: "EPIC-1", status: "open" }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.totalTasks).toBe(1);
    expect(res.groups[0]?.tasks).toHaveLength(1);
    expect(res.groups[0]?.tasks[0]?.id).toBe("T1");
  });

  it("groups by parent epic and resolves epic title from the map", () => {
    const m = buildMap(
      issue({ id: "AUTH", title: "Auth Hardening", issue_type: "epic" }),
      issue({ id: "API", title: "API Spec", issue_type: "epic" }),
      issue({ id: "T1", assignee: "nova", parent: "AUTH", status: "open" }),
      issue({ id: "T2", assignee: "nova", parent: "AUTH", status: "closed" }),
      issue({
        id: "T3",
        assignee: "nova",
        parent: "API",
        status: "in_progress",
      }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.groups).toHaveLength(2);
    const auth = res.groups.find((g) => g.epicId === "AUTH");
    expect(auth?.epicTitle).toBe("Auth Hardening");
    expect(auth?.totalCount).toBe(2);
    expect(auth?.doneCount).toBe(1);
    const api = res.groups.find((g) => g.epicId === "API");
    expect(api?.epicTitle).toBe("API Spec");
    expect(api?.totalCount).toBe(1);
    expect(api?.doneCount).toBe(0);
  });

  it("buckets parentless issues under an Unassigned group sorted last", () => {
    const m = buildMap(
      issue({ id: "AUTH", title: "Auth Hardening", issue_type: "epic" }),
      issue({ id: "T1", assignee: "nova", parent: "AUTH", status: "open" }),
      issue({ id: "ORPHAN", assignee: "nova", status: "open" }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.groups).toHaveLength(2);
    expect(res.groups[res.groups.length - 1]?.epicTitle).toBe("Unassigned");
  });

  it("counts statuses across all of the agent's issues", () => {
    const m = buildMap(
      issue({ id: "T1", assignee: "nova", status: "in_progress" }),
      issue({ id: "T2", assignee: "nova", status: "open" }),
      issue({ id: "T3", assignee: "nova", status: "blocked" }),
      issue({ id: "T4", assignee: "nova", status: "closed" }),
      issue({ id: "T5", assignee: "nova", status: "ready" }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.counts).toEqual({ active: 1, open: 2, blocked: 1, done: 1 });
  });

  it("sorts tasks within an epic by status: active, open, blocked, review, done", () => {
    const m = buildMap(
      issue({ id: "EPIC", title: "E", issue_type: "epic" }),
      issue({
        id: "T-DONE",
        assignee: "nova",
        parent: "EPIC",
        status: "closed",
      }),
      issue({ id: "T-OPEN", assignee: "nova", parent: "EPIC", status: "open" }),
      issue({
        id: "T-ACTIVE",
        assignee: "nova",
        parent: "EPIC",
        status: "in_progress",
      }),
      issue({
        id: "T-BLOCKED",
        assignee: "nova",
        parent: "EPIC",
        status: "blocked",
      }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.groups[0]?.tasks.map((t) => t.id)).toEqual([
      "T-ACTIVE",
      "T-OPEN",
      "T-BLOCKED",
      "T-DONE",
    ]);
  });

  it("handles missing epic title gracefully (falls back to ID)", () => {
    const m = buildMap(
      issue({
        id: "T1",
        assignee: "nova",
        parent: "EPIC-MISSING",
        status: "open",
      }),
    );
    const res = groupAgentTasksByEpic(m, "nova");
    expect(res.groups[0]?.epicTitle).toBe("EPIC-MISSING");
  });
});

describe("leadDeliveryStateLabel", () => {
  it("maps backend delivery states to user-visible labels", () => {
    expect(leadDeliveryStateLabel("pending")).toBe("context pending");
    expect(leadDeliveryStateLabel("delivered")).toBe("context sent");
    expect(leadDeliveryStateLabel("acknowledged")).toBe("lead acknowledged");
    expect(leadDeliveryStateLabel("")).toBe("");
    expect(leadDeliveryStateLabel(undefined)).toBe("");
  });
});

describe("isWorkerTerminalOpenable", () => {
  it("allows active or idle workers that are not explicitly stopped", () => {
    expect(
      isWorkerTerminalOpenable(
        agent({ name: "worker-active", state: "active" }),
      ),
    ).toBe(true);
    expect(
      isWorkerTerminalOpenable(agent({ name: "worker-idle", state: "idle" })),
    ).toBe(true);
  });

  it("blocks completed ephemeral workers so opening the UI cannot rerun them", () => {
    expect(
      isWorkerTerminalOpenable(
        agent({
          name: "worker-stopped",
          state: "stopped",
          desired_state: "stopped",
        }),
      ),
    ).toBe(false);
    expect(
      isWorkerTerminalOpenable(agent({ name: "worker-dead", state: "dead" })),
    ).toBe(false);
  });
});

describe("groupOpenByEpic", () => {
  it("shows open epics even when they have no child tasks", () => {
    const m = buildMap(
      issue({
        id: "EPIC-1",
        title: "Active Epic",
        issue_type: "epic",
        status: "open",
      }),
      issue({
        id: "EPIC-2",
        title: "Closed Epic",
        issue_type: "epic",
        status: "closed",
      }),
    );
    const res = groupOpenByEpic(m);
    expect(res.totalTasks).toBe(0);
    expect(res.groups.map((g) => g.epicId)).toEqual(["EPIC-1"]);
  });

  it("excludes closed tasks from the idle lead queue", () => {
    const m = buildMap(
      issue({ id: "EPIC", title: "E", issue_type: "epic", status: "open" }),
      issue({ id: "T-OPEN", parent: "EPIC", status: "open" }),
      issue({ id: "T-BLOCKED", parent: "EPIC", status: "blocked" }),
      issue({ id: "T-DONE", parent: "EPIC", status: "closed" }),
    );
    const res = groupOpenByEpic(m);
    expect(res.totalTasks).toBe(2);
    expect(res.counts).toEqual({ active: 0, done: 0, open: 1, blocked: 1 });
    expect(res.groups[0]?.tasks.map((t) => t.id)).toEqual([
      "T-OPEN",
      "T-BLOCKED",
    ]);
  });

  it("does not count the unassigned bucket as an epic in the queue label", () => {
    const m = buildMap(
      issue({
        id: "EPIC-1",
        title: "Shell",
        issue_type: "epic",
        status: "open",
      }),
      issue({
        id: "EPIC-2",
        title: "Foundation",
        issue_type: "epic",
        status: "open",
      }),
      issue({ id: "T-1", parent: "EPIC-1", status: "open" }),
      issue({ id: "T-2", parent: "EPIC-2", status: "open" }),
      issue({ id: "T-ORPHAN", status: "open" }),
    );
    const res = groupOpenByEpic(m);

    expect(
      formatQueueLabel("lead-open", res.groups, res.totalTasks, "nova"),
    ).toBe("Open queue · 2 epics · 1 unassigned · 3 tasks");
  });
});

describe("buildWorkerByTaskId", () => {
  it("maps worker agents by their task_id field", () => {
    const workers = buildWorkerByTaskId([
      agent({ name: "lead", role: "lead" }),
      agent({
        name: "worker-a",
        status: "working",
        task_id: "T-1",
        session_id: "session-a",
      }),
    ]);

    expect(workers.get("T-1")?.name).toBe("worker-a");
    expect(workers.has("T-2")).toBe(false);
  });

  it("falls back to parsing task ID from status", () => {
    const workers = buildWorkerByTaskId([
      agent({ name: "worker-b", status: "working: T-2 (1m)" }),
    ]);

    expect(workers.get("T-2")?.name).toBe("worker-b");
  });

  it("prefers an active running worker over a stopped one for the same task", () => {
    const workers = buildWorkerByTaskId([
      agent({
        name: "worker-stopped",
        task_id: "T-3",
        status: "idle",
        desired_state: "stopped",
      }),
      agent({
        name: "worker-active",
        task_id: "T-3",
        status: "working",
        desired_state: "running",
      }),
    ]);

    expect(workers.get("T-3")?.name).toBe("worker-active");
  });
});

describe("effectiveTaskStatus", () => {
  it("treats a live worker as active even before issue status catches up", () => {
    const task = issue({ id: "T-1", status: "open" });
    const liveWorker = agent({
      name: "worker-live",
      task_id: "T-1",
      state: "active",
      desired_state: "running",
    });
    const stoppedWorker = agent({
      name: "worker-stopped",
      task_id: "T-1",
      state: "stopped",
      desired_state: "stopped",
    });

    expect(effectiveTaskStatus(task, liveWorker)).toBe("active");
    expect(effectiveTaskStatus(task, stoppedWorker)).toBe("open");
  });

  it("preserves completed task status even when retained worker metadata exists", () => {
    expect(
      effectiveTaskStatus(
        issue({ id: "T-2", status: "closed" }),
        agent({
          name: "worker-retained",
          task_id: "T-2",
          state: "stopped",
          desired_state: "stopped",
        }),
      ),
    ).toBe("closed");
  });
});

describe("countTaskStatusesWithWorkers", () => {
  it("counts live-worker tasks as active for the panel summary", () => {
    const tasks = [
      issue({ id: "T-ACTIVE", status: "open" }),
      issue({ id: "T-OPEN", status: "open" }),
      issue({ id: "T-BLOCKED", status: "blocked" }),
      issue({ id: "T-DONE", status: "closed" }),
    ];
    const workers = buildWorkerByTaskId([
      agent({
        name: "worker-active",
        task_id: "T-ACTIVE",
        state: "active",
        desired_state: "running",
      }),
    ]);

    expect(countTaskStatusesWithWorkers(tasks, workers)).toEqual({
      active: 1,
      open: 1,
      blocked: 1,
      done: 1,
    });
  });
});

describe("buildWorkerHistoryByEpic", () => {
  it("groups ephemeral worker attempts under their epic", () => {
    const m = buildMap(
      issue({ id: "EPIC-1", issue_type: "epic", title: "Epic" }),
      issue({ id: "T-1", parent: "EPIC-1", status: "closed" }),
      issue({ id: "T-2", parent: "EPIC-1", status: "in_progress" }),
    );

    const history = buildWorkerHistoryByEpic(
      [
        agent({
          name: "worker-done",
          mode: "ephemeral",
          task_id: "T-1",
          desired_state: "stopped",
        }),
        agent({
          name: "worker-live",
          mode: "ephemeral",
          task_id: "T-2",
          desired_state: "running",
        }),
        agent({ name: "service-worker", mode: "service", task_id: "T-3" }),
      ],
      m,
    );

    expect(history.get("EPIC-1")?.map((x) => x.agent.name)).toEqual([
      "worker-live",
      "worker-done",
    ]);
    expect(history.get("EPIC-1")?.map((x) => x.status)).toEqual([
      "running",
      "completed",
    ]);
  });

  it("uses agent parent when task is no longer in the issue map", () => {
    const history = buildWorkerHistoryByEpic(
      [
        agent({
          name: "worker-retained",
          mode: "ephemeral",
          parent: "EPIC-2",
          task_id: "T-MISSING",
          desired_state: "stopped",
        }),
      ],
      buildMap(),
    );

    expect(history.get("EPIC-2")?.[0]?.taskId).toBe("T-MISSING");
  });
});
