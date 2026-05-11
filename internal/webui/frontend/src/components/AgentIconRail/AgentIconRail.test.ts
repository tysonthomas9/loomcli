/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { createElement } from "react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";
import {
  AgentIconRail,
  isLiveAgentRailVisible,
  orderAgentsForEpicRunner,
} from "./AgentIconRail";

vi.mock("react-router-dom", () => ({
  useNavigate: () => vi.fn(),
  useParams: () => ({ workspaceId: "E2E", agentName: "lead" }),
}));

vi.mock("zustand", () => ({
  useStore: (
    _store: unknown,
    selector: (state: { agents: LoomAgentStatus[] }) => unknown,
  ) => selector({ agents: [] }),
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => ({}),
}));

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

describe("AgentIconRail", () => {
  it("surfaces the add-agent action", () => {
    const onAddClick = vi.fn();

    render(createElement(AgentIconRail, { onAddClick }));
    fireEvent.click(screen.getByRole("button", { name: "Add agent" }));

    expect(onAddClick).toHaveBeenCalledTimes(1);
  });
});
