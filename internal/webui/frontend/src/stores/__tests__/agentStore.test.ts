/**
 * Unit tests for agentStore.
 * All tests use the vanilla store directly — no React rendering needed.
 * Uses vi.useFakeTimers() for timer control.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { createAgentStore, INITIAL_STATE } from "../agentStore";
import type { AgentStore } from "../agentStore";
import type { StoreApi } from "zustand/vanilla";
import type { LoomAgentStatus } from "../../types";
import type { FetchStatusResult } from "../../api/agents";

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

// Assign to globalThis so the store can access it
Object.defineProperty(globalThis, "document", {
  value: mockDocument,
  writable: true,
  configurable: true,
});

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("../../api/agents", () => ({
  fetchAgents: vi.fn(),
  fetchStatus: vi.fn(),
  fetchTasks: vi.fn(),
}));

import { fetchAgents, fetchStatus, fetchTasks } from "../../api/agents";

const mockFetchAgents = vi.mocked(fetchAgents);
const mockFetchStatus = vi.mocked(fetchStatus);
const mockFetchTasks = vi.mocked(fetchTasks);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "nova",
    branch: "nova-branch",
    status: "ready",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

function makeStatusResult(
  overrides: Partial<FetchStatusResult> = {},
): FetchStatusResult {
  return {
    agents: [makeAgent()],
    tasks: {
      needs_planning: 1,
      ready_to_implement: 2,
      in_progress: 3,
      need_review: 0,
      backlog: 0,
      epics: 0,
    },
    agentTasks: {
      nova: { id: "task-1", title: "Test Task", priority: 1, status: "open" },
    },
    taskLists: {
      needsPlanning: [],
      readyToImplement: [
        { id: "task-1", title: "Test Task", priority: 1, status: "open" },
      ],
      needsReview: [],
      inProgress: [],
      backlog: [],
      done: [],
    },
    sync: {
      db_synced: true,
      db_last_sync: "2026-01-01T00:00:00Z",
      git_needs_push: 0,
      git_needs_pull: 0,
    },
    stats: {
      open: 5,
      closed: 3,
      total: 8,
      completion: 37.5,
      remaining: 5,
      in_progress: 2,
      review: 1,
      blocked: 0,
    },
    timestamp: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function setupSuccessfulMocks(): void {
  mockFetchStatus.mockResolvedValue(makeStatusResult());
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("agentStore", () => {
  let store: StoreApi<AgentStore>;

  beforeEach(() => {
    vi.useFakeTimers();
    store = createAgentStore();
    vi.clearAllMocks();
    listeners.clear();
    mockVisibilityState = "visible";
  });

  afterEach(() => {
    store.getState().reset();
    vi.useRealTimers();
  });

  describe("refreshForRecovery", () => {
    it("rejects a coordinator workspace different from the configured store before fetching", async () => {
      setupSuccessfulMocks();
      store.getState().startPolling({ workspaceId: "old", pollInterval: 0 });
      await vi.advanceTimersByTimeAsync(0);
      mockFetchStatus.mockClear();
      await expect(
        store
          .getState()
          .refreshForRecovery(new AbortController().signal, "new"),
      ).rejects.toThrow("workspace mismatch");
      expect(mockFetchStatus).not.toHaveBeenCalled();
      await expect(
        store
          .getState()
          .refreshForRecovery(new AbortController().signal, "old"),
      ).resolves.toBeUndefined();
      expect(mockFetchStatus).toHaveBeenCalledWith("old");
    });
    it("rejects API failure while legacy fetchData still reports it through state", async () => {
      const failure = new Error("status unavailable");
      mockFetchStatus.mockRejectedValue(failure);
      await expect(
        store.getState().refreshForRecovery(new AbortController().signal),
      ).rejects.toBe(failure);
      expect(store.getState().error).toBe(failure);
      await expect(store.getState().fetchData()).resolves.toBeUndefined();
    });

    it("starts a new request and never accepts an older in-flight success", async () => {
      const old = deferred<FetchStatusResult>();
      const fresh = deferred<FetchStatusResult>();
      mockFetchStatus
        .mockReturnValueOnce(old.promise)
        .mockReturnValueOnce(fresh.promise);
      const legacy = store.getState().fetchData();
      const recovered = vi.fn();
      const recovery = store
        .getState()
        .refreshForRecovery(new AbortController().signal)
        .then(recovered);
      expect(mockFetchStatus).toHaveBeenCalledTimes(2);
      old.resolve(makeStatusResult({ agents: [makeAgent({ name: "old" })] }));
      await legacy;
      expect(recovered).not.toHaveBeenCalled();
      expect(store.getState().agents).toEqual([]);
      fresh.resolve(
        makeStatusResult({ agents: [makeAgent({ name: "fresh" })] }),
      );
      await recovery;
      expect(recovered).toHaveBeenCalledOnce();
      expect(store.getState().agents[0]?.name).toBe("fresh");
    });

    it("rejects immediately on workspace changes and ignores the old response", async () => {
      setupSuccessfulMocks();
      store.getState().startPolling({ workspaceId: "old", pollInterval: 0 });
      await vi.advanceTimersByTimeAsync(0);
      const old = deferred<FetchStatusResult>();
      mockFetchStatus
        .mockReturnValueOnce(old.promise)
        .mockResolvedValueOnce(
          makeStatusResult({ agents: [makeAgent({ name: "new" })] }),
        );
      const recovery = store
        .getState()
        .refreshForRecovery(new AbortController().signal);
      const rejected = expect(recovery).rejects.toThrow("workspace changed");
      store.getState().startPolling({ workspaceId: "new", pollInterval: 0 });
      await rejected;
      await vi.advanceTimersByTimeAsync(0);
      old.resolve(makeStatusResult({ agents: [makeAgent({ name: "old" })] }));
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().agents[0]?.name).toBe("new");
      expect(mockFetchStatus).toHaveBeenLastCalledWith("new");
    });

    it("rejects an aborted recovery without waiting for the API and cannot commit later", async () => {
      const request = deferred<FetchStatusResult>();
      mockFetchStatus.mockReturnValue(request.promise);
      const controller = new AbortController();
      const recovery = store.getState().refreshForRecovery(controller.signal);
      const reason = new Error("repo scope changed");
      const rejected = expect(recovery).rejects.toBe(reason);
      controller.abort(reason);
      await rejected;
      expect(store.getState().isLoading).toBe(false);
      request.resolve(makeStatusResult());
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().lastUpdated).toBeNull();
      expect(store.getState().agents).toEqual([]);
    });

    it("rejects superseded recovery even if the older request later succeeds", async () => {
      const old = deferred<FetchStatusResult>();
      mockFetchStatus
        .mockReturnValueOnce(old.promise)
        .mockResolvedValueOnce(makeStatusResult());
      const first = store
        .getState()
        .refreshForRecovery(new AbortController().signal);
      const rejected = expect(first).rejects.toThrow("superseded");
      await store.getState().refreshForRecovery(new AbortController().signal);
      await rejected;
      old.resolve(makeStatusResult({ agents: [] }));
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().agents).toHaveLength(1);
    });

    it("does not start or supersede a request with an already aborted signal", async () => {
      const controller = new AbortController();
      controller.abort(new Error("cancelled"));
      await expect(
        store.getState().refreshForRecovery(controller.signal),
      ).rejects.toThrow("cancelled");
      expect(mockFetchStatus).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 1. Initial state
  // -----------------------------------------------------------------------

  describe("initial state", () => {
    it("matches INITIAL_STATE", () => {
      const state = store.getState();
      expect(state.agents).toEqual(INITIAL_STATE.agents);
      expect(state.tasks).toEqual(INITIAL_STATE.tasks);
      expect(state.taskLists).toEqual(INITIAL_STATE.taskLists);
      expect(state.agentTasks).toEqual(INITIAL_STATE.agentTasks);
      expect(state.sync).toEqual(INITIAL_STATE.sync);
      expect(state.stats).toEqual(INITIAL_STATE.stats);
      expect(state.isLoading).toBe(false);
      expect(state.error).toBeNull();
      expect(state.isConnected).toBe(false);
      expect(state.connectionState).toBe("never_connected");
      expect(state.wasEverConnected).toBe(false);
      expect(state.retryCountdown).toBe(0);
      expect(state.showStaleBanner).toBe(false);
      expect(state.connectionLost).toBe(false);
      expect(state.disconnectedSince).toBeNull();
      expect(state.lastUpdated).toBeNull();
    });
  });

  // -----------------------------------------------------------------------
  // 2. fetchData — success
  // -----------------------------------------------------------------------

  describe("fetchData", () => {
    it("populates agents and sets connected state on success", async () => {
      setupSuccessfulMocks();

      await store.getState().fetchData();

      const state = store.getState();
      expect(state.agents).toHaveLength(1);
      expect(state.agents[0]!.name).toBe("nova");
      expect(state.isConnected).toBe(true);
      expect(state.connectionState).toBe("connected");
      expect(state.error).toBeNull();
      expect(state.lastUpdated).toBeTypeOf("number");
      expect(state.isLoading).toBe(false);
      expect(state.wasEverConnected).toBe(true);
    });

    // -----------------------------------------------------------------------
    // 3. fetchData — status response hydrates full monitor state
    // -----------------------------------------------------------------------

    it("uses one status fetch and populates full monitor state", async () => {
      setupSuccessfulMocks();

      await store.getState().fetchData();

      const state = store.getState();
      expect(mockFetchStatus).toHaveBeenCalledOnce();
      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(mockFetchTasks).not.toHaveBeenCalled();

      expect(state.tasks.needs_planning).toBe(1);
      expect(state.tasks.ready_to_implement).toBe(2);
      expect(state.agentTasks).toHaveProperty("nova");
      expect(state.sync.db_synced).toBe(true);
      expect(state.stats.open).toBe(5);
      expect(state.taskLists.readyToImplement).toHaveLength(1);
    });

    // -----------------------------------------------------------------------
    // 4. fetchData — primary failure
    // -----------------------------------------------------------------------

    it("sets error and disconnected state on primary failure", async () => {
      mockFetchStatus.mockRejectedValue(new Error("Network error"));

      await store.getState().fetchData();

      const state = store.getState();
      expect(state.error).toBeInstanceOf(Error);
      expect(state.error!.message).toBe("Network error");
      expect(state.isConnected).toBe(false);
      expect(state.connectionState).toBe("never_connected");
      expect(state.isLoading).toBe(false);
    });

    // -----------------------------------------------------------------------
    // 5. fetchData — split endpoint reduction
    // -----------------------------------------------------------------------

    it("does not use split agents or tasks endpoints", async () => {
      setupSuccessfulMocks();

      await store.getState().fetchData();

      const state = store.getState();
      expect(state.isConnected).toBe(true);
      expect(mockFetchStatus).toHaveBeenCalledOnce();
      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(mockFetchTasks).not.toHaveBeenCalled();
    });

    // -----------------------------------------------------------------------
    // 6. fetchData — overlap prevention
    // -----------------------------------------------------------------------

    it("prevents overlapping fetches", async () => {
      let resolve!: (value: FetchStatusResult) => void;
      mockFetchStatus.mockReturnValue(
        new Promise<FetchStatusResult>((r) => {
          resolve = r;
        }),
      );

      // Start first fetch (doesn't resolve yet)
      const p1 = store.getState().fetchData();
      // Second call should be no-op
      const p2 = store.getState().fetchData();

      resolve(makeStatusResult());
      await p1;
      await p2;

      expect(mockFetchStatus).toHaveBeenCalledOnce();
    });
  });

  // -----------------------------------------------------------------------
  // 7-9. Polling
  // -----------------------------------------------------------------------

  describe("polling", () => {
    it("starts initial fetch and polls on interval", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0); // initial fetch

      expect(mockFetchStatus).toHaveBeenCalledOnce();

      await vi.advanceTimersByTimeAsync(5000); // first interval
      expect(mockFetchStatus).toHaveBeenCalledTimes(2);

      await vi.advanceTimersByTimeAsync(5000); // second interval
      expect(mockFetchStatus).toHaveBeenCalledTimes(3);
    });

    it("restarts polling when called again", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchStatus).toHaveBeenCalledOnce();

      store.getState().startPolling({ pollInterval: 10000 });
      await vi.advanceTimersByTimeAsync(0); // new initial fetch
      expect(mockFetchStatus).toHaveBeenCalledTimes(2);

      // At 10s only the new interval fires (not the old 5s one)
      await vi.advanceTimersByTimeAsync(10000);
      expect(mockFetchStatus).toHaveBeenCalledTimes(3);
    });

    it("stops polling and clears timers", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchStatus).toHaveBeenCalledOnce();

      store.getState().stopPolling();
      await vi.advanceTimersByTimeAsync(60000);
      // No additional fetches after stop
      expect(mockFetchStatus).toHaveBeenCalledOnce();
    });

    it("starts with pollInterval=0 (initial fetch only)", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 0 });
      await vi.advanceTimersByTimeAsync(0);
      expect(mockFetchStatus).toHaveBeenCalledOnce();

      await vi.advanceTimersByTimeAsync(30000);
      expect(mockFetchStatus).toHaveBeenCalledOnce();
    });

    it("passes workspaceId to monitor API fetches", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ workspaceId: "ws-1", pollInterval: 0 });
      await vi.advanceTimersByTimeAsync(0);
      await vi.runAllTimersAsync();

      expect(mockFetchStatus).toHaveBeenCalledWith("ws-1");
      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(mockFetchTasks).not.toHaveBeenCalled();
    });

    it("supersedes an in-flight request when the workspace changes", async () => {
      const oldFetch = deferred<FetchStatusResult>();
      const newFetch = deferred<FetchStatusResult>();
      mockFetchStatus.mockImplementation((workspaceId) =>
        workspaceId === "ws-old" ? oldFetch.promise : newFetch.promise,
      );

      store.getState().startPolling({ workspaceId: "ws-old", pollInterval: 0 });
      store.getState().startPolling({ workspaceId: "ws-new", pollInterval: 0 });

      expect(mockFetchStatus.mock.calls).toEqual([["ws-old"], ["ws-new"]]);

      oldFetch.resolve(
        makeStatusResult({ agents: [makeAgent({ name: "old-agent" })] }),
      );
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().agents).toEqual([]);

      newFetch.resolve(
        makeStatusResult({ agents: [makeAgent({ name: "new-agent" })] }),
      );
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().agents[0]?.name).toBe("new-agent");
    });
  });

  // -----------------------------------------------------------------------
  // 10-12. Backoff
  // -----------------------------------------------------------------------

  describe("backoff", () => {
    it("schedules retry after failure when previously connected", async () => {
      // First: connect successfully
      setupSuccessfulMocks();
      await store.getState().fetchData();
      expect(store.getState().isConnected).toBe(true);

      // Then: fail
      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();

      expect(store.getState().retryCountdown).toBe(5);

      // Countdown ticks
      await vi.advanceTimersByTimeAsync(1000);
      expect(store.getState().retryCountdown).toBe(4);

      // Retry fires after full delay
      mockFetchStatus.mockResolvedValue(makeStatusResult());
      await vi.advanceTimersByTimeAsync(4000);
      expect(mockFetchStatus).toHaveBeenCalledTimes(3); // initial + fail + retry
    });

    it("follows exponential backoff progression", async () => {
      // Connect first
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));

      // First failure: 5s retry
      await store.getState().fetchData();
      expect(store.getState().retryCountdown).toBe(5);

      // Let retry fire → second failure: 10s retry
      await vi.advanceTimersByTimeAsync(5000);
      await vi.advanceTimersByTimeAsync(0); // let fetchData resolve
      expect(store.getState().retryCountdown).toBe(10);

      // Third failure: 20s retry
      await vi.advanceTimersByTimeAsync(10000);
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().retryCountdown).toBe(20);

      // Fourth failure: 40s retry
      await vi.advanceTimersByTimeAsync(20000);
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().retryCountdown).toBe(40);

      // Fifth failure: 60s (capped)
      await vi.advanceTimersByTimeAsync(40000);
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().retryCountdown).toBe(60);

      // Sixth failure: still 60s (capped)
      await vi.advanceTimersByTimeAsync(60000);
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().retryCountdown).toBe(60);
    });

    it("does not schedule retry before first connection", async () => {
      mockFetchStatus.mockRejectedValue(new Error("fail"));

      await store.getState().fetchData();

      expect(store.getState().retryCountdown).toBe(0);
      expect(store.getState().wasEverConnected).toBe(false);

      // Advance 60s — no retry
      await vi.advanceTimersByTimeAsync(60000);
      expect(mockFetchStatus).toHaveBeenCalledOnce();
    });
  });

  // -----------------------------------------------------------------------
  // 13. retryNow
  // -----------------------------------------------------------------------

  describe("retryNow", () => {
    it("resets backoff and fetches immediately", async () => {
      // Connect then disconnect
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();
      expect(store.getState().retryCountdown).toBe(5);

      // retryNow
      mockFetchStatus.mockResolvedValue(makeStatusResult());
      store.getState().retryNow();
      await vi.advanceTimersByTimeAsync(0);

      expect(store.getState().connectionLost).toBe(false);
      expect(store.getState().retryCountdown).toBe(0);
      expect(mockFetchStatus).toHaveBeenCalledTimes(3);
    });
  });

  // -----------------------------------------------------------------------
  // 14-16. Stale banner
  // -----------------------------------------------------------------------

  describe("stale banner", () => {
    it("shows after 5s of disconnection", async () => {
      // Connect first
      setupSuccessfulMocks();
      await store.getState().fetchData();

      // Fail
      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();

      expect(store.getState().showStaleBanner).toBe(false);
      expect(store.getState().disconnectedSince).toBeTypeOf("number");

      // Advance 5s
      await vi.advanceTimersByTimeAsync(5000);
      expect(store.getState().showStaleBanner).toBe(true);
    });

    it("clears on successful reconnect", async () => {
      // Connect → fail → show banner
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();
      await vi.advanceTimersByTimeAsync(5000);
      expect(store.getState().showStaleBanner).toBe(true);

      // Reconnect
      mockFetchStatus.mockResolvedValue(makeStatusResult());
      await store.getState().fetchData();

      expect(store.getState().showStaleBanner).toBe(false);
      expect(store.getState().disconnectedSince).toBeNull();
    });

    it("never shows if recovery happens within 5s", async () => {
      // Connect → fail → recover within 5s
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();

      // Advance 3s (less than 5s)
      await vi.advanceTimersByTimeAsync(3000);
      expect(store.getState().showStaleBanner).toBe(false);

      // Recover
      mockFetchStatus.mockResolvedValue(makeStatusResult());
      await store.getState().fetchData();
      expect(store.getState().showStaleBanner).toBe(false);

      // Advance past 5s total — still false
      await vi.advanceTimersByTimeAsync(5000);
      expect(store.getState().showStaleBanner).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // 17-18. connectionLost
  // -----------------------------------------------------------------------

  describe("connectionLost", () => {
    it("becomes true after 5 failures at 60s ceiling", async () => {
      // Connect first
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));

      // Exhaust backoff to reach 60s ceiling: 5→10→20→40→60
      // Each retry fires fetchData again automatically
      await store.getState().fetchData(); // fail 1, delay=5
      await vi.advanceTimersByTimeAsync(5000);
      await vi.advanceTimersByTimeAsync(0); // fail 2, delay=10
      await vi.advanceTimersByTimeAsync(10000);
      await vi.advanceTimersByTimeAsync(0); // fail 3, delay=20
      await vi.advanceTimersByTimeAsync(20000);
      await vi.advanceTimersByTimeAsync(0); // fail 4, delay=40
      await vi.advanceTimersByTimeAsync(40000);
      await vi.advanceTimersByTimeAsync(0); // fail 5, delay=60, first at ceiling
      expect(store.getState().connectionLost).toBe(false);

      // Now 5 more at ceiling
      for (let i = 0; i < 4; i++) {
        await vi.advanceTimersByTimeAsync(60000);
        await vi.advanceTimersByTimeAsync(0);
      }
      // After 5 failures at ceiling
      expect(store.getState().connectionLost).toBe(true);
    });

    it("resets on retryNow", async () => {
      // Force connectionLost state
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();
      // Manually advance through backoff to get to ceiling and accumulate failures
      for (let i = 0; i < 20; i++) {
        await vi.advanceTimersByTimeAsync(60000);
        await vi.advanceTimersByTimeAsync(0);
      }

      // retryNow resets
      mockFetchStatus.mockResolvedValue(makeStatusResult());
      store.getState().retryNow();
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().connectionLost).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // 19. Hung request recovery
  // -----------------------------------------------------------------------

  describe("hung request recovery", () => {
    it("recovers from a request whose promise never settles", async () => {
      // There is no watchdog: withTimeout races every request against
      // FETCH_TIMEOUT_MS, so even a promise that never settles releases the
      // lock, and the next poll tick fetches again. This test exists to catch
      // anyone removing that race, or reintroducing a watchdog below the
      // request timeout that would discard healthy slow requests instead.
      mockFetchStatus.mockReturnValueOnce(
        new Promise<FetchStatusResult>(() => {}),
      );
      mockFetchStatus.mockResolvedValue(
        makeStatusResult({ agents: [makeAgent({ name: "recovered" })] }),
      );

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(25_000);

      expect(store.getState().agents[0]?.name).toBe("recovered");
    });

    it("leaves a slow but healthy request alone", async () => {
      const slow = deferred<FetchStatusResult>();
      mockFetchStatus.mockReturnValueOnce(slow.promise);
      mockFetchStatus.mockResolvedValue(makeStatusResult());

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(14_000);
      slow.resolve(makeStatusResult({ agents: [makeAgent({ name: "slow" })] }));
      await vi.advanceTimersByTimeAsync(0);

      expect(mockFetchStatus).toHaveBeenCalledOnce();
      expect(store.getState().agents[0]?.name).toBe("slow");
    });
  });

  // -----------------------------------------------------------------------
  // 20. Visibility change
  // -----------------------------------------------------------------------

  describe("visibility change", () => {
    it("refetches on tab focus when polling", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0); // initial fetch

      expect(mockFetchStatus).toHaveBeenCalledOnce();

      // Simulate visibility change
      mockVisibilityState = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      await vi.advanceTimersByTimeAsync(0);

      expect(mockFetchStatus).toHaveBeenCalledTimes(2);
    });

    it("coalesces a focus refetch with an in-flight request", async () => {
      const inFlight = deferred<FetchStatusResult>();
      mockFetchStatus.mockReturnValue(inFlight.promise);

      store.getState().startPolling({ pollInterval: 0 });
      expect(mockFetchStatus).toHaveBeenCalledOnce();

      mockVisibilityState = "visible";
      document.dispatchEvent(new Event("visibilitychange"));

      inFlight.resolve(makeStatusResult());
      await vi.advanceTimersByTimeAsync(0);

      expect(mockFetchStatus).toHaveBeenCalledOnce();
      expect(store.getState().isConnected).toBe(true);
    });

    it("does not refetch when polling is stopped", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      store.getState().stopPolling();

      mockFetchStatus.mockClear();

      mockVisibilityState = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      await vi.advanceTimersByTimeAsync(0);

      expect(mockFetchStatus).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 22. getAgentByName
  // -----------------------------------------------------------------------

  describe("getAgentByName", () => {
    it("returns the matching agent", async () => {
      setupSuccessfulMocks();
      await store.getState().fetchData();

      const agent = store.getState().getAgentByName("nova");
      expect(agent).toBeDefined();
      expect(agent!.name).toBe("nova");
    });

    it("returns undefined for nonexistent agent", async () => {
      setupSuccessfulMocks();
      await store.getState().fetchData();

      expect(store.getState().getAgentByName("nonexistent")).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // 23. reset
  // -----------------------------------------------------------------------

  describe("reset", () => {
    it("clears all state and stops polling", async () => {
      setupSuccessfulMocks();

      store.getState().startPolling({ pollInterval: 5000 });
      await vi.advanceTimersByTimeAsync(0);
      expect(store.getState().agents).toHaveLength(1);

      store.getState().reset();

      const state = store.getState();
      expect(state.agents).toEqual([]);
      expect(state.isConnected).toBe(false);
      expect(state.wasEverConnected).toBe(false);
      expect(state.connectionState).toBe("never_connected");
      expect(state.lastUpdated).toBeNull();

      // Verify timers cleared
      mockFetchStatus.mockClear();
      await vi.advanceTimersByTimeAsync(60000);
      expect(mockFetchStatus).not.toHaveBeenCalled();
    });

    it("ignores a request that resolves after reset", async () => {
      const oldFetch = deferred<FetchStatusResult>();
      mockFetchStatus.mockReturnValueOnce(oldFetch.promise);

      const request = store.getState().fetchData();
      store.getState().reset();

      oldFetch.resolve(
        makeStatusResult({ agents: [makeAgent({ name: "old-agent" })] }),
      );
      await request;

      expect(store.getState().agents).toEqual([]);
      expect(store.getState().isConnected).toBe(false);
      expect(store.getState().lastUpdated).toBeNull();
    });
  });

  // -----------------------------------------------------------------------
  // 24. Multiple instances
  // -----------------------------------------------------------------------

  describe("multiple instances", () => {
    it("creates independent stores", async () => {
      const store2 = createAgentStore();

      mockFetchStatus.mockResolvedValue(
        makeStatusResult({ agents: [makeAgent({ name: "alpha" })] }),
      );
      await store.getState().fetchData();

      mockFetchStatus.mockResolvedValue(
        makeStatusResult({ agents: [makeAgent({ name: "beta" })] }),
      );
      await store2.getState().fetchData();

      expect(store.getState().agents[0]!.name).toBe("alpha");
      expect(store2.getState().agents[0]!.name).toBe("beta");

      store2.getState().reset();
    });
  });

  // -----------------------------------------------------------------------
  // 25. connectionState derivation
  // -----------------------------------------------------------------------

  describe("connectionState derivation", () => {
    it("is 'connected' when isConnected is true", async () => {
      setupSuccessfulMocks();
      await store.getState().fetchData();
      expect(store.getState().connectionState).toBe("connected");
    });

    it("is 'never_connected' initially", () => {
      expect(store.getState().connectionState).toBe("never_connected");
    });

    it("is 'never_connected' on first failure", async () => {
      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();
      expect(store.getState().connectionState).toBe("never_connected");
    });

    it("is 'reconnecting' during retry countdown", async () => {
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();

      expect(store.getState().retryCountdown).toBeGreaterThan(0);
      expect(store.getState().connectionState).toBe("reconnecting");
    });

    it("is 'disconnected' when connected previously but no active retry", async () => {
      setupSuccessfulMocks();
      await store.getState().fetchData();

      mockFetchStatus.mockRejectedValue(new Error("fail"));
      await store.getState().fetchData();

      // stopPolling clears retry timers and re-derives connectionState
      store.getState().stopPolling();

      const state = store.getState();
      expect(state.wasEverConnected).toBe(true);
      expect(state.isConnected).toBe(false);
      expect(state.retryCountdown).toBe(0);
      expect(state.connectionState).toBe("disconnected");
    });
  });
});
