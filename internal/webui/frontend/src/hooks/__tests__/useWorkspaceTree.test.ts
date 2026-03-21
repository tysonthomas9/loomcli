/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceTree hook.
 * Covers grouping tasks under parent epics, orphan tasks,
 * active/all filtering, empty issues, and exclusion of non-epic/non-task issues.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import type { Issue } from "@/types";

import { useWorkspaceTree } from "../useWorkspaceTree";

// Mock useIssues which is the data source for useWorkspaceTree.
const mockRefetch = vi.fn().mockResolvedValue(undefined);

vi.mock("../useIssues", () => ({
  useIssues: vi.fn(() => ({
    issues: [] as Issue[],
    issuesMap: new Map<string, Issue>(),
    isLoading: false,
    error: null,
    connectionState: "connected",
    isConnected: true,
    reconnectAttempts: 0,
    lastEventId: undefined,
    refetch: mockRefetch,
    updateIssueStatus: vi.fn(),
    getIssue: vi.fn(),
    mutationCount: 0,
    retryConnection: vi.fn(),
    showStaleBanner: false,
    connectionLost: false,
    disconnectedSince: null,
    pendingIds: new Set<string>(),
  })),
}));

import { useIssues } from "../useIssues";

const mockUseIssues = vi.mocked(useIssues);

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

function setMockIssues(issues: Issue[]): void {
  mockUseIssues.mockReturnValue({
    issues,
    issuesMap: new Map(issues.map((i) => [i.id, i])),
    isLoading: false,
    error: null,
    connectionState: "connected",
    isConnected: true,
    reconnectAttempts: 0,
    lastEventId: undefined,
    refetch: mockRefetch,
    updateIssueStatus: vi.fn(),
    getIssue: vi.fn(),
    mutationCount: 0,
    retryConnection: vi.fn(),
    showStaleBanner: false,
    connectionLost: false,
    disconnectedSince: null,
    pendingIds: new Set<string>(),
  });
}

