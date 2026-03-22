/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RepoGroupedList component.
 * Verifies repo grouping, collapse/expand, localStorage persistence,
 * "Other" section rendering, and agent card interactions.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";
import { RepoGroupedList } from "../RepoGroupedList";

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

// Mock groupAgentsByRepo from AgentsSidebar
vi.mock("../AgentsSidebar", () => ({
  groupAgentsByRepo: (agents: LoomAgentStatus[], selectedRepos: string[]) => {
    const grouped = new Map<string, LoomAgentStatus[]>();
    const other: LoomAgentStatus[] = [];

    for (const repo of selectedRepos) {
      grouped.set(repo, []);
    }

    for (const agent of agents) {
      if (agent.repo && grouped.has(agent.repo)) {
        grouped.get(agent.repo)!.push(agent);
      } else {
        other.push(agent);
      }
    }

    return { grouped, other };
  },
}));

const REPO_GROUPS_STORAGE_KEY = "agents-sidebar-repo-groups-collapsed";

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

describe("RepoGroupedList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  describe("basic rendering", () => {
    it("renders repo group headers for selected repos", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      expect(screen.getByText("api")).toBeInTheDocument();
      expect(screen.getByText("web")).toBeInTheDocument();
    });

    it("shows agent count per repo group", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "api" }),
        makeAgent("ember", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      expect(screen.getByText("2")).toBeInTheDocument(); // api group
      expect(screen.getByText("1")).toBeInTheDocument(); // web group
    });

    it("renders agent cards under their repo group", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });
  });

  describe("Other section", () => {
    it("renders Other section for agents not matching selected repos", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("orphan", { repo: "misc" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
        />,
      );

      expect(screen.getByText("Other")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-orphan")).toBeInTheDocument();
    });

    it("does not render Other section when all agents match", () => {
      const agents = [makeAgent("falcon", { repo: "api" })];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
        />,
      );

      expect(screen.queryByText("Other")).not.toBeInTheDocument();
    });

    it("renders agents without repo in Other section", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("unassigned", { repo: undefined }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
        />,
      );

      expect(screen.getByText("Other")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-unassigned")).toBeInTheDocument();
    });

    it("shows correct count for Other section", () => {
      const agents = [
        makeAgent("a", { repo: "api" }),
        makeAgent("b", { repo: "misc" }),
        makeAgent("c", { repo: "other" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
        />,
      );

      // Other section should have count 2
      const otherHeader = screen.getByText("Other").closest('[role="button"]');
      expect(otherHeader).toHaveTextContent("2");
    });
  });

  describe("collapse/expand", () => {
    it("collapses repo group when header is clicked", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      fireEvent.click(screen.getByText("api"));

      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });

    it("expands repo group when collapsed header is clicked again", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      // Collapse
      fireEvent.click(screen.getByText("api"));
      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();

      // Expand
      fireEvent.click(screen.getByText("api"));
      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });

    it("collapses Other section when Other header is clicked", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("orphan", { repo: "misc" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
        />,
      );

      fireEvent.click(screen.getByText("Other"));
      expect(screen.queryByTestId("agent-card-orphan")).not.toBeInTheDocument();
    });

    it("toggles on Enter key press", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      const apiHeader = screen.getByText("api").closest('[role="button"]')!;
      fireEvent.keyDown(apiHeader, { key: "Enter" });

      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();
    });

    it("shows > when collapsed and v when expanded", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      // Both expanded by default, should show "v"
      const toggles = screen.getAllByText("v");
      expect(toggles.length).toBe(2);

      // Collapse one
      fireEvent.click(screen.getByText("api"));
      expect(screen.getByText(">")).toBeInTheDocument();
    });
  });

  describe("localStorage persistence", () => {
    it("persists collapsed state to localStorage", () => {
      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      fireEvent.click(screen.getByText("api"));

      const stored = JSON.parse(
        localStorage.getItem(REPO_GROUPS_STORAGE_KEY) ?? "{}",
      );
      expect(stored.api).toBe(true);
    });

    it("reads initial collapsed state from localStorage", () => {
      localStorage.setItem(
        REPO_GROUPS_STORAGE_KEY,
        JSON.stringify({ api: true }),
      );

      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });

    it("handles invalid localStorage data gracefully", () => {
      localStorage.setItem(REPO_GROUPS_STORAGE_KEY, "{invalid json}");

      const agents = [
        makeAgent("falcon", { repo: "api" }),
        makeAgent("nova", { repo: "web" }),
      ];

      // Should not throw
      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api", "web"]}
          agentTasks={{}}
        />,
      );

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });
  });

  describe("agent card props", () => {
    it("passes taskTitle to AgentCard", () => {
      const agents = [makeAgent("falcon", { repo: "api" })];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{
            falcon: {
              id: "t1",
              title: "Fix login",
              priority: 1,
              status: "open",
            },
          }}
        />,
      );

      expect(screen.getByTestId("agent-card-falcon")).toHaveAttribute(
        "data-task-title",
        "Fix login",
      );
    });

    it("calls onAgentClick when agent card is clicked", () => {
      const onAgentClick = vi.fn();
      const agents = [makeAgent("falcon", { repo: "api" })];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
          onAgentClick={onAgentClick}
        />,
      );

      fireEvent.click(screen.getByTestId("agent-card-falcon"));
      expect(onAgentClick).toHaveBeenCalledWith("falcon");
    });

    it("does not pass onClick when onAgentClick is undefined", () => {
      const agents = [makeAgent("falcon", { repo: "api" })];

      render(
        <RepoGroupedList
          agents={agents}
          selectedRepos={["api"]}
          agentTasks={{}}
        />,
      );

      // Click should not throw
      fireEvent.click(screen.getByTestId("agent-card-falcon"));
    });
  });

  describe("edge cases", () => {
    it("renders empty repo groups when no agents match", () => {
      render(
        <RepoGroupedList agents={[]} selectedRepos={["api"]} agentTasks={{}} />,
      );

      // Group header should still appear
      expect(screen.getByText("api")).toBeInTheDocument();
      expect(screen.getByText("0")).toBeInTheDocument();
    });

    it("handles empty selectedRepos array", () => {
      const agents = [makeAgent("falcon", { repo: "api" })];

      render(
        <RepoGroupedList agents={agents} selectedRepos={[]} agentTasks={{}} />,
      );

      // All agents go to Other
      expect(screen.getByText("Other")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });
  });
});
