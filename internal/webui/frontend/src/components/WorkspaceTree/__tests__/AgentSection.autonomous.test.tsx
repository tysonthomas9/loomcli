/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentServiceDTO } from "@/api/agentServices";
import { createAgentStore } from "@/stores/agentStore";

const mocks = vi.hoisted(() => ({
  services: [] as AgentServiceDTO[],
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
    useDeleteWorkspaceAgent: () => vi.fn(),
  };
});

vi.mock("@/hooks/ui", () => ({
  useToast: () => ({ showToast: vi.fn() }),
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
  AgentCard: ({
    agent,
    taskTitle,
  }: {
    agent: {
      name: string;
      display_name?: string;
      role_label?: string;
      status: string;
    };
    taskTitle?: string;
  }) => (
    <div data-testid={`agent-card-${agent.name}`} data-status={agent.status}>
      <span aria-label={`${agent.name} avatar`}>{agent.name.slice(0, 1)}</span>
      {agent.display_name}
      {agent.role_label}
      {taskTitle}
    </div>
  ),
}));

import { AgentSection } from "../AgentSection";

function scout(overrides: Partial<AgentServiceDTO> = {}): AgentServiceDTO {
  return {
    id: "scout",
    name: "Scout",
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
        id: "binding-scout-weekly",
        sourceKind: "cron",
        schedule: "@weekly",
        enabled: true,
        routeKey: "cron.scout.weekly",
      },
    ],
    nextFireAt: "2026-08-17T00:00:00Z",
    lastRunStatus: "succeeded",
    consecutiveFailures: 0,
    errors: [],
    createdAt: "2026-08-14T00:00:00Z",
    updatedAt: "2026-08-14T00:00:00Z",
    ...overrides,
  };
}

describe("AgentSection scheduled background agents", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.services = [];
    const store = createAgentStore();
    store.setState({ agents: [] });
    mocks.useAgentStoreInstance.mockReturnValue(store);
    mocks.useWorkspaceContext.mockReturnValue({
      agents: [],
      workspace: { name: "WS" },
      workspaceId: "WS",
      refetch: vi.fn(),
    });
  });

  it("renders the embedded-binding cadence and navigates by stable service id", () => {
    mocks.services = [scout()];
    const onAgentClick = vi.fn();

    render(<AgentSection onAgentClick={onAgentClick} />);

    expect(screen.getByTestId("agent-section-background")).toHaveTextContent(
      "Background",
    );
    expect(screen.getByTestId("autonomous-agent-scout")).toHaveTextContent(
      "Scout",
    );
    expect(screen.getByTestId("autonomous-agent-scout")).toHaveTextContent(
      "Weekly",
    );
    expect(screen.getByLabelText("scout avatar")).toBeInTheDocument();
    expect(screen.getByTestId("agent-card-scout")).toHaveTextContent("Scout");

    fireEvent.click(screen.getByTestId("autonomous-agent-scout"));
    expect(onAgentClick).toHaveBeenCalledWith("scout");
  });

  it("uses the single Add-agent entry when there are no instances", () => {
    render(<AgentSection onAddClick={vi.fn()} />);

    expect(
      screen.getByRole("button", { name: "+ Add agent" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "+ Add autonomous agent" }),
    ).not.toBeInTheDocument();
  });

  it("surfaces server-computed health errors as an unknown warning state", () => {
    mocks.services = [
      scout({
        errors: ["binding health unavailable: fleet-db timeout"],
      }),
    ];

    render(<AgentSection />);

    const row = screen.getByTestId("autonomous-agent-scout");
    expect(row).toHaveAttribute(
      "title",
      expect.stringContaining("fleet-db timeout"),
    );
    expect(row).toHaveAttribute("data-state", "unknown");
    expect(screen.getByTestId("agent-card-scout")).toHaveAttribute(
      "data-status",
      "error",
    );
  });
});
