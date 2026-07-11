import { describe, expect, it } from "vitest";

import type { LoomAgentStatus } from "@/types";

import {
  isBackgroundAgent,
  isLeadRole,
  isWorkerRole,
  splitAgentsByRuntime,
} from "../agentRole";

function makeAgent(
  overrides: Partial<LoomAgentStatus> & Pick<LoomAgentStatus, "name">,
): LoomAgentStatus {
  return {
    branch: "",
    status: "idle",
    ahead: 0,
    behind: 0,
    workspace: "default",
    ...overrides,
  };
}

describe("isLeadRole", () => {
  it("matches lead and orchestrator roles", () => {
    expect(isLeadRole("lead")).toBe(true);
    expect(isLeadRole("orchestrator")).toBe(true);
    expect(isLeadRole("task")).toBe(false);
  });
});

describe("isWorkerRole", () => {
  it("matches plan and task worker roles", () => {
    expect(isWorkerRole("plan")).toBe(true);
    expect(isWorkerRole("planner")).toBe(true);
    expect(isWorkerRole("task")).toBe(true);
    expect(isWorkerRole("lead")).toBe(false);
  });
});

describe("isBackgroundAgent", () => {
  it("treats lead agents as regular even when daemon-managed", () => {
    expect(
      isBackgroundAgent(
        makeAgent({ name: "lead-a", role: "lead", daemon_managed: true }),
      ),
    ).toBe(false);
  });

  it("treats daemon-managed workers as background", () => {
    expect(
      isBackgroundAgent(
        makeAgent({ name: "task-a", role: "task", daemon_managed: true }),
      ),
    ).toBe(true);
  });

  it("treats plan/task roles as background without daemon flag", () => {
    expect(
      isBackgroundAgent(makeAgent({ name: "planner-a", role: "plan" })),
    ).toBe(true);
    expect(isBackgroundAgent(makeAgent({ name: "task-a", role: "task" }))).toBe(
      true,
    );
  });
});

describe("splitAgentsByRuntime", () => {
  it("separates lead from supervised workers", () => {
    const lead = makeAgent({ name: "lead-a", role: "lead" });
    const planner = makeAgent({ name: "planner-a", role: "plan" });
    const task = makeAgent({ name: "task-a", role: "task" });

    expect(splitAgentsByRuntime([planner, lead, task])).toEqual({
      regular: [lead],
      background: [planner, task],
    });
  });
});
