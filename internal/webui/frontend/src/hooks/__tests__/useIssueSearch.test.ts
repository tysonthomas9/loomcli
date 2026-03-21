/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useIssueSearch hook.
 */

import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import type { Issue } from "@/types";

// Mock the API
vi.mock("@/api", () => ({
  getKanbanIssues: vi.fn(),
}));

/** Helper to create a test issue */
function createIssue(id: string, title: string): Issue {
  return {
    id,
    title,
    priority: 2,
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
  } as Issue;
}

const testIssues: Issue[] = [
  createIssue("issue-1", "Fix login bug"),
  createIssue("issue-2", "Add user dashboard"),
  createIssue("issue-3", "Update API docs"),
  createIssue("PROJ-42", "Refactor auth module"),
];

describe("useIssueSearch", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.resetModules();
  });

  async function setupHook(
    mockIssues: Issue[] = testIssues,
    shouldReject = false,
  ) {
    // Re-import after module reset to get fresh state
    const apiModule = await import("@/api");
    const hookModule = await import("../useIssueSearch");
    const mockGetKanbanIssues = vi.mocked(apiModule.getKanbanIssues);

    if (shouldReject) {
      mockGetKanbanIssues.mockRejectedValue(new Error("Network error"));
    } else {
      mockGetKanbanIssues.mockResolvedValue(mockIssues);
    }

    const hookResult = renderHook(() => hookModule.useIssueSearch());
    return { ...hookResult, mockGetKanbanIssues };
  }

  describe("initial state", () => {
    it("starts in loading state", async () => {
      const { result } = await setupHook();

      // Initial state is loading
      expect(result.current.isLoading).toBe(true);
    });

    it("returns empty results initially", async () => {
      const { result } = await setupHook();

      expect(result.current.results).toEqual([]);
    });

    it("has empty query initially", async () => {
      const { result } = await setupHook();

      expect(result.current.query).toBe("");
    });
  });

  describe("loading state", () => {
    it("sets isLoading to false after fetch completes", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });
    });

    it("calls getKanbanIssues on mount", async () => {
      const { mockGetKanbanIssues } = await setupHook();

      await waitFor(() => {
        expect(mockGetKanbanIssues).toHaveBeenCalledTimes(1);
      });
    });

    it("sets isLoading to false even on API error", async () => {
      const { result } = await setupHook([], true);

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });
    });

    it("returns empty results on API error", async () => {
      const { result } = await setupHook([], true);

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("fix");
      });

      expect(result.current.results).toEqual([]);
    });
  });

  describe("search filtering", () => {
    it("returns empty results for empty query", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("");
      });

      expect(result.current.results).toEqual([]);
    });

    it("returns empty results for whitespace-only query", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("   ");
      });

      expect(result.current.results).toEqual([]);
    });

    it("filters by title substring", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("login");
      });

      expect(result.current.results).toHaveLength(1);
      expect(result.current.results[0]!.id).toBe("issue-1");
    });

    it("filters by ID substring", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("PROJ");
      });

      expect(result.current.results).toHaveLength(1);
      expect(result.current.results[0]!.id).toBe("PROJ-42");
    });

    it("search is case-insensitive", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("FIX LOGIN");
      });

      expect(result.current.results).toHaveLength(1);
      expect(result.current.results[0]!.id).toBe("issue-1");
    });

    it("returns multiple matches", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("issue");
      });

      expect(result.current.results).toHaveLength(3);
    });

    it("returns no results when query does not match", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("nonexistent-xyz");
      });

      expect(result.current.results).toEqual([]);
    });

    it("updates query state when search is called", async () => {
      const { result } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      act(() => {
        result.current.search("dashboard");
      });

      expect(result.current.query).toBe("dashboard");
    });
  });

  describe("search function stability", () => {
    it("search function reference is stable across renders", async () => {
      const { result, rerender } = await setupHook();

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      const search1 = result.current.search;

      rerender();

      expect(result.current.search).toBe(search1);
    });
  });
});
