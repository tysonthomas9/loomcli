/**
 * Unit tests for workspaceStore.
 * All tests use the vanilla store directly — no React rendering needed.
 * Uses vi.useFakeTimers() for timer control.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { StoreApi } from "zustand/vanilla";
import type { WorkspaceStore } from "../workspaceStore";
import type { WorkspaceData } from "../../api/workspace";

// ---------------------------------------------------------------------------
// Minimal document mock for visibility change tests
// ---------------------------------------------------------------------------

const listeners = new Map<string, Set<() => void>>();
let mockVisibilityState = "visible";

const mockDocument = {
  get visibilityState() {
    return mockVisibilityState;
  },
  addEventListener(event: string, handler: () => void) {
    if (!listeners.has(event)) listeners.set(event, new Set());
    listeners.get(event)!.add(handler);
  },
  removeEventListener(event: string, handler: () => void) {
    listeners.get(event)?.delete(handler);
  },
  dispatchEvent(event: Event) {
    listeners.get(event.type)?.forEach((h) => h());
    return true;
  },
};

Object.defineProperty(globalThis, "document", {
  value: mockDocument,
  writable: true,
  configurable: true,
});

// Mock the workspace API module
vi.mock("../../api/workspace", () => ({
  fetchWorkspaceApi: vi.fn(),
}));

import { fetchWorkspaceApi } from "../../api/workspace";
import { createWorkspaceStore } from "../workspaceStore";

const mockFetchWorkspaceApi = vi.mocked(fetchWorkspaceApi);

function makeWorkspace(overrides?: Partial<WorkspaceData>): WorkspaceData {
  return {
    id: "ws-1",
    name: "test-workspace",
    path: "/home/user/workspace",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [],
    default_workspace: "",
    ...overrides,
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
} {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe("workspaceStore", () => {
  let store: StoreApi<WorkspaceStore>;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    store = createWorkspaceStore();
  });

  afterEach(() => {
    store.getState().reset();
    vi.useRealTimers();
  });

  describe("initial state", () => {
    it("has workspace null, isLoading false, error null", () => {
      const state = store.getState();
      expect(state.workspace).toBeNull();
      expect(state.isLoading).toBe(false);
      expect(state.error).toBeNull();
    });
  });

  describe("fetchWorkspace", () => {
    it("fetches workspace data and populates state", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws);

      await store.getState().fetchWorkspace("ws-1");

      expect(mockFetchWorkspaceApi).toHaveBeenCalledWith("ws-1");
      expect(store.getState().workspace).toEqual(ws);
      expect(store.getState().isLoading).toBe(false);
      expect(store.getState().error).toBeNull();
    });

    it("sets error but keeps stale workspace data on failure", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws);
      await store.getState().fetchWorkspace("ws-1");

      mockFetchWorkspaceApi.mockRejectedValueOnce(new Error("Network error"));
      await store.getState().fetchWorkspace("ws-1");

      expect(store.getState().error).toBe("Network error");
      expect(store.getState().workspace).toEqual(ws); // stale data preserved
    });

    it("ignores AbortError silently", async () => {
      const abortError = new DOMException(
        "The operation was aborted",
        "AbortError",
      );
      mockFetchWorkspaceApi.mockRejectedValueOnce(abortError);

      await store.getState().fetchWorkspace("ws-1");

      expect(store.getState().error).toBeNull();
    });

    it("deduplicates concurrent calls", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws);

      await Promise.all([
        store.getState().fetchWorkspace("ws-1"),
        store.getState().fetchWorkspace("ws-1"),
      ]);

      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);
    });
  });

  describe("startPolling", () => {
    it("does initial fetch and starts interval", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValue(ws);

      store
        .getState()
        .startPolling({ workspaceId: "ws-1", pollInterval: 5000 });

      // Initial fetch
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      // Poll tick
      await vi.advanceTimersByTimeAsync(5000);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(2);
    });

    it("restarts cleanly when called again (no double-fetches)", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValue(ws);

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      // Restart
      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(2);

      // Only one interval should be running
      await vi.advanceTimersByTimeAsync(5000);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(3);
    });

    it("clears the previous workspace while a different workspace loads", async () => {
      const nextWorkspace = deferred<WorkspaceData>();
      mockFetchWorkspaceApi
        .mockResolvedValueOnce(makeWorkspace({ id: "ws-1", name: "first" }))
        .mockReturnValueOnce(nextWorkspace.promise);

      store.getState().startPolling({ workspaceId: "ws-1", pollInterval: 0 });
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().workspace?.id).toBe("ws-1");

      store.getState().startPolling({ workspaceId: "ws-2", pollInterval: 0 });

      expect(store.getState().workspace).toBeNull();
      expect(store.getState().isLoading).toBe(true);

      nextWorkspace.resolve(makeWorkspace({ id: "ws-2", name: "second" }));
      await vi.advanceTimersByTimeAsync(0);

      expect(store.getState().workspace?.id).toBe("ws-2");
      expect(store.getState().isLoading).toBe(false);
    });
  });

  describe("stopPolling", () => {
    it("clears timers — no further fetches", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValue(ws);

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      store.getState().stopPolling();

      await vi.advanceTimersByTimeAsync(60000);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1); // no more fetches
    });

    it("preserves existing workspace data", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws);

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);

      store.getState().stopPolling();

      expect(store.getState().workspace).toEqual(ws); // data still there
    });
  });

  describe("visibility change", () => {
    it("triggers refetch when tab becomes visible", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValue(ws);

      store.getState().startPolling({ pollInterval: 60000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      // Simulate tab becoming visible
      mockVisibilityState = "visible";
      mockDocument.dispatchEvent(new Event("visibilitychange"));

      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(2);
    });

    it("does not trigger refetch when polling is stopped", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValue(ws);

      store.getState().startPolling({ pollInterval: 60000 });
      await vi.advanceTimersByTimeAsync(0);
      store.getState().stopPolling();

      const callCount = mockFetchWorkspaceApi.mock.calls.length;

      mockVisibilityState = "visible";
      mockDocument.dispatchEvent(new Event("visibilitychange"));

      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(callCount);
    });
  });

  describe("refetch", () => {
    it("forces a fresh fetch", async () => {
      const ws1 = makeWorkspace({ name: "ws-1" });
      const ws2 = makeWorkspace({ name: "ws-2" });
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws1);

      store
        .getState()
        .startPolling({ workspaceId: "ws-1", pollInterval: 60000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().workspace?.name).toBe("ws-1");

      mockFetchWorkspaceApi.mockResolvedValueOnce(ws2);
      store.getState().refetch();
      await vi.advanceTimersByTimeAsync(0);

      expect(store.getState().workspace?.name).toBe("ws-2");
    });
  });

  describe("upsertAgent", () => {
    it("keeps a newly-created agent when upsert runs before workspace data loads", async () => {
      store.getState().upsertAgent({
        name: "planner",
        role_name: "plan",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });

      mockFetchWorkspaceApi.mockResolvedValueOnce(
        makeWorkspace({ agents: [] }),
      );
      await store.getState().fetchWorkspace("ws-1");

      expect(store.getState().workspace?.agents).toEqual([
        {
          name: "planner",
          role_name: "plan",
          repos: ["hello-world"],
          repo_groups: [],
          cross_repo: false,
        },
      ]);
    });

    it("does not let a stale workspace fetch drop an optimistic agent", async () => {
      mockFetchWorkspaceApi.mockResolvedValueOnce(
        makeWorkspace({ agents: [] }),
      );
      await store.getState().fetchWorkspace("ws-1");

      store.getState().upsertAgent({
        name: "planner",
        role_name: "plan",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });

      mockFetchWorkspaceApi.mockResolvedValueOnce(
        makeWorkspace({ agents: [] }),
      );
      await store.getState().fetchWorkspace("ws-1");

      expect(store.getState().workspace?.agents).toEqual([
        {
          name: "planner",
          role_name: "plan",
          repos: ["hello-world"],
          repo_groups: [],
          cross_repo: false,
        },
      ]);
    });

    it("does not let an obsolete fetch clear pending optimistic agents", async () => {
      const obsoleteFetch = deferred<WorkspaceData>();
      const currentFetch = deferred<WorkspaceData>();
      mockFetchWorkspaceApi
        .mockReturnValueOnce(obsoleteFetch.promise)
        .mockReturnValueOnce(currentFetch.promise);

      const obsoleteRequest = store.getState().fetchWorkspace("ws-1");
      store.getState().upsertAgent({
        name: "planner",
        role_name: "plan",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });
      store.getState().refetch();

      obsoleteFetch.resolve(
        makeWorkspace({
          agents: [
            {
              name: "planner",
              role_name: "plan",
              backend: "codex",
              repos: ["hello-world"],
              repo_groups: [],
              cross_repo: false,
            },
          ],
        }),
      );
      await obsoleteRequest;

      currentFetch.resolve(makeWorkspace({ agents: [] }));
      await vi.advanceTimersByTimeAsync(0);

      expect(store.getState().workspace?.agents).toEqual([
        {
          name: "planner",
          role_name: "plan",
          repos: ["hello-world"],
          repo_groups: [],
          cross_repo: false,
        },
      ]);
    });

    it("uses the server copy once a pending optimistic agent appears in workspace data", async () => {
      store.getState().upsertAgent({
        name: "planner",
        role_name: "plan",
        backend: "opencode",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });

      mockFetchWorkspaceApi.mockResolvedValueOnce(
        makeWorkspace({
          agents: [
            {
              name: "planner",
              role_name: "plan",
              backend: "codex",
              repos: ["hello-world"],
              repo_groups: [],
              cross_repo: false,
            },
          ],
        }),
      );
      await store.getState().fetchWorkspace("ws-1");

      expect(store.getState().workspace?.agents).toEqual([
        {
          name: "planner",
          role_name: "plan",
          backend: "codex",
          repos: ["hello-world"],
          repo_groups: [],
          cross_repo: false,
        },
      ]);
    });

    it("adds a newly-created agent to current workspace state immediately", async () => {
      const ws = makeWorkspace({ agents: [] });
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws);
      await store.getState().fetchWorkspace("ws-1");

      store.getState().upsertAgent({
        name: "planner",
        role_name: "plan",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });

      expect(store.getState().workspace?.agents).toEqual([
        {
          name: "planner",
          role_name: "plan",
          repos: ["hello-world"],
          repo_groups: [],
          cross_repo: false,
        },
      ]);
    });

    it("updates an existing optimistic agent instead of duplicating it", async () => {
      const ws = makeWorkspace({
        agents: [
          {
            name: "planner",
            role_name: "task",
            repos: ["hello-world"],
            repo_groups: [],
            cross_repo: false,
          },
        ],
      });
      mockFetchWorkspaceApi.mockResolvedValueOnce(ws);
      await store.getState().fetchWorkspace("ws-1");

      store.getState().upsertAgent({
        name: "planner",
        role_name: "plan",
        backend: "opencode",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });

      expect(store.getState().workspace?.agents).toHaveLength(1);
      expect(store.getState().workspace?.agents[0]).toEqual({
        name: "planner",
        role_name: "plan",
        backend: "opencode",
        repos: ["hello-world"],
        repo_groups: [],
        cross_repo: false,
      });
    });
  });

  describe("reset", () => {
    it("clears all state and stops polling", async () => {
      const ws = makeWorkspace();
      mockFetchWorkspaceApi.mockResolvedValue(ws);

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);

      store.getState().reset();

      expect(store.getState().workspace).toBeNull();
      expect(store.getState().isLoading).toBe(false);
      expect(store.getState().error).toBeNull();

      const callCount = mockFetchWorkspaceApi.mock.calls.length;
      await vi.advanceTimersByTimeAsync(60000);
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(callCount);
    });
  });
});
