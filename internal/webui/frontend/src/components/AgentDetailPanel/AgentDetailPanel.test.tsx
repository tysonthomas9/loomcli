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

vi.mock("@/hooks", () => ({
  useWorkspaceContext: () => ({
    getAgentByName: () => undefined,
  }),
  useFocusReturn: vi.fn(),
  useFocusTrap: vi.fn(),
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
  LAYER_TERMINAL_SEARCH: 5,
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

// Mock DiffTab to avoid its hook dependencies (useDiff)
vi.mock("./DiffTab", () => ({
  DiffTab: ({ agent }: { agent: { name: string } }) => (
    <div data-testid="diff-tab-mock" data-agent={agent.name} />
  ),
}));

// Mock FileEditorPanel to avoid pulling in CodeMirror and editor stack
vi.mock("@/components/FileEditorPanel", () => ({
  FileEditorPanel: ({
    agentName,
    isActive,
  }: {
    agentName: string;
    isActive: boolean;
  }) => (
    <div
      data-testid="file-editor-panel-mock"
      data-agent={agentName}
      data-active={String(isActive)}
    />
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
        status: "working: loom-123 (5m)",
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

    it("renders all four tabs: Info, Git, Diff, Files", () => {
      renderPanel();

      expect(screen.getByRole("tab", { name: "Info" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Git" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Diff" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Files" })).toBeInTheDocument();
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

  describe("Diff tab in tab bar", () => {
    it("renders Diff tab button in the tab bar", () => {
      renderPanel();
      const diffTab = screen.getByRole("tab", { name: "Diff" });
      expect(diffTab).toBeInTheDocument();
    });

    it("Diff tab activates on click and shows DiffTab component", async () => {
      renderPanel({ name: "nova" });

      // Diff tab should not be selected initially
      expect(screen.getByRole("tab", { name: "Diff" })).toHaveAttribute(
        "aria-selected",
        "false",
      );

      // Click Diff tab
      fireEvent.click(screen.getByRole("tab", { name: "Diff" }));

      // Diff tab should be selected
      expect(screen.getByRole("tab", { name: "Diff" })).toHaveAttribute(
        "aria-selected",
        "true",
      );

      // DiffTab mock should render (lazy-loaded via Suspense)
      const diffTabMock = await screen.findByTestId("diff-tab-mock");
      expect(diffTabMock).toHaveAttribute("data-agent", "nova");
    });

    it("Diff tab panel has correct ARIA attributes", async () => {
      renderPanel();
      fireEvent.click(screen.getByRole("tab", { name: "Diff" }));

      // Wait for lazy load
      await screen.findByTestId("diff-tab-mock");

      const tabPanel = document.getElementById("agent-panel-tabpanel-diff");
      expect(tabPanel).toBeInTheDocument();
      expect(tabPanel).toHaveAttribute("role", "tabpanel");
      expect(tabPanel).toHaveAttribute(
        "aria-labelledby",
        "agent-panel-tab-diff",
      );
    });

    it("Diff tab button has correct ARIA attributes", () => {
      renderPanel();
      const diffTab = screen.getByRole("tab", { name: "Diff" });
      expect(diffTab).toHaveAttribute("id", "agent-panel-tab-diff");
      expect(diffTab).toHaveAttribute(
        "aria-controls",
        "agent-panel-tabpanel-diff",
      );
    });
  });

  describe("Files tab in tab bar", () => {
    it("renders Files tab button in the tab bar", () => {
      renderPanel();

      const filesTab = screen.getByRole("tab", { name: "Files" });
      expect(filesTab).toBeInTheDocument();
    });

    it("renders all four tabs: Info, Git, Diff, Files", () => {
      renderPanel();

      expect(screen.getByRole("tab", { name: "Info" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Git" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Diff" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Files" })).toBeInTheDocument();
    });

    it("Files tab activates on click", () => {
      renderPanel();

      // Files tab should not be selected initially
      expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute(
        "aria-selected",
        "false",
      );

      // Click Files tab
      fireEvent.click(screen.getByRole("tab", { name: "Files" }));

      // Files tab should be selected
      expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute(
        "aria-selected",
        "true",
      );

      // The tabpanel should render
      const tabPanel = document.getElementById("agent-panel-tabpanel-files");
      expect(tabPanel).toBeInTheDocument();
    });

    it("passes correct props to FileEditorPanel", async () => {
      renderPanel({ name: "nova" });

      // Click Files tab to render FileEditorPanel
      fireEvent.click(screen.getByRole("tab", { name: "Files" }));

      // FileEditorPanel is lazy-loaded, wait for it to resolve
      const fileEditorMock = await screen.findByTestId(
        "file-editor-panel-mock",
      );
      expect(fileEditorMock).toHaveAttribute("data-agent", "nova");
      expect(fileEditorMock).toHaveAttribute("data-active", "true");
    });

    it("Files tab panel has correct ARIA attributes", () => {
      renderPanel();

      // Click Files tab
      fireEvent.click(screen.getByRole("tab", { name: "Files" }));

      const tabPanel = document.getElementById("agent-panel-tabpanel-files");
      expect(tabPanel).toBeInTheDocument();
      expect(tabPanel).toHaveAttribute("role", "tabpanel");
      expect(tabPanel).toHaveAttribute(
        "aria-labelledby",
        "agent-panel-tab-files",
      );
    });

    it("Files tab button has correct ARIA attributes", () => {
      renderPanel();

      const filesTab = screen.getByRole("tab", { name: "Files" });
      expect(filesTab).toHaveAttribute("id", "agent-panel-tab-files");
      expect(filesTab).toHaveAttribute(
        "aria-controls",
        "agent-panel-tabpanel-files",
      );
    });
  });

  describe("repo info in Agent Info section", () => {
    it('shows "Repos" row with RepoBadge when agent.repo is set', () => {
      renderPanel({ repo: "api" });

      expect(screen.getByText("Repos")).toBeInTheDocument();
      expect(screen.getByLabelText("Repository: api")).toBeInTheDocument();
      expect(screen.getByText("api")).toBeInTheDocument();
    });

    it('does not show "Repos" row when agent.repo is undefined', () => {
      renderPanel({ repo: undefined });

      expect(screen.queryByText("Repos")).not.toBeInTheDocument();
    });

    it('shows "All repos" label when agent.cross_repo is true', () => {
      renderPanel({ repo: "api", cross_repo: true });

      expect(screen.getByText("Repos")).toBeInTheDocument();
      expect(screen.getByText("All repos")).toBeInTheDocument();
    });

    it('does not show "All repos" when agent.cross_repo is false', () => {
      renderPanel({ repo: "api", cross_repo: false });

      expect(screen.getByText("Repos")).toBeInTheDocument();
      expect(screen.queryByText("All repos")).not.toBeInTheDocument();
    });
  });
});
