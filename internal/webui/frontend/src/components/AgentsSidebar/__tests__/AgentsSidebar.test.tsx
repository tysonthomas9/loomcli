/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentsSidebar component.
 * Focuses on the viewSwitcher slot behavior and sync status rendering.
 */

import { render, screen, fireEvent, within } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

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

    it("push button calls POST /api/git/push-all on click", async () => {
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

      const pushButton = screen.getByRole("button", { name: /push all/i });
      fireEvent.click(pushButton);

      expect(mockGitPushAll).toHaveBeenCalled();

      mockGitPushAll.mockReset();
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
});
