/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useIssueTabPersistence hook.
 * Covers initial loading, fetch, debounced save, clear, mutation handling, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  fetchIssueTabState,
  saveIssueTabState,
  deleteIssueTabState,
} from "@/api/issueTabs";
import type { IssueTabState, IssueTab } from "@/api/issueTabs";
import type { MutationPayload } from "@/api/sse";

import { useIssueTabPersistence } from "../useIssueTabPersistence";

vi.mock("@/api/issueTabs", () => ({
  fetchIssueTabState: vi.fn(),
  saveIssueTabState: vi.fn(),
  deleteIssueTabState: vi.fn(),
}));

const mockFetch = vi.mocked(fetchIssueTabState);
const mockSave = vi.mocked(saveIssueTabState);
const mockDeleteState = vi.mocked(deleteIssueTabState);

function createMockState(overrides?: Partial<IssueTabState>): IssueTabState {
  return {
    issue_id: "PROJ-1",
    tabs: [{ id: "details", type: "details", label: "Details", sort_order: 0 }],
    active_tab_id: "details",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useIssueTabPersistence", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("starts with isLoading true and null savedState", async () => {
      mockFetch.mockResolvedValueOnce(null);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      expect(result.current.isLoading).toBe(true);
      expect(result.current.savedState).toBeNull();

      await flushPromises();
    });
  });

  describe("fetching", () => {
    it("fetches state on mount and updates savedState", async () => {
      const state = createMockState();
      mockFetch.mockResolvedValueOnce(state);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      expect(mockFetch).toHaveBeenCalledWith("PROJ-1");
      expect(result.current.savedState).toEqual(state);
      expect(result.current.isLoading).toBe(false);
    });

    it("sets savedState to null on fetch error (silent fail)", async () => {
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      expect(result.current.savedState).toBeNull();
      expect(result.current.isLoading).toBe(false);
    });

    it("refetches when issueId changes", async () => {
      mockFetch.mockResolvedValueOnce(createMockState({ issue_id: "PROJ-1" }));

      const { result, rerender } = renderHook(
        ({ id }: { id: string }) => useIssueTabPersistence(id),
        { initialProps: { id: "PROJ-1" } },
      );

      await flushPromises();
      expect(result.current.savedState?.issue_id).toBe("PROJ-1");

      mockFetch.mockResolvedValueOnce(createMockState({ issue_id: "PROJ-2" }));

      rerender({ id: "PROJ-2" });
      await flushPromises();

      expect(mockFetch).toHaveBeenLastCalledWith("PROJ-2");
    });

    it("does not fetch when issueId is empty", async () => {
      const { result } = renderHook(() => useIssueTabPersistence(""));

      await flushPromises();

      expect(mockFetch).not.toHaveBeenCalled();
      expect(result.current.savedState).toBeNull();
    });
  });

  describe("saveTabs", () => {
    it("debounces save calls", async () => {
      mockFetch.mockResolvedValueOnce(null);
      mockSave.mockResolvedValue(undefined);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      const tabs: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
      ];

      act(() => {
        result.current.saveTabs(tabs, "details");
      });

      // Should not have saved yet (debounce delay is 300ms)
      expect(mockSave).not.toHaveBeenCalled();

      await act(async () => {
        vi.advanceTimersByTime(300);
      });

      expect(mockSave).toHaveBeenCalledWith("PROJ-1", tabs, "details");
    });

    it("collapses rapid saves into one call", async () => {
      mockFetch.mockResolvedValueOnce(null);
      mockSave.mockResolvedValue(undefined);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      const tabs1: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
      ];
      const tabs2: IssueTab[] = [
        { id: "details", type: "details", label: "Details", sort_order: 0 },
        { id: "logs", type: "logs", label: "Logs", sort_order: 1 },
      ];

      act(() => {
        result.current.saveTabs(tabs1, "details");
      });

      await act(async () => {
        vi.advanceTimersByTime(100);
      });

      act(() => {
        result.current.saveTabs(tabs2, "logs");
      });

      await act(async () => {
        vi.advanceTimersByTime(300);
      });

      // Only the second save should have fired
      expect(mockSave).toHaveBeenCalledTimes(1);
      expect(mockSave).toHaveBeenCalledWith("PROJ-1", tabs2, "logs");
    });
  });

  describe("clearTabs", () => {
    it("calls deleteIssueTabState and clears savedState", async () => {
      mockFetch.mockResolvedValueOnce(createMockState());
      mockDeleteState.mockResolvedValue(undefined);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();
      expect(result.current.savedState).not.toBeNull();

      act(() => {
        result.current.clearTabs();
      });

      expect(mockDeleteState).toHaveBeenCalledWith("PROJ-1");
      expect(result.current.savedState).toBeNull();
    });

    it("does nothing when issueId is empty", async () => {
      mockFetch.mockResolvedValueOnce(null);

      const { result } = renderHook(() => useIssueTabPersistence(""));

      await flushPromises();

      act(() => {
        result.current.clearTabs();
      });

      expect(mockDeleteState).not.toHaveBeenCalled();
    });
  });

  describe("handleMutation", () => {
    it("ignores mutations with wrong type", async () => {
      mockFetch.mockResolvedValueOnce(null);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      const callCount = mockFetch.mock.calls.length;

      act(() => {
        result.current.handleMutation({
          type: "create",
          issue_id: "PROJ-1",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      await act(async () => {
        vi.advanceTimersByTime(200);
      });

      expect(mockFetch).toHaveBeenCalledTimes(callCount);
    });

    it("ignores issue_tabs mutations for different issueId", async () => {
      mockFetch.mockResolvedValueOnce(null);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      const callCount = mockFetch.mock.calls.length;

      act(() => {
        result.current.handleMutation({
          type: "issue_tabs",
          issue_id: "PROJ-OTHER",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      await act(async () => {
        vi.advanceTimersByTime(200);
      });

      expect(mockFetch).toHaveBeenCalledTimes(callCount);
    });

    it("triggers debounced refetch for matching issue_tabs mutation", async () => {
      mockFetch.mockResolvedValueOnce(null);

      const { result } = renderHook(() => useIssueTabPersistence("PROJ-1"));

      await flushPromises();

      const callCount = mockFetch.mock.calls.length;
      mockFetch.mockResolvedValueOnce(createMockState());

      act(() => {
        result.current.handleMutation({
          type: "issue_tabs",
          issue_id: "PROJ-1",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      expect(mockFetch).toHaveBeenCalledTimes(callCount);

      await act(async () => {
        vi.advanceTimersByTime(100);
      });
      await flushPromises();

      expect(mockFetch).toHaveBeenCalledTimes(callCount + 1);
    });
  });

  describe("cleanup", () => {
    it("does not update state after unmount", async () => {
      // First mount loads successfully
      mockFetch.mockResolvedValueOnce(createMockState());

      const { result, unmount } = renderHook(() =>
        useIssueTabPersistence("PROJ-1"),
      );

      await flushPromises();
      expect(result.current.savedState).not.toBeNull();

      // Unmount the hook
      unmount();

      // savedState at time of unmount is preserved (no crash)
      // The mountedRef prevents further updates
      expect(result.current.isLoading).toBe(false);
    });

    it("clears debounce timers on unmount", async () => {
      mockFetch.mockResolvedValueOnce(null);
      mockSave.mockResolvedValue(undefined);

      const { result, unmount } = renderHook(() =>
        useIssueTabPersistence("PROJ-1"),
      );

      await flushPromises();

      act(() => {
        result.current.saveTabs(
          [{ id: "d", type: "details", label: "D", sort_order: 0 }],
          "d",
        );
      });

      unmount();

      await act(async () => {
        vi.advanceTimersByTime(500);
      });

      expect(mockSave).not.toHaveBeenCalled();
    });
  });
});
