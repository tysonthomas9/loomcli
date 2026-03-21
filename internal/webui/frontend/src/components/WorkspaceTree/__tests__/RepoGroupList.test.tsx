/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RepoGroupList component.
 * Verifies repo group rendering, agent cards, selection, collapse/expand,
 * unassigned section, health indicators, and disconnected state.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";
import type { RepoGroupListProps } from "../RepoGroupList";
import { RepoGroupList } from "../RepoGroupList";

// Mock AgentCard to simplify assertions
vi.mock("@/components/AgentCard", () => ({
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

// Mock ConnectionIndicator
vi.mock("../ConnectionIndicator", () => ({
  ConnectionIndicator: ({
    state,
  }: {
    state: string;
    disconnectedSince: number | null;
  }) => <span data-testid="connection-indicator">{state}</span>,
}));

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

function defaultProps(
  overrides: Partial<RepoGroupListProps> = {},
): RepoGroupListProps {
  return {
    repos: [],
    repoAgents: new Map(),
    unassignedAgents: [],
    repoCollapseState: {},
    onRepoToggle: vi.fn(),
    ...overrides,
  };
}

describe("RepoGroupList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("basic rendering", () => {
    it("renders nothing when repos and unassigned are empty", () => {
      const { container } = render(<RepoGroupList {...defaultProps()} />);
      expect(container.innerHTML).toBe("");
    });

    it("renders repo group headers for each repo", () => {
      const props = defaultProps({
        repos: [
          { name: "api", path: "/home/repos/api" },
          { name: "web", path: "/home/repos/web" },
        ],
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("api")).toBeInTheDocument();
      expect(screen.getByText("web")).toBeInTheDocument();
    });

    it("renders repo header as radio button with tooltip containing path", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/home/repos/api" }],
      });

      render(<RepoGroupList {...props} />);

      const radio = screen.getByRole("radio");
      expect(radio).toHaveAttribute("title", expect.stringContaining("/home/repos/api"));
    });
  });

  describe("selection", () => {
    it("sets aria-checked=true on selected repo", () => {
      const props = defaultProps({
        repos: [
          { name: "api", path: "/p/api" },
          { name: "web", path: "/p/web" },
        ],
        activeRepoName: "api",
      });

      render(<RepoGroupList {...props} />);

      const radios = screen.getAllByRole("radio");
      expect(radios[0]).toHaveAttribute("aria-checked", "true");
      expect(radios[1]).toHaveAttribute("aria-checked", "false");
    });

    it("calls onWorkspaceSelect when clicking a repo header", () => {
      const onWorkspaceSelect = vi.fn();
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        onWorkspaceSelect,
      });

      render(<RepoGroupList {...props} />);

      fireEvent.click(screen.getByRole("radio"));
      expect(onWorkspaceSelect).toHaveBeenCalledWith("api");
    });
  });

  describe("agent cards", () => {
    it("renders agent cards for each repo when not collapsed", () => {
      const agents = [makeAgent("falcon"), makeAgent("nova")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        repoCollapseState: {},
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-nova")).toBeInTheDocument();
    });

    it("hides agent cards when repo is collapsed", () => {
      const agents = [makeAgent("falcon")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        repoCollapseState: { api: true },
      });

      render(<RepoGroupList {...props} />);

      expect(screen.queryByTestId("agent-card-falcon")).not.toBeInTheDocument();
    });

    it("passes taskTitle to AgentCard when agentTasks is provided", () => {
      const agents = [makeAgent("falcon")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        agentTasks: { falcon: { title: "Fix login bug" } },
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByTestId("agent-card-falcon")).toHaveAttribute(
        "data-task-title",
        "Fix login bug",
      );
    });

    it("calls onAgentClick when clicking an agent card", () => {
      const onAgentClick = vi.fn();
      const agents = [makeAgent("falcon")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        onAgentClick,
      });

      render(<RepoGroupList {...props} />);

      fireEvent.click(screen.getByTestId("agent-card-falcon"));
      expect(onAgentClick).toHaveBeenCalledWith("falcon");
    });
  });

  describe("collapse toggle", () => {
    it("calls onRepoToggle when clicking the chevron", () => {
      const onRepoToggle = vi.fn();
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        onRepoToggle,
      });

      render(<RepoGroupList {...props} />);

      const chevron = screen.getByLabelText("Collapse agents");
      fireEvent.click(chevron);
      expect(onRepoToggle).toHaveBeenCalledWith("api");
    });

    it("shows Expand agents label when repo is collapsed", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoCollapseState: { api: true },
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByLabelText("Expand agents")).toBeInTheDocument();
    });

    it("chevron click does not trigger onWorkspaceSelect (stopPropagation)", () => {
      const onWorkspaceSelect = vi.fn();
      const onRepoToggle = vi.fn();
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        onWorkspaceSelect,
        onRepoToggle,
      });

      render(<RepoGroupList {...props} />);

      const chevron = screen.getByLabelText("Collapse agents");
      fireEvent.click(chevron);

      expect(onRepoToggle).toHaveBeenCalledWith("api");
      expect(onWorkspaceSelect).not.toHaveBeenCalled();
    });
  });

  describe("agent count display", () => {
    it("shows agent count when repo has agents and is connected", () => {
      const agents = [makeAgent("falcon"), makeAgent("nova")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        connectionState: "connected",
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("2")).toBeInTheDocument();
    });

    it("shows active/total format when activeAgentCount > 0", () => {
      const agents = [makeAgent("falcon"), makeAgent("nova")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        connectionState: "connected",
        repoHealthMap: new Map([
          [
            "api",
            {
              totalAgents: 2,
              activeCount: 1,
              errorCount: 0,
              healthColor: "green" as const,
            },
          ],
        ]),
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("1/2")).toBeInTheDocument();
    });

    it("does not show agent count when disconnected", () => {
      const agents = [makeAgent("falcon")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        connectionState: "disconnected",
        disconnectedSince: Date.now() - 5000,
      });

      render(<RepoGroupList {...props} />);

      // Agent count label should not appear
      const countElements = screen.queryAllByText("1");
      // The count with data-has-agents should not exist
      countElements.forEach((el) => {
        expect(el).not.toHaveAttribute("data-has-agents", "true");
      });
    });
  });

  describe("health indicator", () => {
    it("renders status dot with health color when connected", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        connectionState: "connected",
        repoHealthMap: new Map([
          [
            "api",
            {
              totalAgents: 1,
              activeCount: 0,
              errorCount: 1,
              healthColor: "red" as const,
            },
          ],
        ]),
      });

      const { container } = render(<RepoGroupList {...props} />);

      const dot = container.querySelector('[data-health="red"]');
      expect(dot).toBeInTheDocument();
    });

    it("renders ConnectionIndicator when disconnected", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        connectionState: "disconnected",
        disconnectedSince: Date.now() - 5000,
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByTestId("connection-indicator")).toBeInTheDocument();
    });

    it("defaults healthColor to green when repoHealthMap is not provided", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
      });

      const { container } = render(<RepoGroupList {...props} />);

      const dot = container.querySelector('[data-health="green"]');
      expect(dot).toBeInTheDocument();
    });
  });

  describe("tooltip", () => {
    it("includes agent stats in tooltip when repo has agents", () => {
      const agents = [makeAgent("falcon")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents,
        repoHealthMap: new Map([
          [
            "api",
            {
              totalAgents: 1,
              activeCount: 1,
              errorCount: 0,
              healthColor: "green" as const,
            },
          ],
        ]),
      });

      render(<RepoGroupList {...props} />);

      const radio = screen.getByRole("radio");
      const tooltip = radio.getAttribute("title") ?? "";
      expect(tooltip).toContain("Agents: 1");
      expect(tooltip).toContain("Active: 1");
      expect(tooltip).toContain("Errors: 0");
    });

    it("shows only path in tooltip when repo has no agents", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
      });

      render(<RepoGroupList {...props} />);

      const radio = screen.getByRole("radio");
      expect(radio).toHaveAttribute("title", "/p/api");
    });
  });

  describe("unassigned agents section", () => {
    it("renders unassigned section when unassigned agents exist", () => {
      const props = defaultProps({
        unassignedAgents: [makeAgent("orphan")],
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("Unassigned")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-orphan")).toBeInTheDocument();
    });

    it("does not render unassigned section when no unassigned agents", () => {
      const props = defaultProps({
        unassignedAgents: [],
      });

      render(<RepoGroupList {...props} />);

      expect(screen.queryByText("Unassigned")).not.toBeInTheDocument();
    });

    it("shows unassigned count", () => {
      const props = defaultProps({
        unassignedAgents: [makeAgent("a"), makeAgent("b")],
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("2")).toBeInTheDocument();
    });

    it("hides unassigned agents when __unassigned is collapsed", () => {
      const props = defaultProps({
        unassignedAgents: [makeAgent("orphan")],
        repoCollapseState: { __unassigned: true },
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("Unassigned")).toBeInTheDocument();
      expect(screen.queryByTestId("agent-card-orphan")).not.toBeInTheDocument();
    });

    it("calls onRepoToggle with __unassigned when toggling unassigned section", () => {
      const onRepoToggle = vi.fn();
      const props = defaultProps({
        unassignedAgents: [makeAgent("orphan")],
        onRepoToggle,
      });

      render(<RepoGroupList {...props} />);

      const chevron = screen.getByLabelText("Collapse agents");
      fireEvent.click(chevron);
      expect(onRepoToggle).toHaveBeenCalledWith("__unassigned");
    });
  });

  describe("edge cases", () => {
    it("handles repo with no agents in the map gracefully", () => {
      const props = defaultProps({
        repos: [{ name: "api", path: "/p/api" }],
        repoAgents: new Map(),
      });

      const { container } = render(<RepoGroupList {...props} />);

      expect(screen.getByText("api")).toBeInTheDocument();
      expect(container.querySelector('[data-has-agents="false"]')).toBeInTheDocument();
    });

    it("renders multiple repos with mixed states", () => {
      const agents = [makeAgent("falcon")];
      const repoAgents = new Map([["api", agents]]);
      const props = defaultProps({
        repos: [
          { name: "api", path: "/p/api" },
          { name: "web", path: "/p/web" },
        ],
        repoAgents,
        repoCollapseState: { web: true },
        activeRepoName: "api",
      });

      render(<RepoGroupList {...props} />);

      expect(screen.getByText("api")).toBeInTheDocument();
      expect(screen.getByText("web")).toBeInTheDocument();
      expect(screen.getByTestId("agent-card-falcon")).toBeInTheDocument();
    });
  });
});
