/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useOptimisticUpdate hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useOptimisticUpdate } from "../useOptimisticUpdate";
import type { MutationPayload } from "@/api/sse";
import type { Issue } from "@/types";

/**
 * Helper to create a test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    title: "Test Issue",
    priority: 2,
    created_at: "2025-01-23T10:00:00Z",
    updated_at: "2025-01-23T10:00:00Z",
    ...overrides,
  };
}

/**
 * Helper to create a mutation payload.
 */
function createMutationPayload(
  overrides: Partial<MutationPayload> = {},
): MutationPayload {
  return {
    type: "create",
    issue_id: "issue-1",
    timestamp: "2025-01-23T12:00:00Z",
    ...overrides,
  };
}

/**
 * Helper to set up the hook with default mocks.
 */
function setupHook() {
  const setIssuesMap = vi.fn();
  const handleMutation = vi.fn();
  const showToast = vi.fn().mockReturnValue("toast-1");
  const mountedRef = { current: true };

  const hookResult = renderHook(() =>
    useOptimisticUpdate({
      setIssuesMap,
      handleMutation,
      showToast,
      mountedRef,
    }),
  );

  return { ...hookResult, setIssuesMap, handleMutation, showToast, mountedRef };
}

describe("useOptimisticUpdate", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("initial state", () => {
    it("returns empty pendingIds initially", () => {
      const { result } = setupHook();
      expect(result.current.pendingIds.size).toBe(0);
    });

    it("isOptimistic returns false for any issue initially", () => {
      const { result } = setupHook();
      expect(result.current.isOptimistic("issue-1")).toBe(false);
      expect(result.current.isOptimistic("nonexistent")).toBe(false);
    });

    it("filterMutation returns true for any mutation initially", () => {
      const { result } = setupHook();
      const mutation = createMutationPayload();
      let passThrough: boolean = false;
      act(() => {
        passThrough = result.current.filterMutation(mutation);
      });
      expect(passThrough).toBe(true);
    });
  });

  describe("startOptimistic", () => {
    it("returns a handle with confirm and rollback methods", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      expect(handle).not.toBeNull();
      expect(handle!.confirm).toBeInstanceOf(Function);
      expect(handle!.rollback).toBeInstanceOf(Function);
    });

    it("adds issueId to pendingIds", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(true);
      expect(result.current.pendingIds.size).toBe(1);
    });

    it("returns null if same issueId is already optimistic", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle1: ReturnType<typeof result.current.startOptimistic>;
      let handle2: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle1 = result.current.startOptimistic("issue-1", snapshot);
      });
      act(() => {
        handle2 = result.current.startOptimistic("issue-1", snapshot);
      });

      expect(handle1).not.toBeNull();
      expect(handle2).toBeNull();
    });

    it("allows different issueIds concurrently", () => {
      const { result } = setupHook();
      const snapshot1 = createTestIssue({ id: "issue-1" });
      const snapshot2 = createTestIssue({ id: "issue-2" });

      let handle1: ReturnType<typeof result.current.startOptimistic>;
      let handle2: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle1 = result.current.startOptimistic("issue-1", snapshot1);
        handle2 = result.current.startOptimistic("issue-2", snapshot2);
      });

      expect(handle1).not.toBeNull();
      expect(handle2).not.toBeNull();
      expect(result.current.pendingIds.size).toBe(2);
      expect(result.current.pendingIds.has("issue-1")).toBe(true);
      expect(result.current.pendingIds.has("issue-2")).toBe(true);
    });
  });

  describe("confirm", () => {
    it("removes issueId from pendingIds", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(true);

      act(() => {
        handle!.confirm();
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(false);
      expect(result.current.pendingIds.size).toBe(0);
    });

    it("flushes buffered mutations in order", () => {
      const { result, handleMutation } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      // Buffer some mutations by filtering them
      const mutation1 = createMutationPayload({
        type: "create",
        issue_id: "issue-1",
        timestamp: "2025-01-23T12:01:00Z",
      });
      const mutation2 = createMutationPayload({
        type: "create",
        issue_id: "issue-1",
        timestamp: "2025-01-23T12:02:00Z",
      });

      act(() => {
        result.current.filterMutation(mutation1);
        result.current.filterMutation(mutation2);
      });

      expect(handleMutation).not.toHaveBeenCalled();

      // Confirm flushes them
      act(() => {
        handle!.confirm();
      });

      expect(handleMutation).toHaveBeenCalledTimes(2);
      expect(handleMutation).toHaveBeenNthCalledWith(1, mutation1);
      expect(handleMutation).toHaveBeenNthCalledWith(2, mutation2);
    });

    it("does not restore snapshot on confirm", () => {
      const { result, setIssuesMap } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.confirm();
      });

      // setIssuesMap should not be called on confirm (snapshot is not restored)
      expect(setIssuesMap).not.toHaveBeenCalled();
    });

    it("is a no-op if called after already confirmed", () => {
      const { result, handleMutation } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.confirm();
      });

      // Second confirm should be a no-op
      act(() => {
        handle!.confirm();
      });

      expect(handleMutation).toHaveBeenCalledTimes(0);
    });

    it("does not show a toast on confirm", () => {
      const { result, showToast } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.confirm();
      });

      expect(showToast).not.toHaveBeenCalled();
    });
  });

  describe("rollback", () => {
    it("restores snapshot via setIssuesMap functional update", () => {
      const { result, setIssuesMap } = setupHook();
      const snapshot = createTestIssue({ id: "issue-1", title: "Original" });

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback("Something failed");
      });

      expect(setIssuesMap).toHaveBeenCalledTimes(1);
      const updater = setIssuesMap.mock.calls[0][0];
      expect(typeof updater).toBe("function");

      // Simulate calling the updater
      const currentMap = new Map<string, Issue>();
      currentMap.set("issue-1", createTestIssue({ title: "Modified" }));
      currentMap.set("issue-2", createTestIssue({ id: "issue-2" }));

      const resultMap = updater(currentMap);
      expect(resultMap.get("issue-1")).toEqual(snapshot);
      // Other entries should be preserved
      expect(resultMap.has("issue-2")).toBe(true);
    });

    it("removes issueId from pendingIds", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(true);

      act(() => {
        handle!.rollback("Error occurred");
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(false);
    });

    it("flushes buffered mutations on rollback", () => {
      const { result, handleMutation } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      const mutation = createMutationPayload({ issue_id: "issue-1" });
      act(() => {
        result.current.filterMutation(mutation);
      });

      act(() => {
        handle!.rollback("Error");
      });

      expect(handleMutation).toHaveBeenCalledTimes(1);
      expect(handleMutation).toHaveBeenCalledWith(mutation);
    });

    it("shows error toast with provided message", () => {
      const { result, showToast } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback("API request failed");
      });

      expect(showToast).toHaveBeenCalledTimes(1);
      expect(showToast).toHaveBeenCalledWith("API request failed", {
        type: "error",
      });
    });

    it("does not show toast when no error message is provided", () => {
      const { result, showToast } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback();
      });

      expect(showToast).not.toHaveBeenCalled();
    });

    it("is a no-op if called after already rolled back", () => {
      const { result, setIssuesMap, showToast } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback("Error");
      });

      setIssuesMap.mockClear();
      showToast.mockClear();

      // Second rollback should be a no-op
      act(() => {
        handle!.rollback("Error again");
      });

      expect(setIssuesMap).not.toHaveBeenCalled();
      expect(showToast).not.toHaveBeenCalled();
    });

    it("does not restore snapshot or show toast if component is unmounted", () => {
      const { result, setIssuesMap, showToast, mountedRef } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      // Simulate unmount
      mountedRef.current = false;

      act(() => {
        handle!.rollback("Error");
      });

      expect(setIssuesMap).not.toHaveBeenCalled();
      expect(showToast).not.toHaveBeenCalled();
    });
  });

  describe("filterMutation", () => {
    it("returns true for non-optimistic issue IDs", () => {
      const { result } = setupHook();
      const mutation = createMutationPayload({ issue_id: "other-issue" });

      let passThrough: boolean = false;
      act(() => {
        passThrough = result.current.filterMutation(mutation);
      });

      expect(passThrough).toBe(true);
    });

    it("returns false and buffers mutation for optimistic issue IDs", () => {
      const { result, handleMutation } = setupHook();
      const snapshot = createTestIssue();

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      const mutation = createMutationPayload({ issue_id: "issue-1" });
      let passThrough: boolean = true;
      act(() => {
        passThrough = result.current.filterMutation(mutation);
      });

      expect(passThrough).toBe(false);
      // Mutation should not be handled immediately
      expect(handleMutation).not.toHaveBeenCalled();
    });

    it("buffers multiple mutations for the same optimistic issue", () => {
      const { result, handleMutation } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      const mutations = [
        createMutationPayload({
          issue_id: "issue-1",
          timestamp: "2025-01-23T12:01:00Z",
        }),
        createMutationPayload({
          issue_id: "issue-1",
          timestamp: "2025-01-23T12:02:00Z",
        }),
        createMutationPayload({
          issue_id: "issue-1",
          timestamp: "2025-01-23T12:03:00Z",
        }),
      ];

      act(() => {
        for (const m of mutations) {
          result.current.filterMutation(m);
        }
      });

      // Confirm to flush all
      act(() => {
        handle!.confirm();
      });

      expect(handleMutation).toHaveBeenCalledTimes(3);
      expect(handleMutation).toHaveBeenNthCalledWith(1, mutations[0]);
      expect(handleMutation).toHaveBeenNthCalledWith(2, mutations[1]);
      expect(handleMutation).toHaveBeenNthCalledWith(3, mutations[2]);
    });

    it("passes through mutations for non-optimistic issues while buffering optimistic ones", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue({ id: "issue-1" });

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      let result1: boolean = false;
      let result2: boolean = false;
      act(() => {
        result1 = result.current.filterMutation(
          createMutationPayload({ issue_id: "issue-1" }),
        );
        result2 = result.current.filterMutation(
          createMutationPayload({ issue_id: "issue-2" }),
        );
      });

      expect(result1).toBe(false); // buffered
      expect(result2).toBe(true); // passed through
    });
  });

  describe("isOptimistic", () => {
    it("returns true for an issue currently in optimistic state", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      expect(result.current.isOptimistic("issue-1")).toBe(true);
    });

    it("returns false after confirm", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.confirm();
      });

      expect(result.current.isOptimistic("issue-1")).toBe(false);
    });

    it("returns false after rollback", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback("Error");
      });

      expect(result.current.isOptimistic("issue-1")).toBe(false);
    });

    it("returns false for issues that were never optimistic", () => {
      const { result } = setupHook();
      expect(result.current.isOptimistic("nonexistent")).toBe(false);
    });
  });

  describe("auto-rollback timeout", () => {
    it("auto-rolls back after 30 seconds", () => {
      const { result, setIssuesMap, showToast } = setupHook();
      const snapshot = createTestIssue({ id: "issue-1", title: "Original" });

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(true);

      // Advance just under 30s - should still be pending
      act(() => {
        vi.advanceTimersByTime(29_999);
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(true);

      // Advance to 30s - should auto-rollback
      act(() => {
        vi.advanceTimersByTime(1);
      });

      expect(result.current.pendingIds.has("issue-1")).toBe(false);
      expect(setIssuesMap).toHaveBeenCalledTimes(1);
      expect(showToast).toHaveBeenCalledWith(
        "Update timed out — changes reverted",
        { type: "error" },
      );
    });

    it("restores snapshot on auto-rollback", () => {
      const { result, setIssuesMap } = setupHook();
      const snapshot = createTestIssue({ id: "issue-1", title: "Original" });

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        vi.advanceTimersByTime(30_000);
      });

      const updater = setIssuesMap.mock.calls[0][0];
      expect(typeof updater).toBe("function");

      const currentMap = new Map<string, Issue>();
      currentMap.set("issue-1", createTestIssue({ title: "Modified" }));
      const resultMap = updater(currentMap);
      expect(resultMap.get("issue-1")).toEqual(snapshot);
    });

    it("flushes buffered mutations on auto-rollback", () => {
      const { result, handleMutation } = setupHook();
      const snapshot = createTestIssue();

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      const mutation = createMutationPayload({ issue_id: "issue-1" });
      act(() => {
        result.current.filterMutation(mutation);
      });

      act(() => {
        vi.advanceTimersByTime(30_000);
      });

      expect(handleMutation).toHaveBeenCalledTimes(1);
      expect(handleMutation).toHaveBeenCalledWith(mutation);
    });

    it("does not auto-rollback if component is unmounted", () => {
      const { result, setIssuesMap, showToast, mountedRef } = setupHook();
      const snapshot = createTestIssue();

      act(() => {
        result.current.startOptimistic("issue-1", snapshot);
      });

      // Simulate unmount
      mountedRef.current = false;

      act(() => {
        vi.advanceTimersByTime(30_000);
      });

      expect(setIssuesMap).not.toHaveBeenCalled();
      expect(showToast).not.toHaveBeenCalled();
    });

    it("does not auto-rollback if already confirmed before timeout", () => {
      const { result, setIssuesMap, showToast } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      // Confirm before the timeout
      act(() => {
        handle!.confirm();
      });

      // Advance past the timeout
      act(() => {
        vi.advanceTimersByTime(30_000);
      });

      // Should not have been called (no rollback happened)
      expect(setIssuesMap).not.toHaveBeenCalled();
      expect(showToast).not.toHaveBeenCalled();
    });

    it("does not auto-rollback if already rolled back before timeout", () => {
      const { result, setIssuesMap, showToast } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback("Manual rollback");
      });

      setIssuesMap.mockClear();
      showToast.mockClear();

      // Advance past the timeout
      act(() => {
        vi.advanceTimersByTime(30_000);
      });

      // Should not have rolled back again
      expect(setIssuesMap).not.toHaveBeenCalled();
      expect(showToast).not.toHaveBeenCalled();
    });
  });

  describe("multiple concurrent optimistic updates", () => {
    it("tracks multiple issues independently in pendingIds", () => {
      const { result } = setupHook();
      const snapshot1 = createTestIssue({ id: "issue-1" });
      const snapshot2 = createTestIssue({ id: "issue-2" });
      const snapshot3 = createTestIssue({ id: "issue-3" });

      act(() => {
        result.current.startOptimistic("issue-1", snapshot1);
        result.current.startOptimistic("issue-2", snapshot2);
        result.current.startOptimistic("issue-3", snapshot3);
      });

      expect(result.current.pendingIds.size).toBe(3);
      expect(result.current.isOptimistic("issue-1")).toBe(true);
      expect(result.current.isOptimistic("issue-2")).toBe(true);
      expect(result.current.isOptimistic("issue-3")).toBe(true);
    });

    it("confirming one issue does not affect others", () => {
      const { result } = setupHook();
      const snapshot1 = createTestIssue({ id: "issue-1" });
      const snapshot2 = createTestIssue({ id: "issue-2" });

      let handle1: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle1 = result.current.startOptimistic("issue-1", snapshot1);
        result.current.startOptimistic("issue-2", snapshot2);
      });

      act(() => {
        handle1!.confirm();
      });

      expect(result.current.isOptimistic("issue-1")).toBe(false);
      expect(result.current.isOptimistic("issue-2")).toBe(true);
      expect(result.current.pendingIds.size).toBe(1);
    });

    it("rolling back one issue does not affect others", () => {
      const { result, setIssuesMap } = setupHook();
      const snapshot1 = createTestIssue({ id: "issue-1", title: "Issue 1" });
      const snapshot2 = createTestIssue({ id: "issue-2", title: "Issue 2" });

      let handle1: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle1 = result.current.startOptimistic("issue-1", snapshot1);
        result.current.startOptimistic("issue-2", snapshot2);
      });

      act(() => {
        handle1!.rollback("Error on issue 1");
      });

      expect(result.current.isOptimistic("issue-1")).toBe(false);
      expect(result.current.isOptimistic("issue-2")).toBe(true);

      // Only one setIssuesMap call for the rolled-back issue
      expect(setIssuesMap).toHaveBeenCalledTimes(1);
    });

    it("buffers mutations independently per issue", () => {
      const { result, handleMutation } = setupHook();
      const snapshot1 = createTestIssue({ id: "issue-1" });
      const snapshot2 = createTestIssue({ id: "issue-2" });

      let handle1: ReturnType<typeof result.current.startOptimistic>;
      let handle2: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle1 = result.current.startOptimistic("issue-1", snapshot1);
        handle2 = result.current.startOptimistic("issue-2", snapshot2);
      });

      const mutation1 = createMutationPayload({
        issue_id: "issue-1",
        timestamp: "t1",
      });
      const mutation2 = createMutationPayload({
        issue_id: "issue-2",
        timestamp: "t2",
      });

      act(() => {
        result.current.filterMutation(mutation1);
        result.current.filterMutation(mutation2);
      });

      // Confirm issue-1 only
      act(() => {
        handle1!.confirm();
      });

      expect(handleMutation).toHaveBeenCalledTimes(1);
      expect(handleMutation).toHaveBeenCalledWith(mutation1);

      handleMutation.mockClear();

      // Confirm issue-2
      act(() => {
        handle2!.confirm();
      });

      expect(handleMutation).toHaveBeenCalledTimes(1);
      expect(handleMutation).toHaveBeenCalledWith(mutation2);
    });

    it("auto-rollback fires independently per issue", () => {
      const { result, showToast } = setupHook();
      const snapshot1 = createTestIssue({ id: "issue-1" });
      const snapshot2 = createTestIssue({ id: "issue-2" });

      act(() => {
        result.current.startOptimistic("issue-1", snapshot1);
      });

      // Start issue-2 after 10s
      act(() => {
        vi.advanceTimersByTime(10_000);
      });

      act(() => {
        result.current.startOptimistic("issue-2", snapshot2);
      });

      // At 30s total: issue-1 should auto-rollback, issue-2 should still be pending
      act(() => {
        vi.advanceTimersByTime(20_000);
      });

      expect(result.current.isOptimistic("issue-1")).toBe(false);
      expect(result.current.isOptimistic("issue-2")).toBe(true);
      expect(showToast).toHaveBeenCalledTimes(1);

      // At 40s total: issue-2 should now auto-rollback
      act(() => {
        vi.advanceTimersByTime(10_000);
      });

      expect(result.current.isOptimistic("issue-2")).toBe(false);
      expect(showToast).toHaveBeenCalledTimes(2);
    });
  });

  describe("re-use after confirm/rollback", () => {
    it("allows starting a new optimistic update after confirm", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.confirm();
      });

      // Should be able to start again
      let handle2: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle2 = result.current.startOptimistic("issue-1", snapshot);
      });

      expect(handle2).not.toBeNull();
      expect(result.current.isOptimistic("issue-1")).toBe(true);
    });

    it("allows starting a new optimistic update after rollback", () => {
      const { result } = setupHook();
      const snapshot = createTestIssue();

      let handle: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle = result.current.startOptimistic("issue-1", snapshot);
      });

      act(() => {
        handle!.rollback("Error");
      });

      let handle2: ReturnType<typeof result.current.startOptimistic>;
      act(() => {
        handle2 = result.current.startOptimistic("issue-1", snapshot);
      });

      expect(handle2).not.toBeNull();
      expect(result.current.isOptimistic("issue-1")).toBe(true);
    });
  });
});
