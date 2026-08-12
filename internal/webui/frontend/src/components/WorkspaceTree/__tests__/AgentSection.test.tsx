/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { AgentSection } from "../AgentSection";

const mocks = vi.hoisted(() => ({
  fleetAgents: [],
  workspaceAgents: [],
}));

vi.mock("zustand", () => ({
  useStore: (
    _store: unknown,
    selector: (state: { agents: unknown[] }) => unknown,
  ) => selector({ agents: mocks.fleetAgents }),
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => ({}),
  useDeleteWorkspaceAgent: () => vi.fn(),
  useWorkspaceContext: () => ({
    agents: mocks.workspaceAgents,
    workspace: null,
    workspaceId: "ws-alpha",
    refetch: vi.fn(),
  }),
}));

vi.mock("@/hooks/ui", () => ({
  useToast: () => ({ showToast: vi.fn() }),
}));

vi.mock("../SortableAgentList", () => ({
  SortableAgentList: () => <div data-testid="sortable-agent-list" />,
}));

describe("AgentSection", () => {
  it("renders an empty state when the workspace has no agents", () => {
    render(<AgentSection onAddClick={vi.fn()} />);

    expect(screen.getByText("Agents")).toBeInTheDocument();
    expect(screen.getByText("No agents yet")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "+ Add agent" }),
    ).toBeInTheDocument();
  });
});
