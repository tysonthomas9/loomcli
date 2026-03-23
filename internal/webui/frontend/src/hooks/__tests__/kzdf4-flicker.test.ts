/**
 * @vitest-environment jsdom
 */

/**
 * Tests for kzdf4 flicker fix: debounced MutationRefresh refetch,
 * extended applyUpdateToIssue fields, and structural diff on refetch.
 */

import { renderHook, act, waitFor } from "@testing-library/react";
import type React from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useIssues } from "../useIssues";
import * as useSSEModule from "../useSSE";
import * as issuesApi from "../../api/issues";
import type { ConnectionState, MutationPayload } from "../../api/sse";
import type { Issue } from "../../types/issue";

// Mock the API
vi.mock("../../api/issues", () => ({
  getReadyIssues: vi.fn(),
  getKanbanIssues: vi.fn(),
  updateIssue: vi.fn(),
  fetchGraphIssues: vi.fn(),
}));

// Mock useSSE
vi.mock("../useSSE", () => ({
  useSSE: vi.fn(),
}));

// Mock useToast
vi.mock("../useToast", () => ({
  useToast: () => ({
    toasts: [],
    showToast: vi.fn(),
    removeToast: vi.fn(),
  }),
  ToastProvider: ({ children }: { children: React.ReactNode }) => children,
}));

function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-issue-1",
    title: "Test Issue",
    priority: 2,
    created_at: "2025-01-23T10:00:00Z",
    updated_at: "2025-01-23T10:00:00Z",
    ...overrides,
  };
}

function createMockSSE(
  overrides: Partial<useSSEModule.UseSSEReturn> = {},
): useSSEModule.UseSSEReturn {
  return {
    state: "disconnected" as ConnectionState,
    lastError: null,
    isConnected: false,
    reconnectAttempts: 0,
    lastEventId: undefined,
    connect: vi.fn(),
    disconnect: vi.fn(),
    retryNow: vi.fn(),
    ...overrides,
  };
}

describe("kzdf4 flicker fixes", () => {
  let mockSSE: useSSEModule.UseSSEReturn;
  let onMutationCallback: ((mutation: MutationPayload) => void) | undefined;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();

    mockSSE = createMockSSE();
    vi.mocked(useSSEModule.useSSE).mockImplementation((options) => {
      onMutationCallback = options?.onMutation;
      return mockSSE;
    });

    vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
    onMutationCallback = undefined;
  });

  describe("MutationRefresh debounce", () => {
    it("debounces multiple MutationRefresh events into a single refetch", async () => {
      const issue = createTestIssue();
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([issue]);

      const { result } = renderHook(() => useIssues({ autoFetch: true }));

      // Wait for initial fetch
      await act(async () => {
        await vi.runAllTimersAsync();
      });

      // Reset mock to track new calls
      vi.mocked(issuesApi.getReadyIssues).mockClear();
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([issue]);

      // Fire 5 MutationRefresh events rapidly
      act(() => {
        for (let i = 0; i < 5; i++) {
          onMutationCallback!({
            type: "refresh",
            issue_id: "",
            timestamp: new Date().toISOString(),
          });
        }
      });

      // Before debounce timer fires, no refetch should have happened
      expect(vi.mocked(issuesApi.getReadyIssues)).not.toHaveBeenCalled();

      // Advance past the 1-second debounce window
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1100);
      });

      // Only 1 refetch should have fired (debounced)
      expect(vi.mocked(issuesApi.getReadyIssues)).toHaveBeenCalledTimes(1);
    });

    it("fires refetch after debounce period with no new events", async () => {
      const issue = createTestIssue();
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([issue]);

      const { result } = renderHook(() => useIssues({ autoFetch: true }));

      await act(async () => {
        await vi.runAllTimersAsync();
      });

      vi.mocked(issuesApi.getReadyIssues).mockClear();
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([issue]);

      // Fire a single refresh event
      act(() => {
        onMutationCallback!({
          type: "refresh",
          issue_id: "",
          timestamp: new Date().toISOString(),
        });
      });

      // Advance past debounce
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1100);
      });

      expect(vi.mocked(issuesApi.getReadyIssues)).toHaveBeenCalledTimes(1);
    });
  });

  describe("structural diff on refetch merge", () => {
    it("preserves object references for unchanged issues during refetch", async () => {
      const issue = createTestIssue({ id: "issue-1", title: "Unchanged" });
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([issue]);

      const { result } = renderHook(() => useIssues({ autoFetch: true }));

      // Wait for initial fetch
      await act(async () => {
        await vi.runAllTimersAsync();
      });

      // Capture the reference
      const firstIssue = result.current.issuesMap.get("issue-1");
      expect(firstIssue).toBeDefined();

      // Refetch with same data (new object but same values)
      const sameDateIssue = createTestIssue({
        id: "issue-1",
        title: "Unchanged",
      });
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([sameDateIssue]);

      await act(async () => {
        await result.current.refetch();
        await vi.runAllTimersAsync();
      });

      // Object reference should be preserved (same object, not replaced)
      const secondIssue = result.current.issuesMap.get("issue-1");
      expect(secondIssue).toBe(firstIssue);
    });

    it("replaces object reference when issue data changes", async () => {
      const issue = createTestIssue({ id: "issue-1", title: "Original" });
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([issue]);

      const { result } = renderHook(() => useIssues({ autoFetch: true }));

      await act(async () => {
        await vi.runAllTimersAsync();
      });

      const firstIssue = result.current.issuesMap.get("issue-1");

      // Refetch with changed data
      const changedIssue = createTestIssue({
        id: "issue-1",
        title: "Updated",
        updated_at: "2025-01-23T12:00:00Z",
      });
      vi.mocked(issuesApi.getReadyIssues).mockResolvedValue([changedIssue]);

      await act(async () => {
        await result.current.refetch();
        await vi.runAllTimersAsync();
      });

      const secondIssue = result.current.issuesMap.get("issue-1");
      expect(secondIssue).not.toBe(firstIssue);
      expect(secondIssue?.title).toBe("Updated");
    });
  });
});
