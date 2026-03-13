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
import { AgentsSidebar } from "../AgentsSidebar";

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
vi.mock("@/hooks", () => ({
  useAgentContext: () => ({ ...defaultMockContext, ...mockContextOverride }),
}));

describe("AgentsSidebar", () => {
  beforeEach(() => {
    localStorage.clear();
    mockContextOverride = {};
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

      const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response(JSON.stringify({ results: [], pushed: 1, failed: 0 }), {
          status: 200,
        }),
      );

      render(<AgentsSidebar />);

      const pushButton = screen.getByRole("button", { name: /push all/i });
      fireEvent.click(pushButton);

      expect(fetchSpy).toHaveBeenCalledWith("/api/git/push-all", {
        method: "POST",
      });

      fetchSpy.mockRestore();
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
});
