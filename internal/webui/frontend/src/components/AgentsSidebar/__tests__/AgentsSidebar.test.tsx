/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentsSidebar component.
 * Focuses on the viewSwitcher slot behavior and sync status rendering.
 */

import { render, screen, fireEvent, within, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import "@testing-library/jest-dom";
import type { LoomAgentStatus } from "@/types";
import { AgentsSidebar, groupAgentsByRepo } from "../AgentsSidebar";

// Default mock context value (used by most tests)
const defaultMockContext = {
  agents: [],
  tasks: {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 0,
    need_review: 0,
    backlog: 0,
  },
  taskLists: {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    backlog: [],
    done: [],
  },
  agentTasks: {},
  sync: {
    db_synced: true,
    db_last_sync: "",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 0,
    closed: 0,
    total: 0,
    completion: 0,
    remaining: 0,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
  isLoading: false,
  isConnected: true,
  lastUpdated: new Date(),
};

// Mutable override – tests can replace this before rendering
let mockContextOverride: Partial<typeof defaultMockContext> = {};

// Mock the hooks to prevent API calls in tests
let mockSelectedRepos: string[] = [];

vi.mock("@/hooks", () => ({
  useAgentContext: () => ({ ...defaultMockContext, ...mockContextOverride }),
  useRepoFilter: () => [mockSelectedRepos, vi.fn()],
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

const mockGitPushAll = vi.fn();
vi.mock("@/api/git", () => ({
  gitPushAll: (...args: unknown[]) => mockGitPushAll(...args),
}));

vi.mock("@/api/client", () => ({
  ApiError: class ApiError extends Error {
    statusText: string;
    constructor(status: number, statusText: string) {
      super(statusText);
      this.statusText = statusText;
    }
  },
}));

describe("AgentsSidebar", () => {
  beforeEach(() => {
    localStorage.clear();
    mockContextOverride = {};
    mockSelectedRepos = [];
  });

  describe("viewSwitcher slot", () => {
    it("renders viewSwitcher content when provided", () => {
      render(
        <AgentsSidebar
          viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>}
        />,
      );

      expect(screen.getByTestId("custom-switcher")).toBeInTheDocument();
      expect(screen.getByText("My Switcher")).toBeInTheDocument();
    });

    it("does not render viewSwitcher when collapsed", () => {
      render(
        <AgentsSidebar
          defaultCollapsed={true}
          viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>}
        />,
      );

      expect(screen.queryByTestId("custom-switcher")).not.toBeInTheDocument();
    });

    it("does not render viewSwitcher slot when prop is not provided", () => {
      const { container } = render(<AgentsSidebar />);

      // The viewSwitcherSlot wrapper div should not be present
      const slotDiv = container.querySelector('[class*="viewSwitcherSlot"]');
      expect(slotDiv).not.toBeInTheDocument();
    });

    it("hides viewSwitcher when sidebar is collapsed via toggle", () => {
      render(
        <AgentsSidebar
          viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>}
        />,
      );

      // Switcher should be visible initially
      expect(screen.getByTestId("custom-switcher")).toBeInTheDocument();

      // Click the collapse toggle button
      const toggleButton = screen.getByRole("button", {
        name: /collapse agents sidebar/i,
      });
      fireEvent.click(toggleButton);

      // Switcher should be hidden after collapse
      expect(screen.queryByTestId("custom-switcher")).not.toBeInTheDocument();
    });

    it("shows viewSwitcher again when sidebar is expanded after collapse", () => {
      render(
        <AgentsSidebar
          defaultCollapsed={true}
          viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>}
        />,
      );

      // Initially collapsed, switcher should not be visible
      expect(screen.queryByTestId("custom-switcher")).not.toBeInTheDocument();

      // Expand
      const expandButton = screen.getByRole("button", {
        name: /expand agents sidebar/i,
      });
      fireEvent.click(expandButton);
      expect(screen.getByTestId("custom-switcher")).toBeInTheDocument();
    });
  });

  describe("sync banner in Work Queue", () => {
    it('renders "need push" banner when git_needs_push > 0', () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 2,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      expect(screen.getByText(/2 need push/)).toBeInTheDocument();
      expect(screen.queryByText(/unpushed/)).not.toBeInTheDocument();
    });

    it("does not render banner when git_needs_push is 0", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 0,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      expect(screen.queryByText(/need push/)).not.toBeInTheDocument();
    });

    it('"Push All" button is present in banner', () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 3,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      expect(
        screen.getByRole("button", { name: /push all/i }),
      ).toBeInTheDocument();
    });

    it("shows tooltip with worktree details on banner text", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 2,
          git_needs_pull: 0,
          git_push_details: [
            { name: "nova", count: 3 },
            { name: "falcon", count: 1 },
          ],
        },
      };

      render(<AgentsSidebar />);

      const pushElement = screen.getByText(/2 need push/);
      expect(pushElement).toHaveAttribute("title");
      const title = pushElement.getAttribute("title")!;
      expect(title).toContain("nova");
      expect(title).toContain("3 commit");
      expect(title).toContain("falcon");
      expect(title).toContain("1 commit");
      expect(title).toContain("ahead");
    });

    it("Push All button opens confirmation dialog instead of pushing immediately", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 1,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      const pushButton = screen.getByRole("button", { name: /push all/i });
      fireEvent.click(pushButton);

      // Dialog should appear, push should NOT be called yet
      expect(screen.getByText("Push all branches?")).toBeInTheDocument();
      expect(mockGitPushAll).not.toHaveBeenCalled();
    });

    it("confirmation dialog lists branches with commit counts", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 2,
          git_needs_pull: 0,
          git_push_details: [
            { name: "nova", count: 3 },
            { name: "falcon", count: 1 },
          ],
        },
      };

      render(<AgentsSidebar />);

      fireEvent.click(screen.getByRole("button", { name: /push all/i }));

      expect(screen.getByText(/nova: 3 commits/)).toBeInTheDocument();
      expect(screen.getByText(/falcon: 1 commit$/)).toBeInTheDocument();
    });

    it("confirming the dialog triggers the push", async () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 1,
          git_needs_pull: 0,
        },
      };

      mockGitPushAll.mockResolvedValueOnce({
        results: [],
        pushed: 1,
        failed: 0,
      });

      render(<AgentsSidebar />);

      fireEvent.click(screen.getByRole("button", { name: /push all/i }));
      fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

      expect(mockGitPushAll).toHaveBeenCalled();

      mockGitPushAll.mockReset();
    });

    it("canceling the dialog does not trigger push", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 1,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      fireEvent.click(screen.getByRole("button", { name: /push all/i }));
      fireEvent.click(screen.getByTestId("confirm-dialog-cancel"));

      expect(mockGitPushAll).not.toHaveBeenCalled();
      // Dialog should be closed
      expect(screen.queryByText("Push all branches?")).not.toBeInTheDocument();
    });

    it("dialog closes after confirming", async () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 1,
          git_needs_pull: 0,
        },
      };

      mockGitPushAll.mockResolvedValueOnce({
        results: [],
        pushed: 1,
        failed: 0,
      });

      render(<AgentsSidebar />);

      fireEvent.click(screen.getByRole("button", { name: /push all/i }));
      expect(screen.getByText("Push all branches?")).toBeInTheDocument();

      fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
      expect(screen.queryByText("Push all branches?")).not.toBeInTheDocument();

      mockGitPushAll.mockReset();
    });

    it("fallback message when no push details available", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 3,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      fireEvent.click(screen.getByRole("button", { name: /push all/i }));

      expect(
        screen.getByText("Push 3 branches to remote?"),
      ).toBeInTheDocument();
    });
  });

  describe("footer sync status", () => {
    it("footer does not show push count", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 5,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      // "unpushed" should not appear anywhere (removed from footer)
      expect(screen.queryByText(/unpushed/)).not.toBeInTheDocument();
    });

    it('renders "unpulled" text in footer when git_needs_pull > 0', () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 0,
          git_needs_pull: 3,
        },
      };

      render(<AgentsSidebar />);

      expect(screen.getByText(/3 unpulled/)).toBeInTheDocument();
    });

    it("shows tooltip with worktree names for pull details", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 0,
          git_needs_pull: 1,
          git_pull_details: [{ name: "ember", count: 5 }],
        },
      };

      render(<AgentsSidebar />);

      const pullElement = screen.getByText(/1 unpulled/);
      expect(pullElement).toHaveAttribute("title");
      const title = pullElement.getAttribute("title")!;
      expect(title).toContain("ember");
      expect(title).toContain("5 commits");
      expect(title).toContain("behind");
    });

    it("does not render sync warnings when all counts are zero", () => {
      mockContextOverride = {
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 0,
          git_needs_pull: 0,
        },
      };

      render(<AgentsSidebar />);

      expect(screen.queryByText(/unpulled/)).not.toBeInTheDocument();
      expect(screen.queryByText(/need push/)).not.toBeInTheDocument();
    });
  });

  describe("work queue backlog button", () => {
    it("renders the Backlog button with needs_planning count when expanded", () => {
      mockContextOverride = {
        tasks: {
          needs_planning: 3,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 0,
        },
      };

      render(<AgentsSidebar />);

      const backlogButton = screen.getByRole("button", { name: /backlog/i });
      expect(backlogButton).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
    });

    it("is disabled when needs_planning is 0", () => {
      // defaultMockContext already has needs_planning: 0
      render(<AgentsSidebar />);

      const backlogButton = screen.getByRole("button", { name: /backlog/i });
      expect(backlogButton).toBeDisabled();
    });

    it("is enabled and clickable when needs_planning > 0", () => {
      mockContextOverride = {
        tasks: {
          needs_planning: 5,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 0,
        },
      };

      render(<AgentsSidebar />);

      const backlogButton = screen.getByRole("button", { name: /backlog/i });
      expect(backlogButton).not.toBeDisabled();

      // Clicking should not throw
      fireEvent.click(backlogButton);
    });

    it("sets data-highlight attribute correctly based on count", () => {
      // When needs_planning is 0, data-highlight should be "false"
      render(<AgentsSidebar />);

      const backlogButton = screen.getByRole("button", { name: /backlog/i });
      const zeroCount = within(backlogButton).getByText("0");
      expect(zeroCount).toHaveAttribute("data-highlight", "false");
    });

    it("sets data-highlight to true when needs_planning > 0", () => {
      mockContextOverride = {
        tasks: {
          needs_planning: 2,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 0,
        },
      };

      render(<AgentsSidebar />);

      const countSpan = screen.getByText("2", {
        selector: 'button span[data-highlight="true"]',
      });
      expect(countSpan).toHaveAttribute("data-highlight", "true");
    });
  });

  // ---------------------------------------------------------------------------
  // groupAgentsByRepo utility (pure function)
  // ---------------------------------------------------------------------------

  describe("groupAgentsByRepo utility", () => {
    function makeAgent(name: string, repo?: string): LoomAgentStatus {
      return {
        name,
        branch: "main",
        status: "idle",
        ahead: 0,
        behind: 0,
        ...(repo !== undefined && { repo }),
      };
    }

    it("groups agents correctly by matching repo to selectedRepos", () => {
      const agents = [
        makeAgent("a1", "repo-a"),
        makeAgent("a2", "repo-b"),
        makeAgent("a3", "repo-a"),
      ];

      const { grouped, other } = groupAgentsByRepo(agents, [
        "repo-a",
        "repo-b",
      ]);

      expect(grouped.get("repo-a")).toHaveLength(2);
      expect(grouped.get("repo-a")!.map((a) => a.name)).toEqual(["a1", "a3"]);
      expect(grouped.get("repo-b")).toHaveLength(1);
      expect(grouped.get("repo-b")![0]!.name).toBe("a2");
      expect(other).toHaveLength(0);
    });

    it('puts agents without repo field into "other"', () => {
      const agents = [
        makeAgent("a1", "repo-a"),
        makeAgent("a2"), // no repo
      ];

      const { grouped, other } = groupAgentsByRepo(agents, ["repo-a"]);

      expect(grouped.get("repo-a")).toHaveLength(1);
      expect(other).toHaveLength(1);
      expect(other[0]!.name).toBe("a2");
    });

    it('puts agents with non-matching repo into "other"', () => {
      const agents = [makeAgent("a1", "repo-x"), makeAgent("a2", "repo-a")];

      const { grouped, other } = groupAgentsByRepo(agents, ["repo-a"]);

      expect(grouped.get("repo-a")).toHaveLength(1);
      expect(other).toHaveLength(1);
      expect(other[0]!.name).toBe("a1");
    });

    it("returns empty grouped Map and all agents in other when selectedRepos is empty", () => {
      const agents = [makeAgent("a1", "repo-a"), makeAgent("a2")];

      const { grouped, other } = groupAgentsByRepo(agents, []);

      expect(grouped.size).toBe(0);
      expect(other).toHaveLength(2);
    });

    it("preserves order of selectedRepos in grouped Map keys", () => {
      const agents = [
        makeAgent("a1", "repo-c"),
        makeAgent("a2", "repo-a"),
        makeAgent("a3", "repo-b"),
      ];

      const { grouped } = groupAgentsByRepo(agents, [
        "repo-c",
        "repo-a",
        "repo-b",
      ]);

      const keys = Array.from(grouped.keys());
      expect(keys).toEqual(["repo-c", "repo-a", "repo-b"]);
    });
  });

  // ---------------------------------------------------------------------------
  // Grouped rendering (RepoGroupedList via AgentsSidebar)
  // ---------------------------------------------------------------------------

  describe("grouped rendering", () => {
    function makeAgentStatus(name: string, repo?: string): LoomAgentStatus {
      return {
        name,
        branch: "main",
        status: "idle",
        ahead: 0,
        behind: 0,
        ...(repo !== undefined && { repo }),
      };
    }

    it("renders repo group headers with correct names when filter active", () => {
      mockSelectedRepos = ["repo-a", "repo-b"];
      mockContextOverride = {
        agents: [
          makeAgentStatus("alpha", "repo-a"),
          makeAgentStatus("beta", "repo-b"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Find group header elements specifically (not the RepoBadge inside AgentCard)
      const headers = container.querySelectorAll('[class*="repoGroupHeader"]');
      const headerTexts = Array.from(headers).map((h) => {
        const nameSpan = h.querySelector('[class*="repoGroupName"]');
        return nameSpan?.textContent;
      });
      expect(headerTexts).toContain("repo-a");
      expect(headerTexts).toContain("repo-b");
    });

    it("shows agent counts in group header badges", () => {
      mockSelectedRepos = ["repo-a", "repo-b"];
      mockContextOverride = {
        agents: [
          makeAgentStatus("alpha", "repo-a"),
          makeAgentStatus("gamma", "repo-a"),
          makeAgentStatus("beta", "repo-b"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Find the header for repo-a – its sibling count badge should show 2
      const headers = container.querySelectorAll('[class*="repoGroupHeader"]');
      expect(headers.length).toBeGreaterThanOrEqual(2);

      // repo-a header: contains "repo-a" text and count "2"
      const repoAHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("repo-a"),
      )!;
      expect(repoAHeader).toBeDefined();
      expect(
        within(repoAHeader as HTMLElement).getByText("2"),
      ).toBeInTheDocument();

      // repo-b header: count "1"
      const repoBHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("repo-b"),
      )!;
      expect(repoBHeader).toBeDefined();
      expect(
        within(repoBHeader as HTMLElement).getByText("1"),
      ).toBeInTheDocument();
    });

    it('"Other" section appears when agents have no or non-matching repo', () => {
      mockSelectedRepos = ["repo-a"];
      mockContextOverride = {
        agents: [
          makeAgentStatus("alpha", "repo-a"),
          makeAgentStatus("beta"), // no repo
          makeAgentStatus("gamma", "repo-x"), // non-matching
        ],
      };

      render(<AgentsSidebar collapsible={false} />);

      expect(screen.getByText("Other")).toBeInTheDocument();

      // The "Other" header badge should show 2
      const { container } = render(<AgentsSidebar collapsible={false} />);
      const headers = container.querySelectorAll('[class*="repoGroupHeader"]');
      const otherHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("Other"),
      )!;
      expect(otherHeader).toBeDefined();
      expect(
        within(otherHeader as HTMLElement).getByText("2"),
      ).toBeInTheDocument();
    });

    it('"Other" section is hidden when all agents match selected repos', () => {
      mockSelectedRepos = ["repo-a", "repo-b"];
      mockContextOverride = {
        agents: [
          makeAgentStatus("alpha", "repo-a"),
          makeAgentStatus("beta", "repo-b"),
        ],
      };

      render(<AgentsSidebar collapsible={false} />);

      expect(screen.queryByText("Other")).not.toBeInTheDocument();
    });
  });

  // ---------------------------------------------------------------------------
  // Flat list preservation (no grouping when no repos selected)
  // ---------------------------------------------------------------------------

  describe("flat list preservation", () => {
    it("renders agents without group headers when mockSelectedRepos is empty", () => {
      mockSelectedRepos = [];
      mockContextOverride = {
        agents: [
          {
            name: "alpha",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            repo: "repo-a",
          },
          {
            name: "beta",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
          },
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Agent names should render
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();

      // No group headers should exist
      const groupHeaders = container.querySelectorAll(
        '[class*="repoGroupHeader"]',
      );
      expect(groupHeaders).toHaveLength(0);
    });
  });

  // ---------------------------------------------------------------------------
  // Collapsible group behavior
  // ---------------------------------------------------------------------------

  describe("collapsible group behavior", () => {
    it("clicking a group header hides agents in that group", () => {
      mockSelectedRepos = ["repo-a"];
      mockContextOverride = {
        agents: [
          {
            name: "alpha",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            repo: "repo-a",
          },
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Agent should be visible initially
      expect(screen.getByText("alpha")).toBeInTheDocument();

      // Click the repo-a group header to collapse it
      const header = container.querySelector(
        '[class*="repoGroupHeader"]',
      ) as HTMLElement;
      expect(header).toBeDefined();
      fireEvent.click(header);

      // Agent should now be hidden
      expect(screen.queryByText("alpha")).not.toBeInTheDocument();
    });

    it("clicking a collapsed group header re-expands it", () => {
      mockSelectedRepos = ["repo-a"];
      mockContextOverride = {
        agents: [
          {
            name: "alpha",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            repo: "repo-a",
          },
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      const header = container.querySelector(
        '[class*="repoGroupHeader"]',
      ) as HTMLElement;

      // Collapse
      fireEvent.click(header);
      expect(screen.queryByText("alpha")).not.toBeInTheDocument();

      // Re-expand
      fireEvent.click(header);
      expect(screen.getByText("alpha")).toBeInTheDocument();
    });
  });

  // ---------------------------------------------------------------------------
  // Workspace grouping
  // ---------------------------------------------------------------------------

  describe("workspace grouping", () => {
    function makeWsAgent(name: string, workspace?: string): LoomAgentStatus {
      return {
        name,
        branch: "main",
        status: "idle",
        ahead: 0,
        behind: 0,
        ...(workspace !== undefined && { workspace }),
      };
    }

    it("groups agents correctly by workspace field", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "frontend"),
          makeWsAgent("beta", "backend"),
          makeWsAgent("gamma", "frontend"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Should render workspace headers (use button selector to avoid matching child spans)
      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      expect(headers.length).toBe(2);

      // All agents should be rendered
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
    });

    it('agents with no workspace appear in "(default)" group', () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "frontend"),
          makeWsAgent("beta"), // no workspace
          makeWsAgent("gamma", ""), // empty workspace
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Should have workspace headers for "frontend" and "(default)"
      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      expect(headers.length).toBe(2);

      // "(default)" group should exist
      const headerTexts = Array.from(headers).map((h) => {
        const textSpan = h.querySelector('[class*="workspaceHeaderText"]');
        return textSpan?.textContent;
      });
      expect(headerTexts).toContain("(default)");
      expect(headerTexts).toContain("frontend");
    });

    it('groups sorted alphabetically with "(default)" last', () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "zebra"),
          makeWsAgent("beta"), // (default)
          makeWsAgent("gamma", "alpha-ws"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      const headerTexts = Array.from(headers).map((h) => {
        const textSpan = h.querySelector('[class*="workspaceHeaderText"]');
        return textSpan?.textContent;
      });

      // alpha-ws < zebra < (default)
      expect(headerTexts).toEqual(["alpha-ws", "zebra", "(default)"]);
    });

    it("clicking workspace header toggles collapse", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "frontend"),
          makeWsAgent("beta", "backend"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Both agents visible initially
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();

      // Click the first workspace header (frontend comes before backend alphabetically: "backend" < "frontend")
      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      const backendHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("backend"),
      ) as HTMLElement;
      fireEvent.click(backendHeader);

      // beta should be hidden, alpha still visible
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.queryByText("beta")).not.toBeInTheDocument();
    });

    it("collapsed group hides its agent cards", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "ws-a"),
          makeWsAgent("beta", "ws-a"),
          makeWsAgent("gamma", "ws-b"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Click ws-a header
      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      const wsAHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("ws-a"),
      ) as HTMLElement;
      fireEvent.click(wsAHeader);

      // alpha and beta hidden, gamma still visible
      expect(screen.queryByText("alpha")).not.toBeInTheDocument();
      expect(screen.queryByText("beta")).not.toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
    });

    it("single workspace renders flat without group header", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "only-ws"),
          makeWsAgent("beta", "only-ws"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // No workspace headers should be rendered
      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      expect(headers).toHaveLength(0);

      // Agents should still be visible
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
    });

    it("single workspace with all agents in (default) renders flat", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha"), // no workspace -> (default)
          makeWsAgent("beta", ""), // empty workspace -> (default)
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      expect(headers).toHaveLength(0);

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
    });

    it("workspace header shows agent count badge", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "ws-a"),
          makeWsAgent("beta", "ws-a"),
          makeWsAgent("gamma", "ws-b"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      const wsAHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("ws-a"),
      ) as HTMLElement;

      // ws-a has 2 agents
      expect(within(wsAHeader).getByText("2")).toBeInTheDocument();

      const wsBHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("ws-b"),
      ) as HTMLElement;

      // ws-b has 1 agent
      expect(within(wsBHeader).getByText("1")).toBeInTheDocument();
    });

    it("persists collapsed state to localStorage", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "frontend"),
          makeWsAgent("beta", "backend"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      // Click a workspace header to collapse it
      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      const backendHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("backend"),
      ) as HTMLElement;
      fireEvent.click(backendHeader);

      // localStorage should have the collapsed state
      const stored = localStorage.getItem("agents-sidebar-ws-collapsed");
      expect(stored).toBeTruthy();
      const parsed = JSON.parse(stored!);
      expect(parsed["backend"]).toBe(true);
    });

    it("re-expanding clears collapsed state in localStorage", () => {
      mockContextOverride = {
        agents: [
          makeWsAgent("alpha", "frontend"),
          makeWsAgent("beta", "backend"),
        ],
      };

      const { container } = render(<AgentsSidebar collapsible={false} />);

      const headers = container.querySelectorAll(
        'button[class*="workspaceHeader"]',
      );
      const backendHeader = Array.from(headers).find((h) =>
        h.textContent?.includes("backend"),
      ) as HTMLElement;

      // Collapse then re-expand
      fireEvent.click(backendHeader);
      fireEvent.click(backendHeader);

      const stored = localStorage.getItem("agents-sidebar-ws-collapsed");
      expect(stored).toBeTruthy();
      const parsed = JSON.parse(stored!);
      expect(parsed["backend"]).toBe(false);
    });
  });

  // ---------------------------------------------------------------------------
  // Responsive layout breakpoint (auto-collapse at <1024px)
  // ---------------------------------------------------------------------------

  describe("responsive layout breakpoint", () => {
    let originalMatchMedia: typeof window.matchMedia;

    // Helper to create a mock matchMedia for the max-width:1024px query
    function createMockMatchMedia(matches: boolean) {
      const listeners: Array<(e: MediaQueryListEvent) => void> = [];
      const mql = {
        matches,
        media: "(max-width: 1024px)",
        addEventListener: vi.fn(
          (_event: string, handler: (e: MediaQueryListEvent) => void) => {
            listeners.push(handler);
          },
        ),
        removeEventListener: vi.fn(
          (_event: string, handler: (e: MediaQueryListEvent) => void) => {
            const idx = listeners.indexOf(handler);
            if (idx >= 0) listeners.splice(idx, 1);
          },
        ),
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      };
      const matchMediaMock = vi.fn(() => mql);
      return { matchMediaMock, mql, listeners };
    }

    beforeEach(() => {
      originalMatchMedia = window.matchMedia;
    });

    afterEach(() => {
      window.matchMedia = originalMatchMedia;
    });

    it("auto-collapses the sidebar when viewport is below 1024px on mount", () => {
      const { matchMediaMock } = createMockMatchMedia(true);
      window.matchMedia = matchMediaMock as unknown as typeof window.matchMedia;

      render(
        <AgentsSidebar
          viewSwitcher={<div data-testid="sidebar-content">Content</div>}
        />,
      );

      // Sidebar should be collapsed — viewSwitcher hidden when collapsed
      expect(screen.queryByTestId("sidebar-content")).not.toBeInTheDocument();

      // The expand button should be visible (collapsed state)
      expect(
        screen.getByRole("button", { name: /expand agents sidebar/i }),
      ).toBeInTheDocument();
    });

    it("does not auto-collapse when viewport is at or above 1024px on mount", () => {
      const { matchMediaMock } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock as unknown as typeof window.matchMedia;

      render(
        <AgentsSidebar
          viewSwitcher={<div data-testid="sidebar-content">Content</div>}
        />,
      );

      // Sidebar should remain expanded — viewSwitcher visible
      expect(screen.getByTestId("sidebar-content")).toBeInTheDocument();

      // The collapse button should be visible (expanded state)
      expect(
        screen.getByRole("button", { name: /collapse agents sidebar/i }),
      ).toBeInTheDocument();
    });

    it("collapses when matchMedia change event fires with matches=true", () => {
      const { matchMediaMock, listeners } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock as unknown as typeof window.matchMedia;

      render(
        <AgentsSidebar
          viewSwitcher={<div data-testid="sidebar-content">Content</div>}
        />,
      );

      // Initially expanded
      expect(screen.getByTestId("sidebar-content")).toBeInTheDocument();

      // Simulate viewport shrinking below 1024px
      act(() => {
        for (const listener of listeners) {
          listener({ matches: true } as MediaQueryListEvent);
        }
      });

      // Sidebar should now be collapsed
      expect(screen.queryByTestId("sidebar-content")).not.toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: /expand agents sidebar/i }),
      ).toBeInTheDocument();
    });

    it("registers and cleans up the matchMedia event listener", () => {
      const { matchMediaMock, mql } = createMockMatchMedia(false);
      window.matchMedia = matchMediaMock as unknown as typeof window.matchMedia;

      const { unmount } = render(<AgentsSidebar />);

      // addEventListener should have been called with "change"
      expect(mql.addEventListener).toHaveBeenCalledWith(
        "change",
        expect.any(Function),
      );

      unmount();

      // removeEventListener should have been called on cleanup
      expect(mql.removeEventListener).toHaveBeenCalledWith(
        "change",
        expect.any(Function),
      );
    });

    it("does not crash when matchMedia is not available", () => {
      // Remove matchMedia entirely to simulate environments without it
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (window as any).matchMedia = undefined;

      // Should not throw
      expect(() => {
        render(
          <AgentsSidebar
            viewSwitcher={<div data-testid="sidebar-content">Content</div>}
          />,
        );
      }).not.toThrow();

      // Sidebar should render normally (expanded by default)
      expect(screen.getByTestId("sidebar-content")).toBeInTheDocument();
    });
  });
});
