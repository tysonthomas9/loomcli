/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { createAgentStore } from "@/stores/agentStore";
import type { LoomAgentStatus } from "@/types";

const mocks = vi.hoisted(() => ({
  deleteWorkspaceAgent: vi.fn(),
  showToast: vi.fn(),
  refetch: vi.fn(),
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
    <div data-testid={`agent-card-${agent.name}`}>{agent.name}</div>
  ),
}));

import { AgentSection } from "../AgentSection";

describe("AgentSection archive", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.deleteWorkspaceAgent.mockResolvedValue(undefined);

    const store = createAgentStore();
    store.setState({
      agents: [
        {
          name: "codex-coder",
          branch: "main",
          status: "changes",
          ahead: 1,
          behind: 0,
          workspace: "LOCALMODE",
          role: "coder",
        },
      ],
    });
    mocks.useAgentStoreInstance.mockReturnValue(store);
    mocks.useWorkspaceContext.mockReturnValue({
      agents: [],
      workspace: { name: "LOCALMODE" },
      workspaceId: "LOCALMODE",
      refetch: mocks.refetch,
    });
  });

  it("archives via the hover button", async () => {
    render(<AgentSection />);

    fireEvent.click(screen.getByTestId("agent-row-archive"));

    await waitFor(() => {
      expect(mocks.deleteWorkspaceAgent).toHaveBeenCalledWith(
        "LOCALMODE",
        "codex-coder",
      );
    });
    expect(mocks.showToast).toHaveBeenCalledWith("Agent codex-coder archived", {
      type: "success",
    });
    expect(mocks.refetch).toHaveBeenCalled();
  });

  it("archives via the right-click context menu", async () => {
    render(<AgentSection />);

    fireEvent.contextMenu(screen.getByTestId("sortable-agent-row"), {
      clientX: 40,
      clientY: 80,
    });

    expect(screen.getByTestId("agent-context-menu")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("agent-context-menu-archive"));

    await waitFor(() => {
      expect(mocks.deleteWorkspaceAgent).toHaveBeenCalledWith(
        "LOCALMODE",
        "codex-coder",
      );
    });
    expect(mocks.refetch).toHaveBeenCalled();
  });

  it("toasts an error when delete fails", async () => {
    mocks.deleteWorkspaceAgent.mockRejectedValueOnce(new Error("boom"));
    render(<AgentSection />);

    fireEvent.click(screen.getByTestId("agent-row-archive"));

    await waitFor(() => {
      expect(mocks.showToast).toHaveBeenCalledWith("Failed to archive agent", {
        type: "error",
      });
    });
    expect(mocks.refetch).not.toHaveBeenCalled();
  });
});
