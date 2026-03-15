/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentDetailPanel component.
 * Covers Path field rendering and OpenInEditor integration in the Agent Info section.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
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

// Mock fetchDiffCommits from @/api for "Show all commits" tests
const mockFetchDiffCommits = vi.fn();
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    fetchDiffCommits: (...args: unknown[]) => mockFetchDiffCommits(...args),
  };
});

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

  describe("Show all commits feature", () => {
    beforeEach(() => {
      mockFetchDiffCommits.mockReset();
    });

    it("renders 'Show all' button when agent.ahead > 10", () => {
      renderPanel({
        ahead: 15,
        commits: [
          { hash: "abc1234", message: "Some commit message" },
          { hash: "def5678", message: "Another commit" },
        ],
      });

      expect(screen.getByText("Show all 15 commits")).toBeInTheDocument();
    });

    it("does not render 'Show all' button when all commits are already shown", () => {
      renderPanel({
        ahead: 2,
        commits: [
          { hash: "abc1234", message: "Some commit message" },
          { hash: "def5678", message: "Another commit" },
        ],
      });

      expect(
        screen.queryByText(/Show all \d+ commits/),
      ).not.toBeInTheDocument();
    });

    it("renders 'Show all' button when commits shown is less than ahead count", () => {
      renderPanel({
        ahead: 10,
        commits: [{ hash: "abc1234", message: "Some commit message" }],
      });

      expect(screen.getByText("Show all 10 commits")).toBeInTheDocument();
    });

    it("calls fetchDiffCommits with agent name when 'Show all' is clicked", async () => {
      mockFetchDiffCommits.mockResolvedValue([
        {
          hash: "abc1234567890",
          short_hash: "abc1234",
          subject: "feat: something",
          author: "test",
          email: "test@test.com",
          date: "2026-01-01T00:00:00Z",
        },
      ]);

      renderPanel({
        name: "falcon",
        ahead: 15,
        commits: [{ hash: "abc1234", message: "Some commit message" }],
      });

      fireEvent.click(screen.getByText("Show all 15 commits"));

      await waitFor(() => {
        expect(mockFetchDiffCommits).toHaveBeenCalledWith("falcon");
      });
    });

    it("displays expanded commits and 'Show less' after successful fetch", async () => {
      mockFetchDiffCommits.mockResolvedValue([
        {
          hash: "abc1234567890",
          short_hash: "abc1234",
          subject: "feat: something",
          author: "test",
          email: "test@test.com",
          date: "2026-01-01T00:00:00Z",
        },
        {
          hash: "def5678901234",
          short_hash: "def5678",
          subject: "fix: another thing",
          author: "test",
          email: "test@test.com",
          date: "2026-01-02T00:00:00Z",
        },
      ]);

      renderPanel({
        ahead: 15,
        commits: [{ hash: "abc1234", message: "Some commit message" }],
      });

      fireEvent.click(screen.getByText("Show all 15 commits"));

      await waitFor(() => {
        expect(screen.getByText("feat: something")).toBeInTheDocument();
      });

      expect(screen.getByText("fix: another thing")).toBeInTheDocument();
      expect(screen.getByText("Show less")).toBeInTheDocument();
      // The "Show all" button should no longer be visible
      expect(
        screen.queryByText(/Show all \d+ commits/),
      ).not.toBeInTheDocument();
    });

    it("clicking 'Show less' reverts to original commits", async () => {
      mockFetchDiffCommits.mockResolvedValue([
        {
          hash: "abc1234567890",
          short_hash: "abc1234",
          subject: "feat: expanded commit",
          author: "test",
          email: "test@test.com",
          date: "2026-01-01T00:00:00Z",
        },
      ]);

      renderPanel({
        ahead: 15,
        commits: [{ hash: "orig123", message: "Original commit message" }],
      });

      // Click "Show all" and wait for expanded commits
      fireEvent.click(screen.getByText("Show all 15 commits"));

      await waitFor(() => {
        expect(screen.getByText("feat: expanded commit")).toBeInTheDocument();
      });

      // Original commit should no longer be visible (replaced by expanded)
      expect(
        screen.queryByText("Original commit message"),
      ).not.toBeInTheDocument();

      // Click "Show less" to revert
      fireEvent.click(screen.getByText("Show less"));

      // Original commits should be back
      expect(screen.getByText("Original commit message")).toBeInTheDocument();
      // Expanded commit message should be gone
      expect(
        screen.queryByText("feat: expanded commit"),
      ).not.toBeInTheDocument();
      // "Show all" button should reappear
      expect(screen.getByText("Show all 15 commits")).toBeInTheDocument();
    });

    it("expanded commits render hashes as plain spans (no link) since url is undefined", async () => {
      mockFetchDiffCommits.mockResolvedValue([
        {
          hash: "abc1234567890",
          short_hash: "abc1234",
          subject: "feat: something",
          author: "test",
          email: "test@test.com",
          date: "2026-01-01T00:00:00Z",
        },
      ]);

      renderPanel({
        ahead: 15,
        commits: [{ hash: "orig123", message: "Original commit" }],
      });

      fireEvent.click(screen.getByText("Show all 15 commits"));

      await waitFor(() => {
        expect(screen.getByText("abc1234")).toBeInTheDocument();
      });

      // The hash should be rendered as a <span>, not an <a> link
      const hashElement = screen.getByText("abc1234");
      expect(hashElement.tagName).toBe("SPAN");
      expect(hashElement.closest("a")).toBeNull();
    });

    it("shows 'Loading...' while fetching commits", async () => {
      // Create a promise that we can control resolution of
      let resolvePromise!: (value: unknown[]) => void;
      mockFetchDiffCommits.mockReturnValue(
        new Promise((resolve) => {
          resolvePromise = resolve;
        }),
      );

      renderPanel({
        ahead: 15,
        commits: [{ hash: "abc1234", message: "Some commit message" }],
      });

      fireEvent.click(screen.getByText("Show all 15 commits"));

      // While loading, the button should show "Loading..."
      await waitFor(() => {
        expect(screen.getByText("Loading...")).toBeInTheDocument();
      });

      // Resolve the promise to clean up
      resolvePromise([
        {
          hash: "abc1234567890",
          short_hash: "abc1234",
          subject: "feat: something",
          author: "test",
          email: "test@test.com",
          date: "2026-01-01T00:00:00Z",
        },
      ]);

      // After resolution, loading text should be gone
      await waitFor(() => {
        expect(screen.queryByText("Loading...")).not.toBeInTheDocument();
      });
    });
  });
});
