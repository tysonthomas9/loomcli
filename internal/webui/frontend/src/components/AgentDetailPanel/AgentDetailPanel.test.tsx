/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentDetailPanel component.
 * Covers Path field rendering and OpenInEditor integration in the Agent Info section.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus, LoomTaskInfo } from "@/types";

import { AgentDetailPanel } from "./AgentDetailPanel";

// Mock the useAgentTerminalLogs hook to prevent WebSocket/API calls in tests
vi.mock("@/hooks", () => ({
  useAgentTerminalLogs: () => ({
    mode: "idle",
    chunks: [],
    state: "disconnected",
    error: null,
    resetVersion: 0,
    refresh: vi.fn(),
    resize: vi.fn(),
    sendInput: vi.fn(),
    loadOlderLogs: vi.fn(),
    hasMoreLines: false,
    isLoadingMore: false,
  }),
}));

// Mock LogViewer to avoid terminal rendering complexity
vi.mock("../LogViewer", () => ({
  LogViewer: () => <div data-testid="log-viewer-mock" />,
}));

// Mock OpenInEditor to avoid its hook dependencies (useEditors)
vi.mock("../OpenInEditor", () => ({
  OpenInEditor: ({ path }: { path: string }) => (
    <div data-testid="open-in-editor" data-path={path} />
  ),
}));

// Mock GitTab to avoid its hook dependencies (useGitStatus, fetchDiffCommits)
vi.mock("./GitTab", () => ({
  GitTab: ({ agent }: { agent: { name: string } }) => (
    <div data-testid="git-tab-mock" data-agent={agent.name} />
  ),
}));

/** Helper to build a minimal agent object. */
function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "falcon",
    branch: "webui/falcon",
    status: "ready",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

/** Default props for the panel. */
function renderPanel(
  agentOverrides: Partial<LoomAgentStatus> = {},
  agentTasks: Record<string, LoomTaskInfo> = {},
) {
  const agent = makeAgent(agentOverrides);
  return render(
    <AgentDetailPanel
      isOpen={true}
      agentName={agent.name}
      agents={[agent]}
      agentTasks={agentTasks}
      onClose={vi.fn()}
    />,
  );
}

describe("AgentDetailPanel", () => {
  describe("path field in Agent Info section", () => {
    it("renders Path when agent has a path", () => {
      renderPanel({ path: "worktrees/cobalt" });

      expect(screen.getByText("Path")).toBeInTheDocument();
      expect(screen.getByText("worktrees/cobalt")).toBeInTheDocument();
    });

    it("does not render Path row when agent has no path", () => {
      renderPanel({ path: undefined });

      expect(screen.queryByText("Path")).not.toBeInTheDocument();
    });

    it("does not render Path row when path is empty string", () => {
      renderPanel({ path: "" });

      expect(screen.queryByText("Path")).not.toBeInTheDocument();
    });

    it("renders Path alongside other Agent Info fields", () => {
      renderPanel({
        path: "worktrees/falcon",
        branch: "feature-branch",
        status: "working: bd-123 (5m)",
      });

      // Path should be present
      expect(screen.getByText("Path")).toBeInTheDocument();
      expect(screen.getByText("worktrees/falcon")).toBeInTheDocument();

      // Other info fields should also be present in the Agent Info section
      expect(screen.getByText("Branch")).toBeInTheDocument();
      expect(screen.getByText("Status")).toBeInTheDocument();
      // Branch value appears in both metadata bar and Agent Info, so use getAllByText
      expect(
        screen.getAllByText("feature-branch").length,
      ).toBeGreaterThanOrEqual(1);
    });
  });

  describe("OpenInEditor in Agent Info section", () => {
    it("renders OpenInEditor when agent has worktree_path", () => {
      renderPanel({ worktree_path: "/home/user/worktrees/falcon" });

      const openInEditor = screen.getByTestId("open-in-editor");
      expect(openInEditor).toBeInTheDocument();
      expect(openInEditor).toHaveAttribute(
        "data-path",
        "/home/user/worktrees/falcon",
      );
    });

    it("does not render OpenInEditor when worktree_path is undefined", () => {
      renderPanel({ worktree_path: undefined });

      expect(screen.queryByTestId("open-in-editor")).not.toBeInTheDocument();
    });

    it("does not render OpenInEditor when worktree_path is empty string", () => {
      renderPanel({ worktree_path: "" });

      expect(screen.queryByTestId("open-in-editor")).not.toBeInTheDocument();
    });
  });

  describe("Git tab in tab bar", () => {
    it("renders Git tab button in the tab bar", () => {
      renderPanel();

      const gitTab = screen.getByRole("tab", { name: "Git" });
      expect(gitTab).toBeInTheDocument();
    });

    it("renders all three tabs: Info, Logs, Git", () => {
      renderPanel();

      expect(screen.getByRole("tab", { name: "Info" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Logs" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Git" })).toBeInTheDocument();
    });

    it("Info tab is selected by default", () => {
      renderPanel();

      const infoTab = screen.getByRole("tab", { name: "Info" });
      expect(infoTab).toHaveAttribute("aria-selected", "true");

      const gitTab = screen.getByRole("tab", { name: "Git" });
      expect(gitTab).toHaveAttribute("aria-selected", "false");
    });

    it("switches to Git tab content when Git tab is clicked", () => {
      renderPanel();

      // Git tab content should not be visible initially
      expect(screen.queryByTestId("git-tab-mock")).not.toBeInTheDocument();

      // Click Git tab
      fireEvent.click(screen.getByRole("tab", { name: "Git" }));

      // Git tab content should now be visible
      expect(screen.getByTestId("git-tab-mock")).toBeInTheDocument();

      // Git tab should be selected
      expect(screen.getByRole("tab", { name: "Git" })).toHaveAttribute(
        "aria-selected",
        "true",
      );

      // Info tab should no longer be selected
      expect(screen.getByRole("tab", { name: "Info" })).toHaveAttribute(
        "aria-selected",
        "false",
      );
    });

    it("passes the correct agent to GitTab component", () => {
      renderPanel({ name: "nova" });

      // Click Git tab to render GitTab
      fireEvent.click(screen.getByRole("tab", { name: "Git" }));

      const gitTabMock = screen.getByTestId("git-tab-mock");
      expect(gitTabMock).toHaveAttribute("data-agent", "nova");
    });

    it("Git tab panel has correct ARIA attributes", () => {
      renderPanel();

      // Click Git tab
      fireEvent.click(screen.getByRole("tab", { name: "Git" }));

      const tabPanel = document.getElementById("agent-panel-tabpanel-git");
      expect(tabPanel).toBeInTheDocument();
      expect(tabPanel).toHaveAttribute("role", "tabpanel");
      expect(tabPanel).toHaveAttribute(
        "aria-labelledby",
        "agent-panel-tab-git",
      );
    });

    it("Git tab button has correct ARIA attributes", () => {
      renderPanel();

      const gitTab = screen.getByRole("tab", { name: "Git" });
      expect(gitTab).toHaveAttribute("id", "agent-panel-tab-git");
      expect(gitTab).toHaveAttribute(
        "aria-controls",
        "agent-panel-tabpanel-git",
      );
    });
  });
});
