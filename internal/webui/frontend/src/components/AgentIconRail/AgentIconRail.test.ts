import { describe, expect, it } from "vitest";

import type { LoomAgentStatus } from "@/types";
import { isLiveAgentRailVisible, orderAgentsForEpicRunner } from "./AgentIconRail";

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

describe("isLiveAgentRailVisible", () => {
  it("keeps leads and service agents visible", () => {
    expect(
      isLiveAgentRailVisible(
        agent({ name: "lead", role: "lead", desired_state: "stopped" }),
      ),
    ).toBe(true);
    expect(
      isLiveAgentRailVisible(
        agent({ name: "worker", mode: "service", desired_state: "stopped" }),
      ),
    ).toBe(true);
  });

  it("hides completed ephemeral workers from the live rail", () => {
    expect(
      isLiveAgentRailVisible(
        agent({
          name: "worker-done",
          mode: "ephemeral",
          desired_state: "stopped",
        }),
      ),
    ).toBe(false);
  });

  it("keeps running ephemeral workers visible", () => {
    expect(
      isLiveAgentRailVisible(
        agent({
          name: "worker-live",
          mode: "ephemeral",
          desired_state: "running",
        }),
      ),
    ).toBe(true);
  });
});

describe("orderAgentsForEpicRunner", () => {
  it("orders leads before workers and unscoped agents", () => {
    const ordered = orderAgentsForEpicRunner([
      agent({ name: "unscoped", role: "task" }),
      agent({ name: "worker", role: "task", parent: "EPIC-1" }),
      agent({ name: "lead", role: "lead" }),
    ]);

    expect(ordered.map((x) => x.name)).toEqual(["lead", "worker", "unscoped"]);
  });
});