describe("useWorkspaceTree", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("grouping tasks under parent epics", () => {
    it("groups tasks under their parent epic", () => {
      const epic = makeIssue({
        id: "epic-1",
        title: "Epic 1",
        issue_type: "epic",
      });
      const task1 = makeIssue({
        id: "task-1",
        title: "Task 1",
        issue_type: "task",
        parent: "epic-1",
        status: "open",
      });
      const task2 = makeIssue({
        id: "task-2",
        title: "Task 2",
        issue_type: "task",
        parent: "epic-1",
        status: "in_progress",
      });

      setMockIssues([epic, task1, task2]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(1);
      expect(result.current.epics[0].epic.id).toBe("epic-1");
      expect(result.current.epics[0].tasks).toHaveLength(2);
      expect(result.current.epics[0].tasks.map((t) => t.id)).toEqual([
        "task-1",
        "task-2",
      ]);
      expect(result.current.orphanTasks).toHaveLength(0);
    });

    it("groups tasks under correct epics when multiple epics exist", () => {
      const epicA = makeIssue({
        id: "epic-a",
        title: "Epic A",
        issue_type: "epic",
      });
      const epicB = makeIssue({
        id: "epic-b",
        title: "Epic B",
        issue_type: "epic",
      });
      const taskA1 = makeIssue({
        id: "task-a1",
        title: "Task A1",
        issue_type: "task",
        parent: "epic-a",
        status: "open",
      });
      const taskB1 = makeIssue({
        id: "task-b1",
        title: "Task B1",
        issue_type: "task",
        parent: "epic-b",
        status: "open",
      });
      const taskB2 = makeIssue({
        id: "task-b2",
        title: "Task B2",
        issue_type: "task",
        parent: "epic-b",
        status: "open",
      });

      setMockIssues([epicA, epicB, taskA1, taskB1, taskB2]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(2);
      const epicAResult = result.current.epics.find(
        (e) => e.epic.id === "epic-a",
      );
      const epicBResult = result.current.epics.find(
        (e) => e.epic.id === "epic-b",
      );
      expect(epicAResult?.tasks).toHaveLength(1);
      expect(epicBResult?.tasks).toHaveLength(2);
    });
  });

  describe("orphan tasks", () => {
    it("treats tasks with no parent as orphans", () => {
      const epic = makeIssue({
        id: "epic-1",
        title: "Epic 1",
        issue_type: "epic",
      });
      const orphan = makeIssue({
        id: "task-orphan",
        title: "Orphan Task",
        issue_type: "task",
        status: "open",
      });

      setMockIssues([epic, orphan]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.orphanTasks).toHaveLength(1);
      expect(result.current.orphanTasks[0].id).toBe("task-orphan");
    });

    it("treats tasks whose parent is not in the epic set as orphans", () => {
      const epic = makeIssue({
        id: "epic-1",
        title: "Epic 1",
        issue_type: "epic",
      });
      const task = makeIssue({
        id: "task-1",
        title: "Task 1",
        issue_type: "task",
        parent: "epic-missing",
        status: "open",
      });

      setMockIssues([epic, task]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.orphanTasks).toHaveLength(1);
      expect(result.current.orphanTasks[0].id).toBe("task-1");
      // The existing epic should have no tasks
      expect(result.current.epics[0].tasks).toHaveLength(0);
    });
  });

  describe("active filter", () => {
    it("only includes epics with in_progress or review tasks", () => {
      const epicActive = makeIssue({
        id: "epic-active",
        title: "Active Epic",
        issue_type: "epic",
      });
      const epicInactive = makeIssue({
        id: "epic-inactive",
        title: "Inactive Epic",
        issue_type: "epic",
      });
      const taskInProgress = makeIssue({
        id: "task-ip",
        title: "In Progress Task",
        issue_type: "task",
        parent: "epic-active",
        status: "in_progress",
      });
      const taskOpen = makeIssue({
        id: "task-open",
        title: "Open Task",
        issue_type: "task",
        parent: "epic-inactive",
        status: "open",
      });

      setMockIssues([epicActive, epicInactive, taskInProgress, taskOpen]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "active", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(1);
      expect(result.current.epics[0].epic.id).toBe("epic-active");
      expect(result.current.epics[0].tasks).toHaveLength(1);
    });

    it("filters out non-active tasks from active epics", () => {
      const epic = makeIssue({
        id: "epic-1",
        title: "Epic 1",
        issue_type: "epic",
      });
      const taskReview = makeIssue({
        id: "task-review",
        title: "Review Task",
        issue_type: "task",
        parent: "epic-1",
        status: "review",
      });
      const taskOpen = makeIssue({
        id: "task-open",
        title: "Open Task",
        issue_type: "task",
        parent: "epic-1",
        status: "open",
      });
      const taskClosed = makeIssue({
        id: "task-closed",
        title: "Closed Task",
        issue_type: "task",
        parent: "epic-1",
        status: "closed",
      });

      setMockIssues([epic, taskReview, taskOpen, taskClosed]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "active", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(1);
      // Only the review task should be included
      expect(result.current.epics[0].tasks).toHaveLength(1);
      expect(result.current.epics[0].tasks[0].id).toBe("task-review");
    });

    it("filters orphan tasks to only active statuses", () => {
      const orphanActive = makeIssue({
        id: "orphan-active",
        title: "Active Orphan",
        issue_type: "task",
        status: "in_progress",
      });
      const orphanOpen = makeIssue({
        id: "orphan-open",
        title: "Open Orphan",
        issue_type: "task",
        status: "open",
      });

      setMockIssues([orphanActive, orphanOpen]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "active", ["repo-1"]),
      );

      expect(result.current.orphanTasks).toHaveLength(1);
      expect(result.current.orphanTasks[0].id).toBe("orphan-active");
    });
  });

  describe("all filter", () => {
    it("includes all epics regardless of task status", () => {
      const epic1 = makeIssue({
        id: "epic-1",
        title: "Epic 1",
        issue_type: "epic",
      });
      const epic2 = makeIssue({
        id: "epic-2",
        title: "Epic 2",
        issue_type: "epic",
      });
      const taskOpen = makeIssue({
        id: "task-open",
        title: "Open Task",
        issue_type: "task",
        parent: "epic-1",
        status: "open",
      });
      const taskClosed = makeIssue({
        id: "task-closed",
        title: "Closed Task",
        issue_type: "task",
        parent: "epic-2",
        status: "closed",
      });

      setMockIssues([epic1, epic2, taskOpen, taskClosed]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(2);
      expect(result.current.epics[0].tasks).toHaveLength(1);
      expect(result.current.epics[1].tasks).toHaveLength(1);
    });

    it("includes epics with no tasks", () => {
      const epic = makeIssue({
        id: "epic-empty",
        title: "Empty Epic",
        issue_type: "epic",
      });

      setMockIssues([epic]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(1);
      expect(result.current.epics[0].epic.id).toBe("epic-empty");
      expect(result.current.epics[0].tasks).toHaveLength(0);
    });
  });

  describe("empty issues", () => {
    it("returns empty arrays when no issues exist", () => {
      setMockIssues([]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(0);
      expect(result.current.orphanTasks).toHaveLength(0);
    });
  });

  describe("exclusion of non-epic/non-task issues", () => {
    it("excludes issues with issue_type other than epic or task", () => {
      const epic = makeIssue({
        id: "epic-1",
        title: "Epic 1",
        issue_type: "epic",
      });
      const task = makeIssue({
        id: "task-1",
        title: "Task 1",
        issue_type: "task",
        parent: "epic-1",
        status: "open",
      });
      // Issues with other types should be excluded
      const molecule = makeIssue({
        id: "mol-1",
        title: "Molecule 1",
        issue_type: "molecule",
      });
      const noType = makeIssue({ id: "no-type", title: "No Type" });

      setMockIssues([epic, task, molecule, noType]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.epics).toHaveLength(1);
      expect(result.current.epics[0].tasks).toHaveLength(1);
      expect(result.current.orphanTasks).toHaveLength(0);
    });
  });

  describe("return value", () => {
    it("passes through isLoading from useIssues", () => {
      mockUseIssues.mockReturnValue({
        issues: [],
        issuesMap: new Map(),
        isLoading: true,
        error: null,
        connectionState: "loading",
        isConnected: false,
        reconnectAttempts: 0,
        lastEventId: undefined,
        refetch: mockRefetch,
        updateIssueStatus: vi.fn(),
        getIssue: vi.fn(),
        mutationCount: 0,
        retryConnection: vi.fn(),
        showStaleBanner: false,
        connectionLost: false,
        disconnectedSince: null,
        pendingIds: new Set<string>(),
      });

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.isLoading).toBe(true);
    });

    it("passes through error from useIssues", () => {
      mockUseIssues.mockReturnValue({
        issues: [],
        issuesMap: new Map(),
        isLoading: false,
        error: "fetch failed",
        connectionState: "error_never_connected",
        isConnected: false,
        reconnectAttempts: 0,
        lastEventId: undefined,
        refetch: mockRefetch,
        updateIssueStatus: vi.fn(),
        getIssue: vi.fn(),
        mutationCount: 0,
        retryConnection: vi.fn(),
        showStaleBanner: false,
        connectionLost: false,
        disconnectedSince: null,
        pendingIds: new Set<string>(),
      });

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.error).toBe("fetch failed");
    });

    it("passes through refetch from useIssues", () => {
      setMockIssues([]);

      const { result } = renderHook(() =>
        useWorkspaceTree("ws-1", "all", ["repo-1"]),
      );

      expect(result.current.refetch).toBe(mockRefetch);
    });
  });
});
