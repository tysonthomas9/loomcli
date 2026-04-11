/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EpicTaskTree component.
 * Covers TalkToLeadEntry rendering, EpicRow rendering,
 * empty state messages, ungrouped orphan tasks, and loading skeleton.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";
import type { EpicWithTasks } from "@/hooks/workspace";

import { EpicTaskTree } from "../EpicTaskTree";

// Mock useWorkspaceTree which the component uses internally.
const mockRefetch = vi.fn().mockResolvedValue(undefined);

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceTree: vi.fn(() => ({
      epics: [] as EpicWithTasks[],
      orphanTasks: [] as Issue[],
      isLoading: false,
      error: null,
      refetch: mockRefetch,
    })),
  };
});

// Mock useIssueDiffStat and useToast since TaskRow and EpicTaskTree use them.
vi.mock("@/hooks", () => ({
  useIssueDiffStat: vi.fn(() => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
}));

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return {
    ...actual,
    useToast: vi.fn(() => ({
      showToast: vi.fn(),
    })),
  };
});

import { useWorkspaceTree } from "@/hooks/workspace";

const mockUseWorkspaceTree = vi.mocked(useWorkspaceTree);

/** Helper to create a test Issue. */
function makeIssue(
  overrides: Partial<Issue> & { id: string; title: string },
): Issue {
  return {
    priority: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function setMockTree(
  epics: EpicWithTasks[],
  orphanTasks: Issue[],
  isLoading = false,
  error: string | null = null,
): void {
  mockUseWorkspaceTree.mockReturnValue({
    epics,
    orphanTasks,
    isLoading,
    error,
    refetch: mockRefetch,
  });
}

describe("EpicTaskTree", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset to default empty state
    setMockTree([], [], false);
    // Clear localStorage to avoid persisted collapse state leaking between tests
    localStorage.clear();
  });

  describe("TalkToLeadEntry rendering", () => {
    it("renders TalkToLeadEntry", () => {
      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.getByText("Talk to Lead")).toBeInTheDocument();
    });

    it("renders TalkToLeadEntry with custom backend", () => {
      render(
        <EpicTaskTree
          workspaceName="ws-1"
          activeFilter="all"
          backend="gemini"
        />,
      );
      expect(screen.getByText("gemini")).toBeInTheDocument();
    });

    it("renders TalkToLeadEntry even during loading", () => {
      setMockTree([], [], true);
      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.getByText("Talk to Lead")).toBeInTheDocument();
    });
  });

  describe("EpicRow rendering", () => {
    it("renders an EpicRow for each epic", () => {
      const epics: EpicWithTasks[] = [
        {
          epic: makeIssue({
            id: "epic-1",
            title: "Epic Alpha",
            issue_type: "epic",
          }),
          tasks: [],
        },
        {
          epic: makeIssue({
            id: "epic-2",
            title: "Epic Beta",
            issue_type: "epic",
          }),
          tasks: [],
        },
      ];
      setMockTree(epics, []);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.getByText("Epic Alpha")).toBeInTheDocument();
      expect(screen.getByText("Epic Beta")).toBeInTheDocument();
    });

    it("renders tasks within an expanded epic", () => {
      const epics: EpicWithTasks[] = [
        {
          epic: makeIssue({
            id: "epic-1",
            title: "Epic Alpha",
            issue_type: "epic",
          }),
          tasks: [
            makeIssue({
              id: "task-1",
              title: "Task Under Epic",
              issue_type: "task",
              status: "open",
              parent: "epic-1",
            }),
          ],
        },
      ];
      setMockTree(epics, []);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      // Epics start expanded by default (isCollapsed defaults to false from empty collapse state)
      expect(screen.getByText("Task Under Epic")).toBeInTheDocument();
    });
  });

  describe("empty state messages", () => {
    it("shows 'No active tasks' when activeFilter is 'active' and no content", () => {
      setMockTree([], []);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="active" />);
      expect(screen.getByText("No active tasks")).toBeInTheDocument();
    });

    it("shows 'No epics or tasks' when activeFilter is 'all' and no content", () => {
      setMockTree([], []);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.getByText("No epics or tasks")).toBeInTheDocument();
    });

    it("does not show empty message when there are epics", () => {
      const epics: EpicWithTasks[] = [
        {
          epic: makeIssue({
            id: "epic-1",
            title: "Epic 1",
            issue_type: "epic",
          }),
          tasks: [],
        },
      ];
      setMockTree(epics, []);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.queryByText("No epics or tasks")).not.toBeInTheDocument();
    });

    it("does not show empty message when there are orphan tasks", () => {
      const orphans = [
        makeIssue({
          id: "orphan-1",
          title: "Orphan Task",
          issue_type: "task",
          status: "open",
        }),
      ];
      setMockTree([], orphans);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.queryByText("No epics or tasks")).not.toBeInTheDocument();
    });
  });

  describe("orphan tasks in Ungrouped section", () => {
    it("renders Ungrouped section when orphan tasks exist", () => {
      const orphans = [
        makeIssue({
          id: "orphan-1",
          title: "Stray Task",
          issue_type: "task",
          status: "open",
        }),
      ];
      setMockTree([], orphans);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.getByText("Ungrouped")).toBeInTheDocument();
      expect(screen.getByText("Stray Task")).toBeInTheDocument();
    });

    it("does not render Ungrouped section when there are no orphan tasks", () => {
      setMockTree([], []);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
    });

    it("toggles orphan section visibility on click", () => {
      const orphans = [
        makeIssue({
          id: "orphan-1",
          title: "Stray Task",
          issue_type: "task",
          status: "open",
        }),
      ];
      setMockTree([], orphans);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);

      // Initially expanded
      expect(screen.getByText("Stray Task")).toBeInTheDocument();

      // Click to collapse
      fireEvent.click(screen.getByTitle("Ungrouped tasks"));
      expect(screen.queryByText("Stray Task")).not.toBeInTheDocument();

      // Click to expand again
      fireEvent.click(screen.getByTitle("Ungrouped tasks"));
      expect(screen.getByText("Stray Task")).toBeInTheDocument();
    });
  });

  describe("loading skeleton", () => {
    it("renders loading skeleton rows when isLoading is true", () => {
      setMockTree([], [], true);

      const { container } = render(
        <EpicTaskTree workspaceName="ws-1" activeFilter="all" />,
      );
      const skeletonRows = container.querySelectorAll("[class*='skeletonRow']");
      expect(skeletonRows.length).toBe(3);
    });

    it("does not render epics or empty state when loading", () => {
      setMockTree([], [], true);

      render(<EpicTaskTree workspaceName="ws-1" activeFilter="all" />);
      expect(screen.queryByText("No epics or tasks")).not.toBeInTheDocument();
    });
  });

  describe("callbacks", () => {
    it("calls onTalkToLead on TalkToLeadEntry click", () => {
      const onTalkToLead = vi.fn();
      render(
        <EpicTaskTree
          workspaceName="ws-1"
          activeFilter="all"
          onTalkToLead={onTalkToLead}
        />,
      );
      fireEvent.click(screen.getByText("Talk to Lead"));
      expect(onTalkToLead).toHaveBeenCalledWith("ws-1");
    });

    it("calls onSelect when a task is clicked", () => {
      const epics: EpicWithTasks[] = [
        {
          epic: makeIssue({
            id: "epic-1",
            title: "Epic 1",
            issue_type: "epic",
          }),
          tasks: [
            makeIssue({
              id: "task-1",
              title: "Clickable Task",
              issue_type: "task",
              status: "open",
              parent: "epic-1",
            }),
          ],
        },
      ];
      setMockTree(epics, []);

      const onSelect = vi.fn();
      render(
        <EpicTaskTree
          workspaceName="ws-1"
          activeFilter="all"
          onSelect={onSelect}
        />,
      );
      fireEvent.click(screen.getByText("Clickable Task"));
      expect(onSelect).toHaveBeenCalledWith("task-1");
    });
  });
});
