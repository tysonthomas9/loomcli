/**
 * Unit tests for issueStore.
 * All tests use the vanilla store directly — no React rendering needed.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { createIssueStore, issuesAreEqual } from "../issueStore";
import type { IssueStore } from "../issueStore";
import type { StoreApi } from "zustand/vanilla";
import type { Issue } from "../../types/issue";
import type { MutationPayload } from "../../api/sse";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("../../api/issues", () => ({
  getReadyIssues: vi.fn(),
  getKanbanIssues: vi.fn(),
  fetchGraphIssues: vi.fn(),
  updateIssue: vi.fn(),
}));

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
        undefined,
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

    it("prevents concurrent fetches", async () => {
      let resolveFn: (v: Issue[]) => void;
      mockGetReadyIssues.mockReturnValue(
        new Promise<Issue[]>((resolve) => {
          resolveFn = resolve;
        }),
      );

      const promise1 = store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      // Second call should be a no-op
      const promise2 = store.getState().fetchIssues({
        workspaceId: "ws1",
        mode: "ready",
      });

      resolveFn!([]);
      await Promise.all([promise1, promise2]);

      expect(mockGetReadyIssues).toHaveBeenCalledTimes(1);
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
        undefined,
      );
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
        undefined,
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
