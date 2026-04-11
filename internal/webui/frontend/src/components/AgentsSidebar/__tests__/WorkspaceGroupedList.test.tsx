/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceGroupedList component and useWorkspaceGroups hook.
 * Verifies grouping logic, collapse/expand, localStorage persistence,
 * single-workspace optimization, and agent card rendering.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";
import { WorkspaceGroupedList } from "../WorkspaceGroupedList";

const TEST_WS_ID = "test-ws-uuid-1234";

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: TEST_WS_ID }),
  };
});

// Mock AgentCard to simplify assertions
vi.mock("../../AgentCard", () => ({
  AgentCard: ({
    agent,
    taskTitle,
    onClick,
  }: {
    agent: LoomAgentStatus;
    taskTitle?: string;
    onClick?: () => void;
  }) => (
    <div
      data-testid={`agent-card-${agent.name}`}
      data-task-title={taskTitle ?? ""}
      onClick={onClick}
    >
      {agent.name}
    </div>
  ),
}));

const WS_COLLAPSED_STORAGE_KEY = `loom:${TEST_WS_ID}:agents-sidebar-ws-collapsed`;

function makeAgent(
  name: string,
  overrides: Partial<LoomAgentStatus> = {},
): LoomAgentStatus {
  return {
    name,
    branch: `webui/${name}`,
    status: "ready",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

describe("WorkspaceGroupedList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem("loom:last-workspace-id", TEST_WS_ID);
  });

  describe("single workspace optimization", () => {
    it("renders flat list without headers when all agents share the same workspace", () => {
      const agents = [
        makeAgent("falcon", { workspace: "dev" }),
        makeAgent("nova", { workspace: "dev" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
      // No workspace headers
      expect(screen.queryByText("dev")).not.toBeInTheDocument();
    });

    it("renders flat list when all agents have empty workspace", () => {
      const agents = [
        makeAgent("falcon", { workspace: "" }),
        makeAgent("nova", { workspace: "" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });

    it("renders flat list when only one agent exists", () => {
      const agents = [makeAgent("falcon", { workspace: "dev" })];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });
  });

  describe("multi-workspace grouping", () => {
    it("renders workspace group headers when agents span multiple workspaces", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByText("api")).toBeInTheDocument();
      expect(screen.getByText("web")).toBeInTheDocument();
    });

    it("shows agent count per workspace group", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "api" }),
        makeAgent("ember", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByText("2")).toBeInTheDocument(); // api group
      expect(screen.getByText("1")).toBeInTheDocument(); // web group
    });

    it("sorts workspace groups alphabetically with (default) last", () => {
      const agents = [
        makeAgent("falcon", { workspace: "zebra" }),
        makeAgent("nova", { workspace: "" }), // becomes "(default)"
        makeAgent("ember", { workspace: "alpha" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      const buttons = screen.getAllByRole("button");
      const headerTexts = buttons.map((b) => b.textContent);

      // alpha should come before zebra, and (default) should be last
      const alphaIndex = headerTexts.findIndex((t) => t?.includes("alpha"));
      const zebraIndex = headerTexts.findIndex((t) => t?.includes("zebra"));
      const defaultIndex = headerTexts.findIndex((t) =>
        t?.includes("(default)"),
      );

      expect(alphaIndex).toBeLessThan(zebraIndex);
      expect(zebraIndex).toBeLessThan(defaultIndex);
    });

    it("renders agents under their workspace group", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });
  });

  describe("collapse/expand", () => {
    it("collapses workspace group when header is clicked", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      // Click the api workspace header to collapse it
      fireEvent.click(screen.getByText("api"));

      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();
      // web group should still be visible
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });

    it("expands workspace group when collapsed header is clicked again", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      // Collapse
      fireEvent.click(screen.getByText("api"));
      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();

      // Expand
      fireEvent.click(screen.getByText("api"));
      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });
  });

  describe("localStorage persistence", () => {
    it("persists collapsed state to localStorage", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      fireEvent.click(screen.getByText("api"));

      const stored = JSON.parse(
        localStorage.getItem(WS_COLLAPSED_STORAGE_KEY) ?? "{}",
      );
      expect(stored.api).toBe(true);
    });

    it("reads initial collapsed state from localStorage", () => {
      localStorage.setItem(
        WS_COLLAPSED_STORAGE_KEY,
        JSON.stringify({ api: true }),
      );

      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      // api group should be collapsed
      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();
      // web group should be visible
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });

    it("handles invalid localStorage data gracefully", () => {
      localStorage.setItem(WS_COLLAPSED_STORAGE_KEY, "not valid json [");

      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      // Should not throw
      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });
  });

  describe("agent card props", () => {
    it("passes taskTitle to AgentCard", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(
        <WorkspaceGroupedList
          agents={agents}
          agentTasks={{
            falcon: { id: "t1", title: "Fix bug", priority: 1, status: "open" },
          }}
        />,
      );

      expect(screen.getByTestId("agent-card-falcon")).toHaveAttribute(
        "data-task-title",
        "Fix bug",
      );
    });

    it("calls onAgentClick when agent card is clicked", () => {
      const onAgentClick = vi.fn();
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(
        <WorkspaceGroupedList
          agents={agents}
          agentTasks={{}}
          onAgentClick={onAgentClick}
        />,
      );

      fireEvent.click(screen.getByTestId("agent-card-falcon"));
      expect(onAgentClick).toHaveBeenCalledWith("falcon");
    });

    it("does not pass onClick to AgentCard when onAgentClick is undefined", () => {
      const agents = [
        makeAgent("falcon", { workspace: "api" }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      // Click should not throw - just verifying no error occurs
      fireEvent.click(screen.getByTestId("agent-card-falcon"));
    });
  });

  describe("edge cases", () => {
    it("renders nothing when agents array is empty", () => {
      const { container } = render(
        <WorkspaceGroupedList agents={[]} agentTasks={{}} />,
      );

      expect(container.innerHTML).toBe("");
    });

    it("groups agents with undefined workspace into (default)", () => {
      const agents = [
        makeAgent("falcon", { workspace: undefined }),
        makeAgent("nova", { workspace: "web" }),
      ];

      render(<WorkspaceGroupedList agents={agents} agentTasks={{}} />);

      expect(screen.getByText("(default)")).toBeInTheDocument();
      expect(screen.getByText("web")).toBeInTheDocument();
    });
  });
});
