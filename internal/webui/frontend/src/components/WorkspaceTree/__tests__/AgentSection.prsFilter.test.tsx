/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { createAgentStore } from "@/stores/agentStore";
import type { LoomAgentStatus } from "@/types";

const mocks = vi.hoisted(() => ({
  deleteWorkspaceAgent: vi.fn(),
  showToast: vi.fn(),
  refetch: vi.fn(),
  useAgentServices: vi.fn(),
  useAgentStoreInstance: vi.fn(),
  useWorkspaceContext: vi.fn(),
}));

vi.mock("@/api/workspace/workspace", () => ({
  deleteWorkspaceAgent: mocks.deleteWorkspaceAgent,
}));

vi.mock("@/hooks", async () => {
  const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
  return {
    ...actual,
    useAgentStoreInstance: mocks.useAgentStoreInstance,
    useAgentServices: mocks.useAgentServices,
    useWorkspaceContext: mocks.useWorkspaceContext,
  };
});

vi.mock("@/hooks/ui", () => ({
  useToast: () => ({ showToast: mocks.showToast }),
}));

vi.mock("@dnd-kit/core", () => ({
  DndContext: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  closestCenter: vi.fn(),
  KeyboardSensor: vi.fn(),
  PointerSensor: vi.fn(),
  useSensor: vi.fn(),
  useSensors: () => [],
}));

vi.mock("@dnd-kit/sortable", () => ({
  SortableContext: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  useSortable: () => ({
    attributes: {},
    listeners: {},
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
  verticalListSortingStrategy: {},
  arrayMove: vi.fn(),
}));

vi.mock("@/components/AgentCard", () => ({
  AgentCard: ({ agent }: { agent: LoomAgentStatus }) => (
    <div data-testid={`agent-card-${agent.name}`}>
      {agent.display_name ?? agent.name}
      {agent.role_label
        ? ` ${agent.role_label}`
        : agent.role
          ? ` ${agent.role}`
          : ""}
    </div>
  ),
}));

import { AgentSection } from "../AgentSection";

function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "agent",
    branch: "main",
    status: "ready",
    ahead: 0,
    behind: 0,
    workspace: "WS",
    ...overrides,
  };
}

describe("AgentSection PR view filter", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    const store = createAgentStore();
    store.setState({
      agents: [
        makeAgent({
          name: "review-loomcli-pr-222",
          role: "pr-reviewer",
          display_name: "loomcli#222",
          role_label: "Review",
        }),
        makeAgent({ name: "codex-coder", role: "task" }),
        makeAgent({ name: "codex-planner", role: "plan" }),
      ],
    });
    mocks.useAgentStoreInstance.mockReturnValue(store);
    mocks.useAgentServices.mockReturnValue({
      services: [],
      total: 0,
      loading: false,
      initialized: true,
      error: null,
      refresh: vi.fn(),
    });
    mocks.useWorkspaceContext.mockReturnValue({
      agents: [],
      workspace: { name: "WS" },
      workspaceId: "ws-1",
      refetch: mocks.refetch,
    });
  });

  it("shows all agents and Add agent outside the PRs view", () => {
    render(<AgentSection onAddClick={vi.fn()} />);

    expect(screen.getByText(/loomcli#222/)).toBeInTheDocument();
    expect(screen.getByTestId("agent-card-codex-coder")).toBeInTheDocument();
    expect(screen.getByTestId("agent-card-codex-planner")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "+ Add agent" }),
    ).toBeInTheDocument();
  });

  it("keeps only pr-reviewer agents and hides Add agent on the PRs view", () => {
    render(<AgentSection activeView="prs" onAddClick={vi.fn()} />);

    expect(screen.getByText(/loomcli#222/)).toBeInTheDocument();
    expect(screen.getByText(/Review/)).toBeInTheDocument();
    expect(
      screen.queryByTestId("agent-card-codex-coder"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("agent-card-codex-planner"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "+ Add agent" }),
    ).not.toBeInTheDocument();
  });
});
