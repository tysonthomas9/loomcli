/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentServiceDTO } from "@/api/agentServices";
import { createAgentStore } from "@/stores/agentStore";

const mocks = vi.hoisted(() => ({
  services: [] as AgentServiceDTO[],
  workspaceAgents: [] as Array<{
    name: string;
    cross_repo?: boolean;
    repos?: string[];
    role_name?: string;
  }>,
  useAgentStoreInstance: vi.fn(),
  useWorkspaceContext: vi.fn(),
}));

vi.mock("@/hooks", async () => {
  const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
  return {
    ...actual,
    useAgentStoreInstance: mocks.useAgentStoreInstance,
    useWorkspaceContext: mocks.useWorkspaceContext,
    useAgentServices: () => ({
      services: mocks.services,
      total: mocks.services.length,
      loading: false,
      initialized: true,
      error: null,
      refresh: vi.fn(),
    }),
  };
});

import { CollapsedAgentRail } from "./CollapsedAgentRail";

function scheduledAgent(): AgentServiceDTO {
  return {
    id: "test",
    name: "Test",
    triggerKind: "cron",
    enabled: true,
    behavior: {
      roleName: "scout",
      roleDisplayName: "Scout",
      workflowName: "scout",
      scripted: true,
    },
    bindings: [
      {
        id: "binding-test-hourly",
        sourceKind: "cron",
        schedule: "@hourly",
        enabled: true,
        routeKey: "cron.test.hourly",
      },
    ],
    nextFireAt: "2026-08-30T13:00:00Z",
    lastRunStatus: "completed",
    consecutiveFailures: 0,
    errors: [],
    createdAt: "2026-08-30T00:00:00Z",
    updatedAt: "2026-08-30T12:00:00Z",
  };
}

describe("CollapsedAgentRail", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.services = [];
    mocks.workspaceAgents = [];
    const store = createAgentStore();
    store.setState({ agents: [] });
    mocks.useAgentStoreInstance.mockReturnValue(store);
    mocks.useWorkspaceContext.mockReturnValue({
      agents: mocks.workspaceAgents,
      workspace: { name: "LOCALMODE" },
      workspaceId: "LOCALMODE",
    });
  });

  it("shows durable scheduled agents in compact mode", () => {
    mocks.services = [scheduledAgent()];

    render(<CollapsedAgentRail selectedAgentName="test" />);

    const avatar = screen.getByRole("button", { name: /Test/ });
    expect(avatar).toHaveAttribute("data-agent-name", "test");
    expect(avatar).toHaveAttribute("aria-current", "page");
  });

  it("does not duplicate a durable agent's configured projection", () => {
    mocks.services = [scheduledAgent()];
    mocks.workspaceAgents = [{ name: "test", role_name: "scout" }];

    render(<CollapsedAgentRail />);

    expect(screen.getAllByRole("button", { name: /Test/ })).toHaveLength(1);
  });

  it("keeps scheduled agents out of the PR-only compact rail", () => {
    mocks.services = [scheduledAgent()];

    render(<CollapsedAgentRail activeView="prs" />);

    expect(
      screen.queryByRole("button", { name: /Test/ }),
    ).not.toBeInTheDocument();
  });
});
