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
  useAgentDiffStat: vi.fn(() => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
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
}));

// Mock OpenInEditor to avoid its hook dependencies (useEditors)
vi.mock("../OpenInEditor", () => ({
  OpenInEditor: ({ path }: { path: string }) => (
    <div data-testid="open-in-editor" data-path={path} />
  ),
}));

// Mock ChangesTab to avoid its hook dependencies (useDiff, started-from)
vi.mock("./ChangesTab", () => ({
  ChangesTab: ({ agent }: { agent: { name: string } }) => (
    <div data-testid="changes-tab-mock" data-agent={agent.name} />
  ),
}));

// Mock the v3 file browser to avoid pulling in CodeMirror and editor stack.
vi.mock("@/components/FileExplorer", () => ({
  WorkspaceFileBrowser: ({
    mode,
    agentName,
    isActive,
  }: {
    mode?: string;
    agentName?: string;
    isActive?: boolean;
  }) => (
    <div
      data-testid="workspace-file-browser-mock"
      data-mode={mode}
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

  describe("Changes tab in tab bar (D43)", () => {
    it("renders Info, Changes and Files, with no Git or Diff tab", () => {
      renderPanel();

      const names = screen.getAllByRole("tab").map((t) => t.textContent);
      expect(names).toEqual(["Info", "Changes", "Files"]);
      expect(screen.getByRole("tab", { name: "Info" })).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    it("shows the agent's Changes when the tab is clicked", async () => {
      renderPanel({ name: "nova" });

      fireEvent.click(screen.getByRole("tab", { name: "Changes" }));

      expect(screen.getByRole("tab", { name: "Changes" })).toHaveAttribute(
        "aria-selected",
        "true",
      );
      const mock = await screen.findByTestId("changes-tab-mock");
      expect(mock).toHaveAttribute("data-agent", "nova");
      const tabPanel = document.getElementById("agent-panel-tabpanel-changes");
      expect(tabPanel).toHaveAttribute(
        "aria-labelledby",
        "agent-panel-tab-changes",
      );
    });
  });

  describe("Files tab in tab bar", () => {
    it("renders Files tab button in the tab bar", () => {
      renderPanel();

      const filesTab = screen.getByRole("tab", { name: "Files" });
      expect(filesTab).toBeInTheDocument();
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

    it("passes correct props to WorkspaceFileBrowser", async () => {
      renderPanel({ name: "nova" });

      fireEvent.click(screen.getByRole("tab", { name: "Files" }));

      const fileBrowserMock = await screen.findByTestId(
        "workspace-file-browser-mock",
      );
      expect(fileBrowserMock).toHaveAttribute("data-mode", "agent");
      expect(fileBrowserMock).toHaveAttribute("data-agent", "nova");
      expect(fileBrowserMock).toHaveAttribute("data-active", "true");
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
