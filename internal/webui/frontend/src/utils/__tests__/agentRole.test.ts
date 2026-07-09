import { describe, expect, it } from "vitest";

import type { LoomAgentStatus } from "@/types";

import {
  agentRailRank,
  buildEpicLeadClaims,
  isBackgroundAgent,
  isInteractiveAgent,
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

describe("isInteractiveAgent", () => {
  it("uses role_kind when present", () => {
    expect(
      isInteractiveAgent(
        makeAgent({
          name: "operator-a",
          role: "operator",
          role_kind: "interactive",
        }),
      ),
    ).toBe(true);
    expect(
      isInteractiveAgent(
        makeAgent({ name: "lead-a", role: "lead", role_kind: "worker" }),
      ),
    ).toBe(false);
  });

  it("falls back to lead role names when role_kind is absent", () => {
    expect(
      isInteractiveAgent(makeAgent({ name: "lead-a", role: "lead" })),
    ).toBe(true);
    expect(
      isInteractiveAgent(makeAgent({ name: "task-a", role: "task" })),
    ).toBe(false);
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

  it("treats interactive-kind custom agents as regular", () => {
    expect(
      isBackgroundAgent(
        makeAgent({
          name: "operator-a",
          role: "operator",
          role_kind: "interactive",
          daemon_managed: true,
        }),
      ),
    ).toBe(false);
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

describe("agentRailRank", () => {
  it("ranks interactive-kind agents first", () => {
    expect(
      agentRailRank(
        makeAgent({
          name: "operator-a",
          role: "operator",
          role_kind: "interactive",
        }),
      ),
    ).toBe(0);
    expect(agentRailRank(makeAgent({ name: "worker-a", role: "task" }))).toBe(
      2,
    );
  });
});

describe("buildEpicLeadClaims", () => {
  it("claims epics for interactive-kind agents and legacy leads", () => {
    const claims = buildEpicLeadClaims([
      makeAgent({
        name: "operator-a",
        role: "operator",
        role_kind: "interactive",
        parent: "EPIC-1",
      }),
      makeAgent({ name: "lead-a", role: "lead", parent: "EPIC-2" }),
      makeAgent({ name: "task-a", role: "task", parent: "EPIC-3" }),
    ]);

    expect(claims.get("EPIC-1")).toBe("operator-a");
    expect(claims.get("EPIC-2")).toBe("lead-a");
    expect(claims.has("EPIC-3")).toBe(false);
  });
});
