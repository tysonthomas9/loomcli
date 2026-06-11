/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { createElement } from "react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";
import {
  AgentAvatarButton,
  AgentIconRail,
  agentAvatarTooltip,
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

describe("agentAvatarTooltip", () => {
  it("includes agent name and status type", () => {
    expect(
      agentAvatarTooltip(agent({ name: "local-coder", status: "3 changes" })),
    ).toBe("local-coder — changes");
  });
});

describe("AgentAvatarButton", () => {
  it("renders a hover tooltip with agent details", () => {
    render(
      createElement(AgentAvatarButton, {
        agent: agent({ name: "local-coder", status: "3 changes" }),
        selected: false,
        onClick: vi.fn(),
        size: 32,
      }),
    );

    expect(
      screen.getByRole("tooltip", { name: "local-coder — changes" }),
    ).toBeInTheDocument();
  });
});

describe("AgentIconRail", () => {
  it("surfaces the add-agent action", () => {
    const onAddClick = vi.fn();

    render(createElement(AgentIconRail, { onAddClick }));
    fireEvent.click(screen.getByRole("button", { name: "Add agent" }));

    expect(onAddClick).toHaveBeenCalledTimes(1);
  });

  it("renders an add-agent hover tooltip", () => {
    render(createElement(AgentIconRail, { onAddClick: vi.fn() }));

    expect(
      screen.getByRole("tooltip", { name: "Add agent" }),
    ).toBeInTheDocument();
  });
});
