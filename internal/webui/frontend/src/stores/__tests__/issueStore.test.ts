/**
 * Unit tests for issueStore.
 * All tests use the vanilla store directly — no React rendering needed.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { createIssueStore, issuesAreEqual } from "../issueStore";
import type { IssueStore } from "../issueStore";
import type { StoreApi } from "zustand/vanilla";
import type { Issue } from "@/types/issue";
import type { MutationPayload } from "../../api/common";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock(import("../../api/issues"), async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    getReadyIssues: vi.fn(),
    getKanbanIssues: vi.fn(),
    fetchGraphIssues: vi.fn(),
    updateIssue: vi.fn(),
  };
});

import {
  getReadyIssues,
  getKanbanIssues,
  fetchGraphIssues,
  updateIssue,
} from "../../api/issues";

const mockGetReadyIssues = vi.mocked(getReadyIssues);
const mockGetKanbanIssues = vi.mocked(getKanbanIssues);
const mockFetchGraphIssues = vi.mocked(fetchGraphIssues);
const mockUpdateIssue = vi.mocked(updateIssue);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    title: "Test Issue",
    priority: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function makeMutation(
  overrides: Partial<MutationPayload> = {},
): MutationPayload {
  return {
    type: "update",
    issue_id: "issue-1",
    timestamp: "2026-01-02T00:00:00Z",
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("issueStore", () => {
  let store: StoreApi<IssueStore>;

  beforeEach(() => {
    vi.useFakeTimers();
    store = createIssueStore();
    vi.clearAllMocks();
  });

  afterEach(() => {
    store.getState().reset();
    vi.useRealTimers();
  });

  // -----------------------------------------------------------------------
  // Initial state
  // -----------------------------------------------------------------------

  describe("initial state", () => {
    it("has correct defaults", () => {
      const s = store.getState();
      expect(s.issuesMap.size).toBe(0);
      expect(s.isLoading).toBe(false);
      expect(s.error).toBeNull();
      expect(s.connectionState).toBe("disconnected");
      expect(s.reconnectAttempts).toBe(0);
      expect(s.lastEventId).toBeUndefined();
      expect(s.showStaleBanner).toBe(false);
      expect(s.connectionLost).toBe(false);
      expect(s.disconnectedSince).toBeNull();
      expect(s.pendingIds.size).toBe(0);
      expect(s.mutationCount).toBe(0);
      expect(s.resetGeneration).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // fetchIssues
  // -----------------------------------------------------------------------

  describe("fetchIssues", () => {
    it("fetches ready issues and populates issuesMap", async () => {
      const issues = [makeIssue({ id: "a" }), makeIssue({ id: "b" })];
      mockGetReadyIssues.mockResolvedValue(issues);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      expect(s.issuesMap.size).toBe(2);
      expect(s.issuesMap.get("a")).toBeDefined();
      expect(s.issuesMap.get("b")).toBeDefined();
      expect(s.isLoading).toBe(false);
      expect(s.error).toBeNull();
      expect(mockGetReadyIssues).toHaveBeenCalledWith(
        "ws1",
        undefined,
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
    });

    it("transitions isLoading correctly", async () => {
      let resolveFn: (v: Issue[]) => void;
      mockGetReadyIssues.mockReturnValue(
        new Promise<Issue[]>((resolve) => {
          resolveFn = resolve;
        }),
      );

      const promise = store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      expect(store.getState().isLoading).toBe(true);

      resolveFn!([]);
      await promise;

      expect(store.getState().isLoading).toBe(false);
    });

    it("fetches kanban issues when mode is kanban", async () => {
      mockGetKanbanIssues.mockResolvedValue([]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "kanban",
      });

      expect(mockGetKanbanIssues).toHaveBeenCalled();
      expect(mockGetReadyIssues).not.toHaveBeenCalled();
    });

    it("fetches graph issues when mode is graph", async () => {
      mockFetchGraphIssues.mockResolvedValue([]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "graph",
      });

      expect(mockFetchGraphIssues).toHaveBeenCalled();
      expect(mockGetReadyIssues).not.toHaveBeenCalled();
    });

    it("sets error on API failure", async () => {
      mockGetReadyIssues.mockRejectedValue(new Error("Network error"));

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      expect(s.error).toBe("Network error");
      expect(s.isLoading).toBe(false);
    });

    it("surfaces ApiError.body.error instead of the generic status text", async () => {
      // Reproduces the fix for the daemon-loading UX: IssueViewGuard decides
      // between the "loading" and "fetch-error" variants by checking the
      // error string for the server's phrase. Prior to the fix the store
      // surfaced ApiError.message ("API Error: 503 Service Unavailable"),
      // which never matched the phrase — the loading-spinner path was
      // unreachable. The store must now extract body.error.
      const { ApiError } = await import("@/types/common");
      const body = { error: "workspace is loading", kind: "starting" };
      mockGetKanbanIssues.mockRejectedValue(
        new ApiError(503, "Service Unavailable", body),
      );

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "kanban",
      });

      const s = store.getState();
      expect(s.error).toBe("workspace is loading");
      expect(s.isLoading).toBe(false);
    });

    it("falls back to ApiError.message when body.error is missing", async () => {
      const { ApiError } = await import("@/types/common");
      mockGetKanbanIssues.mockRejectedValue(
        new ApiError(502, "Bad Gateway", "plain text body"),
      );

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "kanban",
      });

      expect(store.getState().error).toBe("API Error: 502 Bad Gateway");
    });

    it("suppresses AbortError", async () => {
      mockGetReadyIssues.mockRejectedValue(
        new DOMException("Aborted", "AbortError"),
      );

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      expect(s.error).toBeNull();
      expect(s.isLoading).toBe(false);
    });

    it("preserves SSE mutations during fetch (merge: newer wins)", async () => {
      // Seed an issue with a newer timestamp
      const newerIssue = makeIssue({
        id: "a",
        updated_at: "2026-02-01T00:00:00Z",
        title: "SSE Updated",
      });
      store.setState({ issuesMap: new Map([["a", newerIssue]]) });

      // API returns older version
      const apiIssue = makeIssue({
        id: "a",
        updated_at: "2026-01-01T00:00:00Z",
        title: "API Version",
      });
      mockGetReadyIssues.mockResolvedValue([apiIssue]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      expect(s.issuesMap.get("a")!.title).toBe("SSE Updated");
    });

    it("preserves reference for unchanged issues (issuesAreEqual)", async () => {
      const original = makeIssue({ id: "a" });
      store.setState({ issuesMap: new Map([["a", original]]) });

      // Return identical issue from API
      const apiIssue = makeIssue({ id: "a" });
      mockGetReadyIssues.mockResolvedValue([apiIssue]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      expect(store.getState().issuesMap.get("a")).toBe(original);
    });

    it("skips issues deleted during fetch window", async () => {
      // We need to intercept during the fetch. Use a delayed mock that triggers a delete.
      mockGetReadyIssues.mockImplementation(async () => {
        // Simulate SSE delete arriving while fetch is in flight
        store.getState().applyMutation(
          makeMutation({
            type: "delete",
            issue_id: "a",
            timestamp: "2026-03-01T00:00:00Z",
          }),
        );
        return [makeIssue({ id: "a" }), makeIssue({ id: "b" })];
      });

      // Seed the issue so delete has something to remove
      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
      });

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      // "a" was deleted during fetch, so it should NOT be in the final map
      expect(s.issuesMap.has("a")).toBe(false);
      expect(s.issuesMap.has("b")).toBe(true);
    });

    it("aborts in-flight fetch when a new fetch starts (no silent drop)", async () => {
      // First call: never resolves on its own, but its signal should be
      // aborted when the second call starts. Reject with AbortError when
      // signalled, simulating what a real HTTP client does.
      mockGetReadyIssues.mockImplementationOnce(
        (_ws, _filter, opts) =>
          new Promise<Issue[]>((_resolve, reject) => {
            opts?.signal?.addEventListener("abort", () => {
              reject(new DOMException("Aborted", "AbortError"));
            });
          }),
      );
      // Second call: resolves immediately with a known result.
      const expected = [makeIssue({ id: "from-second" })];
      mockGetReadyIssues.mockResolvedValueOnce(expected);

      const promise1 = store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });
      const promise2 = store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      await Promise.all([promise1, promise2]);

      // Both fetches were invoked — no silent drop.
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(2);
      // Only the second fetch's data is applied; the first was aborted.
      const s = store.getState();
      expect(s.issuesMap.has("from-second")).toBe(true);
      expect(s.isLoading).toBe(false);
      expect(s.error).toBeNull();
    });

    it("passes sourceRepos to filter", async () => {
      mockGetReadyIssues.mockResolvedValue([]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
        filter: { status: "open" },
        sourceRepos: ["repo-a"],
      });

      expect(mockGetReadyIssues).toHaveBeenCalledWith(
        "ws1",
        expect.objectContaining({ source_repos: ["repo-a"], status: "open" }),
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
    });
  });

  // -----------------------------------------------------------------------
  // Auto-retry with exponential backoff
  // -----------------------------------------------------------------------

  describe("auto-retry", () => {
    it("schedules an auto-retry when fetch fails with a non-abort error", async () => {
      mockGetReadyIssues.mockRejectedValueOnce(new Error("boom"));

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      expect(s.error).toBe("boom");
      expect(s.retryCount).toBe(1);
      expect(s.nextRetryAt).not.toBeNull();
      expect(s.isLoading).toBe(false);
    });

    it("auto-retry succeeds on recovery and clears error + retryCount", async () => {
      mockGetReadyIssues
        .mockRejectedValueOnce(new Error("transient"))
        .mockResolvedValueOnce([makeIssue({ id: "recovered" })]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      // After first failure
      expect(store.getState().retryCount).toBe(1);
      expect(store.getState().error).toBe("transient");

      // Advance past the 1s base delay
      await vi.advanceTimersByTimeAsync(1_500);

      const s = store.getState();
      expect(s.error).toBeNull();
      expect(s.retryCount).toBe(0);
      expect(s.nextRetryAt).toBeNull();
      expect(s.issuesMap.has("recovered")).toBe(true);
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(2);
    });

    it("stops retrying after MAX_AUTO_RETRIES failures and leaves error visible", async () => {
      mockGetReadyIssues.mockRejectedValue(new Error("persistent failure"));

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      // Advance through all 5 retries: 1s + 2s + 4s + 8s + 16s = 31s total
      await vi.advanceTimersByTimeAsync(35_000);

      const s = store.getState();
      expect(s.error).toBe("persistent failure");
      expect(s.retryCount).toBe(5);
      expect(s.nextRetryAt).toBeNull();
      // Initial call + 5 auto-retries = 6 total
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(6);
    });

    it("cancels pending auto-retry when a new fetch starts", async () => {
      mockGetReadyIssues.mockRejectedValueOnce(new Error("fail"));

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });
      expect(store.getState().retryCount).toBe(1);

      // Start a new (successful) fetch before the retry timer fires
      mockGetReadyIssues.mockResolvedValueOnce([makeIssue({ id: "manual" })]);
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      // retryCount is reset by the new non-retry fetch
      expect(store.getState().retryCount).toBe(0);
      expect(store.getState().nextRetryAt).toBeNull();

      // Advance time — the cancelled retry should NOT fire
      await vi.advanceTimersByTimeAsync(5_000);
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(2);
    });

    it("cancels pending auto-retry on reset()", async () => {
      mockGetReadyIssues.mockRejectedValueOnce(new Error("fail"));

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });
      expect(store.getState().retryCount).toBe(1);

      store.getState().reset();

      expect(store.getState().retryCount).toBe(0);
      expect(store.getState().nextRetryAt).toBeNull();
      // reset() increments resetGeneration so App.tsx's fetchIssues effect
      // re-runs and recovers from the aborted initial fetch.
      expect(store.getState().resetGeneration).toBeGreaterThan(0);

      await vi.advanceTimersByTimeAsync(5_000);
      // Only the initial (failed) call; no retry fired.
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(1);
    });

    it("refetch() cancels pending auto-retry and starts fresh (resets retryCount)", async () => {
      mockGetReadyIssues
        .mockRejectedValueOnce(new Error("first fail"))
        .mockResolvedValueOnce([makeIssue({ id: "manual-refetch" })]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });
      expect(store.getState().retryCount).toBe(1);

      // Manual refetch before auto-retry fires
      await store.getState().refetch();

      const s = store.getState();
      expect(s.retryCount).toBe(0);
      expect(s.error).toBeNull();
      expect(s.issuesMap.has("manual-refetch")).toBe(true);

      // Cancelled auto-retry must not fire later
      await vi.advanceTimersByTimeAsync(5_000);
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(2);
    });

    it("uses exponential backoff for successive failures (1s, 2s, 4s)", async () => {
      mockGetReadyIssues.mockRejectedValue(new Error("fail"));

      const t0 = Date.now();
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const afterFirst = store.getState();
      expect(afterFirst.retryCount).toBe(1);
      // Delay for retry #1 uses index 0: 1000ms
      expect(afterFirst.nextRetryAt).toBe(t0 + 1_000);

      await vi.advanceTimersByTimeAsync(1_000);
      const afterSecond = store.getState();
      expect(afterSecond.retryCount).toBe(2);
      // Delay for retry #2 uses index 1: 2000ms
      expect(afterSecond.nextRetryAt).toBe(Date.now() + 2_000);

      await vi.advanceTimersByTimeAsync(2_000);
      const afterThird = store.getState();
      expect(afterThird.retryCount).toBe(3);
      // Delay for retry #3 uses index 2: 4000ms
      expect(afterThird.nextRetryAt).toBe(Date.now() + 4_000);
    });

    it("isAutoRetry=true preserves retryCount from prior failure", async () => {
      mockGetReadyIssues.mockRejectedValue(new Error("fail"));

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });
      expect(store.getState().retryCount).toBe(1);

      // Manually call fetchIssues with isAutoRetry=true — simulates the
      // scheduled retry callback. retryCount should NOT reset to 0.
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
        isAutoRetry: true,
      });

      // The auto-retry also failed, so count should be 2 now
      expect(store.getState().retryCount).toBe(2);
    });

    it("successful initial fetch does not schedule a retry", async () => {
      mockGetReadyIssues.mockResolvedValue([makeIssue({ id: "a" })]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      const s = store.getState();
      expect(s.retryCount).toBe(0);
      expect(s.nextRetryAt).toBeNull();
      expect(s.error).toBeNull();
    });

    it("does not reuse the caller's external signal for scheduled retries", async () => {
      // Simulate the App.tsx pattern: caller passes an AbortSignal and
      // aborts it later (e.g., useEffect cleanup on view switch). If the
      // retry reused that aborted signal, the retry would be dropped.
      const externalController = new AbortController();
      mockGetReadyIssues
        .mockRejectedValueOnce(new Error("transient"))
        .mockResolvedValueOnce([makeIssue({ id: "recovered" })]);

      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
        signal: externalController.signal,
      });
      expect(store.getState().retryCount).toBe(1);

      // Caller cleans up (e.g. component remount, view switch).
      externalController.abort();

      // Advance past the 1s backoff — retry should still fire and succeed,
      // despite the external signal being aborted.
      await vi.advanceTimersByTimeAsync(1_500);

      const s = store.getState();
      expect(s.error).toBeNull();
      expect(s.retryCount).toBe(0);
      expect(s.issuesMap.has("recovered")).toBe(true);
      expect(mockGetReadyIssues).toHaveBeenCalledTimes(2);
    });
  });

  // -----------------------------------------------------------------------
  // applyMutation
  // -----------------------------------------------------------------------

  describe("applyMutation", () => {
    it("creates a new issue from create mutation", () => {
      store.getState().applyMutation(
        makeMutation({
          type: "create",
          issue_id: "new-1",
          title: "New Issue",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      const s = store.getState();
      expect(s.issuesMap.has("new-1")).toBe(true);
      expect(s.issuesMap.get("new-1")!.title).toBe("New Issue");
      expect(s.mutationCount).toBe(1);
    });

    it("updates an existing issue", () => {
      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a", title: "Old" })]]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          title: "New Title",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.title).toBe("New Title");
      expect(store.getState().mutationCount).toBe(1);
    });

    it("deletes an existing issue", () => {
      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "delete",
          issue_id: "a",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.has("a")).toBe(false);
      expect(store.getState().mutationCount).toBe(1);
    });

    it("applies status mutation", () => {
      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a", status: "open" })]]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "status",
          issue_id: "a",
          new_status: "in_progress",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.status).toBe("in_progress");
    });

    it("skips stale mutations", () => {
      store.setState({
        issuesMap: new Map([
          [
            "a",
            makeIssue({
              id: "a",
              title: "Current",
              updated_at: "2026-02-01T00:00:00Z",
            }),
          ],
        ]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          title: "Stale",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.title).toBe("Current");
      expect(store.getState().mutationCount).toBe(0);
    });

    it("gates mutations for different workspace", () => {
      // Activate workspace gating
      mockGetReadyIssues.mockResolvedValue([]);
      store.getState().fetchIssues({ workspaceId: "ws1", mode: "ready" });

      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          title: "From Other Workspace",
          workspace_id: "ws2",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.title).toBe("Test Issue");
    });

    it("gates mutations for unselected source_repo", async () => {
      mockGetReadyIssues.mockResolvedValue([]);
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
        sourceRepos: ["repo-a"],
      });

      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          title: "From Other Repo",
          source_repo: "repo-b",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.title).toBe("Test Issue");
    });

    it("allows mutations without source_repo when repo filter is active", async () => {
      mockGetReadyIssues.mockResolvedValue([]);
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
        sourceRepos: ["repo-a"],
      });

      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          title: "No Repo",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.title).toBe("No Repo");
    });

    it("triggers debounced refetch on refresh mutation", () => {
      const refetchSpy = vi
        .spyOn(store.getState(), "refetch")
        .mockResolvedValue();

      store.getState().applyMutation(
        makeMutation({
          type: "refresh",
          issue_id: "",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(refetchSpy).not.toHaveBeenCalled();

      vi.advanceTimersByTime(1000);

      expect(refetchSpy).toHaveBeenCalledTimes(1);
    });

    it("collapses rapid refresh mutations into single refetch", () => {
      const refetchSpy = vi
        .spyOn(store.getState(), "refetch")
        .mockResolvedValue();

      store.getState().applyMutation(
        makeMutation({
          type: "refresh",
          issue_id: "",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );
      vi.advanceTimersByTime(500);

      store.getState().applyMutation(
        makeMutation({
          type: "refresh",
          issue_id: "",
          timestamp: "2026-01-01T00:00:01Z",
        }),
      );
      vi.advanceTimersByTime(1000);

      expect(refetchSpy).toHaveBeenCalledTimes(1);
    });

    it("ignores mutations with empty issue_id (non-refresh)", () => {
      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().mutationCount).toBe(0);
    });

    it("treats duplicate create as update", () => {
      store.setState({
        issuesMap: new Map([
          [
            "a",
            makeIssue({
              id: "a",
              title: "Existing",
              updated_at: "2026-01-01T00:00:00Z",
            }),
          ],
        ]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "create",
          issue_id: "a",
          title: "Updated Via Create",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.title).toBe(
        "Updated Via Create",
      );
    });

    it("skips non-create mutations for missing issues", () => {
      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "nonexistent",
          title: "Won't Apply",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.has("nonexistent")).toBe(false);
      expect(store.getState().mutationCount).toBe(0);
    });

    it("applies bonded mutation (updates timestamp)", () => {
      store.setState({
        issuesMap: new Map([
          ["a", makeIssue({ id: "a", updated_at: "2026-01-01T00:00:00Z" })],
        ]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "bonded",
          issue_id: "a",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.updated_at).toBe(
        "2026-02-01T00:00:00Z",
      );
    });

    it("handles unsupported mutation types by updating timestamp", () => {
      store.setState({
        issuesMap: new Map([
          ["a", makeIssue({ id: "a", updated_at: "2026-01-01T00:00:00Z" })],
        ]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "comment" as MutationPayload["type"],
          issue_id: "a",
          timestamp: "2026-02-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.get("a")!.updated_at).toBe(
        "2026-02-01T00:00:00Z",
      );
    });
  });

  // -----------------------------------------------------------------------
  // connectToEvents
  // -----------------------------------------------------------------------

  describe("connectToEvents", () => {
    it("subscribes and processes mutations from callback", () => {
      let capturedCallback: ((mutation: MutationPayload) => void) | null = null;
      const mockSubscribe = vi.fn((cb: (mutation: MutationPayload) => void) => {
        capturedCallback = cb;
        return () => {
          capturedCallback = null;
        };
      });

      const unsubscribe = store.getState().connectToEvents(mockSubscribe);
      expect(mockSubscribe).toHaveBeenCalledTimes(1);
      expect(typeof unsubscribe).toBe("function");

      // Deliver a mutation
      capturedCallback!(
        makeMutation({
          type: "create",
          issue_id: "new-1",
          title: "From SSE",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().issuesMap.has("new-1")).toBe(true);
    });

    it("returns existing unsubscribe on duplicate call", () => {
      const mockSubscribe = vi.fn(() => () => {});

      const unsub1 = store.getState().connectToEvents(mockSubscribe);
      const unsub2 = store.getState().connectToEvents(mockSubscribe);

      expect(mockSubscribe).toHaveBeenCalledTimes(1);
      expect(unsub1).toBe(unsub2);
    });

    it("unsubscribe cleans up", () => {
      const mockUnsubscribe = vi.fn();
      const mockSubscribe = vi.fn(() => mockUnsubscribe);

      const unsubscribe = store.getState().connectToEvents(mockSubscribe);
      unsubscribe();

      expect(mockUnsubscribe).toHaveBeenCalledTimes(1);

      // Can subscribe again after unsubscribing
      store.getState().connectToEvents(mockSubscribe);
      expect(mockSubscribe).toHaveBeenCalledTimes(2);
    });
  });

  // -----------------------------------------------------------------------
  // updateIssueStatus (optimistic)
  // -----------------------------------------------------------------------

  describe("updateIssueStatus", () => {
    it("optimistically updates status, then confirms", async () => {
      const issue = makeIssue({ id: "a", status: "open" });
      store.setState({ issuesMap: new Map([["a", issue]]) });
      mockUpdateIssue.mockResolvedValue(
        makeIssue({ id: "a", status: "in_progress" }),
      );

      await store.getState().updateIssueStatus("a", "in_progress", "ws1");

      const s = store.getState();
      expect(s.issuesMap.get("a")!.status).toBe("in_progress");
      expect(s.pendingIds.size).toBe(0);
    });

    it("rolls back on API failure and calls onToast", async () => {
      const toastFn = vi.fn();
      store.getState().configure({ onToast: toastFn });

      const issue = makeIssue({ id: "a", status: "open" });
      store.setState({ issuesMap: new Map([["a", issue]]) });
      mockUpdateIssue.mockRejectedValue(new Error("API Error"));

      await expect(
        store.getState().updateIssueStatus("a", "in_progress", "ws1"),
      ).rejects.toThrow("API Error");

      const s = store.getState();
      expect(s.issuesMap.get("a")!.status).toBe("open");
      expect(s.pendingIds.size).toBe(0);
      expect(toastFn).toHaveBeenCalledWith("API Error", { type: "error" });
    });

    it("throws if issue not found", async () => {
      await expect(
        store.getState().updateIssueStatus("nonexistent", "in_progress", "ws1"),
      ).rejects.toThrow("Issue nonexistent not found");
    });

    it("throws if issue already has pending update", async () => {
      const issue = makeIssue({ id: "a", status: "open" });
      store.setState({ issuesMap: new Map([["a", issue]]) });

      // Don't resolve — keep it pending
      mockUpdateIssue.mockReturnValue(new Promise(() => {}));
      void store.getState().updateIssueStatus("a", "in_progress", "ws1");

      await expect(
        store.getState().updateIssueStatus("a", "closed", "ws1"),
      ).rejects.toThrow("already has a pending update");

      // Clean up
      store.getState().reset();
    });

    it("buffers SSE mutations during optimistic window, flushes on confirm", async () => {
      const issue = makeIssue({ id: "a", status: "open", title: "Before" });
      store.setState({ issuesMap: new Map([["a", issue]]) });

      let resolveUpdate: (v: Issue) => void;
      mockUpdateIssue.mockReturnValue(
        new Promise<Issue>((resolve) => {
          resolveUpdate = resolve;
        }),
      );

      const updatePromise = store
        .getState()
        .updateIssueStatus("a", "in_progress", "ws1");

      // Use a timestamp far in the future so it's not considered stale
      // relative to the optimistic update's new Date().toISOString()
      const futureTimestamp = new Date(Date.now() + 60_000).toISOString();

      // SSE mutation arrives while optimistic is pending �� should be buffered
      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          title: "SSE Updated",
          timestamp: futureTimestamp,
        }),
      );

      // Title should still be the optimistic value (not SSE value yet)
      expect(store.getState().issuesMap.get("a")!.title).not.toBe(
        "SSE Updated",
      );

      // Confirm
      resolveUpdate!(makeIssue({ id: "a", status: "in_progress" }));
      await updatePromise;

      // After confirm, buffered mutation should have been flushed
      expect(store.getState().issuesMap.get("a")!.title).toBe("SSE Updated");
    });

    it("auto-rolls back after timeout", () => {
      const toastFn = vi.fn();
      store.getState().configure({ onToast: toastFn });

      const issue = makeIssue({ id: "a", status: "open" });
      store.setState({ issuesMap: new Map([["a", issue]]) });

      // Never resolving promise
      mockUpdateIssue.mockReturnValue(new Promise(() => {}));
      store.getState().updateIssueStatus("a", "in_progress", "ws1");

      // Advance past the auto-rollback timeout
      vi.advanceTimersByTime(30_000);

      const s = store.getState();
      expect(s.issuesMap.get("a")!.status).toBe("open");
      expect(s.pendingIds.size).toBe(0);
      expect(toastFn).toHaveBeenCalledWith(
        "Update timed out — changes reverted",
        { type: "error" },
      );
    });
  });

  // -----------------------------------------------------------------------
  // Connection state management
  // -----------------------------------------------------------------------

  describe("connection state", () => {
    it("shows stale banner after 5s of reconnecting", () => {
      store.getState().setConnectionState("reconnecting");

      expect(store.getState().showStaleBanner).toBe(false);

      vi.advanceTimersByTime(5000);

      expect(store.getState().showStaleBanner).toBe(true);
    });

    it("clears stale banner on reconnection", () => {
      store.getState().setConnectionState("reconnecting");
      vi.advanceTimersByTime(5000);
      expect(store.getState().showStaleBanner).toBe(true);

      store.getState().setConnectionState("connected");
      expect(store.getState().showStaleBanner).toBe(false);
    });

    it("sets connectionLost when reconnectAttempts >= 10", () => {
      store.getState().setReconnectAttempts(10);
      expect(store.getState().connectionLost).toBe(true);
    });

    it("does not set connectionLost below threshold", () => {
      store.getState().setReconnectAttempts(9);
      expect(store.getState().connectionLost).toBe(false);
    });

    it("triggers refetch after too-far-behind reconnection", async () => {
      mockGetReadyIssues.mockResolvedValue([]);

      // Set up active workspace for refetch
      await store.getState().fetchIssues({ workspaceId: "ws1", mode: "ready" });

      const refetchSpy = vi
        .spyOn(store.getState(), "refetch")
        .mockResolvedValue();
      const toastFn = vi.fn();
      store.getState().configure({ onToast: toastFn });

      // Simulate 3 reconnect attempts then reconnection
      store.getState().setReconnectAttempts(3);
      store.getState().setConnectionState("reconnecting");
      store.getState().setConnectionState("connected");

      expect(refetchSpy).toHaveBeenCalled();
      expect(toastFn).toHaveBeenCalledWith(
        "Connection restored. Refreshing data...",
        { type: "info", duration: 3000 },
      );
    });

    it("shows change count toast on simple reconnection", () => {
      const toastFn = vi.fn();
      store.getState().configure({ onToast: toastFn });

      // Enter reconnecting state
      store.getState().setConnectionState("reconnecting");

      // Simulate some mutations arriving (e.g., via another mechanism)
      store.setState({ mutationCount: 5 });

      // Reconnect
      store.getState().setConnectionState("connected");

      expect(toastFn).toHaveBeenCalledWith(
        "Connection restored. 5 changes synced.",
        { type: "info", duration: 3000 },
      );
    });

    it("records disconnectedSince on transition to reconnecting", () => {
      const now = Date.now();
      store.getState().setConnectionState("reconnecting");
      expect(store.getState().disconnectedSince).toBe(now);
    });

    it("clears disconnectedSince on reconnection", () => {
      store.getState().setConnectionState("reconnecting");
      store.getState().setConnectionState("connected");
      expect(store.getState().disconnectedSince).toBeNull();
    });

    it("clears stale banner timer on transition to disconnected", () => {
      store.getState().setConnectionState("reconnecting");
      store.getState().setConnectionState("disconnected");

      // Banner should not appear even after delay
      vi.advanceTimersByTime(10000);
      expect(store.getState().showStaleBanner).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // reset
  // -----------------------------------------------------------------------

  describe("reset", () => {
    it("clears all state to initial values", async () => {
      // Populate store
      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
        isLoading: false,
        error: "some error",
        connectionState: "connected",
        reconnectAttempts: 5,
        mutationCount: 10,
        showStaleBanner: true,
        connectionLost: true,
        disconnectedSince: Date.now(),
      });

      store.getState().reset();

      const s = store.getState();
      expect(s.issuesMap.size).toBe(0);
      expect(s.error).toBeNull();
      expect(s.connectionState).toBe("disconnected");
      expect(s.reconnectAttempts).toBe(0);
      expect(s.mutationCount).toBe(0);
      expect(s.showStaleBanner).toBe(false);
      expect(s.connectionLost).toBe(false);
      expect(s.disconnectedSince).toBeNull();
      expect(s.pendingIds.size).toBe(0);
    });

    it("clears pending optimistic timers", () => {
      const issue = makeIssue({ id: "a", status: "open" });
      store.setState({ issuesMap: new Map([["a", issue]]) });
      mockUpdateIssue.mockReturnValue(new Promise(() => {}));
      store.getState().updateIssueStatus("a", "in_progress", "ws1");

      expect(store.getState().pendingIds.size).toBe(1);

      store.getState().reset();

      expect(store.getState().pendingIds.size).toBe(0);

      // Timer should be cleared — advancing shouldn't cause issues
      vi.advanceTimersByTime(60000);
    });

    it("does not unsubscribe from events (managed by StoreWiring)", () => {
      const mockUnsubscribe = vi.fn();
      const mockSubscribe = vi.fn(() => mockUnsubscribe);
      store.getState().connectToEvents(mockSubscribe);

      store.getState().reset();

      // SSE subscription lifecycle is managed by StoreWiring's useEffect,
      // not by reset(). Calling eventUnsubscribe in reset() breaks SSE
      // after workspace changes because the subscription is never re-established.
      expect(mockUnsubscribe).not.toHaveBeenCalled();
    });

    it("increments resetGeneration on every call (does not reset to 0)", () => {
      expect(store.getState().resetGeneration).toBe(0);

      store.getState().reset();
      expect(store.getState().resetGeneration).toBe(1);

      store.getState().reset();
      expect(store.getState().resetGeneration).toBe(2);

      store.getState().reset();
      expect(store.getState().resetGeneration).toBe(3);
    });

    it("preserves resetGeneration across full state resets", () => {
      // Populate state so reset has work to do.
      store.setState({
        issuesMap: new Map([["a", makeIssue({ id: "a" })]]),
        error: "some error",
        connectionState: "connected",
      });

      store.getState().reset();
      expect(store.getState().resetGeneration).toBe(1);

      // Populate again and reset again — generation must keep climbing, not
      // reset to 0 like other fields. App.tsx's fetchIssues effect depends
      // on this monotonic counter to detect reset() and re-run.
      store.setState({
        issuesMap: new Map([["b", makeIssue({ id: "b" })]]),
        error: "another error",
      });
      store.getState().reset();
      expect(store.getState().resetGeneration).toBe(2);
    });
  });

  // -----------------------------------------------------------------------
  // retryConnection
  // -----------------------------------------------------------------------

  describe("retryConnection", () => {
    it("calls configured retryConnectionFn", () => {
      const retryFn = vi.fn();
      store.getState().configure({ retryConnectionFn: retryFn });
      store.getState().retryConnection();
      expect(retryFn).toHaveBeenCalled();
    });

    it("is a no-op without configuration", () => {
      // Should not throw
      store.getState().retryConnection();
    });
  });

  // -----------------------------------------------------------------------
  // getIssue
  // -----------------------------------------------------------------------

  describe("getIssue", () => {
    it("returns issue by id", () => {
      const issue = makeIssue({ id: "a" });
      store.setState({ issuesMap: new Map([["a", issue]]) });
      expect(store.getState().getIssue("a")).toBe(issue);
    });

    it("returns undefined for missing id", () => {
      expect(store.getState().getIssue("nonexistent")).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // issuesAreEqual
  // -----------------------------------------------------------------------

  describe("issuesAreEqual", () => {
    it("returns true for identical issues", () => {
      const a = makeIssue();
      expect(issuesAreEqual(a, a)).toBe(true);
    });

    it("returns true for equal issues (different references)", () => {
      const a = makeIssue({ labels: ["bug"] });
      const b = makeIssue({ labels: ["bug"] });
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns false when id differs", () => {
      expect(
        issuesAreEqual(makeIssue({ id: "a" }), makeIssue({ id: "b" })),
      ).toBe(false);
    });

    it("returns false when updated_at differs", () => {
      expect(
        issuesAreEqual(
          makeIssue({ updated_at: "2026-01-01T00:00:00Z" }),
          makeIssue({ updated_at: "2026-02-01T00:00:00Z" }),
        ),
      ).toBe(false);
    });

    it("returns false when title differs", () => {
      expect(
        issuesAreEqual(makeIssue({ title: "A" }), makeIssue({ title: "B" })),
      ).toBe(false);
    });

    it("returns false when status differs", () => {
      expect(
        issuesAreEqual(
          makeIssue({ status: "open" }),
          makeIssue({ status: "closed" }),
        ),
      ).toBe(false);
    });

    it("returns false when priority differs", () => {
      expect(
        issuesAreEqual(makeIssue({ priority: 1 }), makeIssue({ priority: 2 })),
      ).toBe(false);
    });

    it("returns false when assignee differs", () => {
      expect(
        issuesAreEqual(
          makeIssue({ assignee: "alice" }),
          makeIssue({ assignee: "bob" }),
        ),
      ).toBe(false);
    });

    it("returns false when labels differ", () => {
      expect(
        issuesAreEqual(
          makeIssue({ labels: ["a"] }),
          makeIssue({ labels: ["b"] }),
        ),
      ).toBe(false);
    });

    it("returns false when label count differs", () => {
      expect(
        issuesAreEqual(
          makeIssue({ labels: ["a"] }),
          makeIssue({ labels: ["a", "b"] }),
        ),
      ).toBe(false);
    });

    it("returns false when one has labels and other doesn't", () => {
      expect(issuesAreEqual(makeIssue({ labels: ["a"] }), makeIssue({}))).toBe(
        false,
      );
    });
  });

  // -----------------------------------------------------------------------
  // mutationCount
  // -----------------------------------------------------------------------

  describe("mutationCount", () => {
    it("increments on processed mutations", () => {
      store.getState().applyMutation(
        makeMutation({
          type: "create",
          issue_id: "a",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );
      store.getState().applyMutation(
        makeMutation({
          type: "create",
          issue_id: "b",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().mutationCount).toBe(2);
    });

    it("does not increment on gated mutations", async () => {
      mockGetReadyIssues.mockResolvedValue([]);
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          workspace_id: "ws2",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().mutationCount).toBe(0);
    });

    it("does not increment on skipped stale mutations", () => {
      store.setState({
        issuesMap: new Map([
          ["a", makeIssue({ id: "a", updated_at: "2026-02-01T00:00:00Z" })],
        ]),
      });

      store.getState().applyMutation(
        makeMutation({
          type: "update",
          issue_id: "a",
          timestamp: "2026-01-01T00:00:00Z",
        }),
      );

      expect(store.getState().mutationCount).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // configure
  // -----------------------------------------------------------------------

  describe("configure", () => {
    it("sets onToast callback", () => {
      const toastFn = vi.fn();
      store.getState().configure({ onToast: toastFn });

      // Trigger a toast through auto-rollback
      const issue = makeIssue({ id: "a", status: "open" });
      store.setState({ issuesMap: new Map([["a", issue]]) });
      mockUpdateIssue.mockReturnValue(new Promise(() => {}));
      store.getState().updateIssueStatus("a", "in_progress", "ws1");
      vi.advanceTimersByTime(30_000);

      expect(toastFn).toHaveBeenCalled();
    });

    it("sets retryConnectionFn", () => {
      const retryFn = vi.fn();
      store.getState().configure({ retryConnectionFn: retryFn });
      store.getState().retryConnection();
      expect(retryFn).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // refetch
  // -----------------------------------------------------------------------

  describe("refetch", () => {
    it("is a no-op without active workspace", async () => {
      await store.getState().refetch();
      expect(mockGetReadyIssues).not.toHaveBeenCalled();
    });

    it("uses active params from last fetchIssues call", async () => {
      mockGetReadyIssues.mockResolvedValue([]);
      await store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
        filter: { status: "open" },
      });

      mockGetReadyIssues.mockClear();
      mockGetReadyIssues.mockResolvedValue([]);

      await store.getState().refetch();

      expect(mockGetReadyIssues).toHaveBeenCalledWith(
        "ws1",
        expect.objectContaining({ status: "open" }),
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
    });
  });

  // -----------------------------------------------------------------------
  // Store does not import React
  // -----------------------------------------------------------------------

  describe("framework agnostic", () => {
    it("store can be used without React", () => {
      // This test verifies that createIssueStore works in a non-React context
      const s = createIssueStore();
      expect(s.getState().issuesMap.size).toBe(0);
      s.getState().reset();
    });
  });
});
