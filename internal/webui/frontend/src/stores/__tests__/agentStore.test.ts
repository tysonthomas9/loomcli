/**
 * Unit tests for agentStore.
 *
 * The store's lifecycle is SSE-driven: `start(workspaceId, subscribeFn)`
 * subscribes to `agent_state_change` mutations and refetches agent data on
 * each signal. Status/tasks (no SSE equivalent) keep a 5s polling timer.
 *
 * All tests use the vanilla store directly — no React rendering needed.
 * Uses vi.useFakeTimers() for timer control. Tests pass a mock subscribeFn
 * that captures the registered callback so signals can be emitted directly.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { createAgentStore, INITIAL_STATE } from "../agentStore";
import type { AgentStore } from "../agentStore";
import type { SubscribeFn } from "../issueStoreHelpers";
import type { StoreApi } from "zustand/vanilla";
import type { LoomAgentStatus } from "../../types";
import type { MutationPayload, MutationType } from "../../types";

// ---------------------------------------------------------------------------
// Minimal document mock for visibility change tests
// ---------------------------------------------------------------------------

const docListeners = new Map<string, Set<() => void>>();
let mockVisibilityState = "visible";

const mockDocument = {
  get visibilityState() {
    return mockVisibilityState;
  },
  addEventListener(event: string, handler: () => void) {
    if (!docListeners.has(event)) docListeners.set(event, new Set());
    docListeners.get(event)!.add(handler);
  },
  removeEventListener(event: string, handler: () => void) {
    docListeners.get(event)?.delete(handler);
  },
  dispatchEvent(event: Event) {
    docListeners.get(event.type)?.forEach((h) => h());
    return true;
  },
};

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

function makeMutation(
  type: MutationType,
  workspaceId?: string,
): MutationPayload {
  return {
    type,
    issue_id: "",
    timestamp: new Date().toISOString(),
    ...(workspaceId !== undefined ? { workspace_id: workspaceId } : {}),
  };
}

interface SubscribeHarness {
  subscribe: SubscribeFn;
  spy: ReturnType<typeof vi.fn>;
  emit: (mutation: MutationPayload) => void;
  unsubscribeSpy: ReturnType<typeof vi.fn>;
  listenerCount: () => number;
}

function makeSubscribe(): SubscribeHarness {
  const callbacks: Array<(m: MutationPayload) => void> = [];
  const unsubscribeSpy = vi.fn();
  const spy = vi.fn();
  const subscribe: SubscribeFn = (cb, opts) => {
    spy(cb, opts);
    callbacks.push(cb);
    return () => {
      unsubscribeSpy();
      const i = callbacks.indexOf(cb);
      if (i >= 0) callbacks.splice(i, 1);
    };
  };
  return {
    subscribe,
    spy,
    emit: (m) => callbacks.forEach((cb) => cb(m)),
    unsubscribeSpy,
    listenerCount: () => callbacks.length,
  };
}

function setupSuccessfulMocks(): void {
  mockFetchAgents.mockResolvedValue([makeAgent()]);
  mockFetchStatus.mockResolvedValue({
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
  });
  mockFetchTasks.mockResolvedValue({
    needsPlanning: [],
    readyToImplement: [
      { id: "task-1", title: "Test Task", priority: 1, status: "open" },
    ],
    needsReview: [],
    inProgress: [],
    backlog: [],
    done: [],
  });
}

// flushMicrotasks drains promise continuations without burning past the 15s
// timeout in fetchAgents/fetchStatus/fetchTasks. Tests with hung mocks rely
// on this — runAllTimersAsync would trip the fetch's internal timeout.
async function flushMicrotasks(rounds = 4): Promise<void> {
  for (let i = 0; i < rounds; i++) {
    await vi.advanceTimersByTimeAsync(0);
  }
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
    docListeners.clear();
    mockVisibilityState = "visible";
  });

  afterEach(() => {
    store.getState().reset();
    vi.useRealTimers();
  });

  // -----------------------------------------------------------------------
  // Initial state
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
  // start / stop lifecycle
  // -----------------------------------------------------------------------

  describe("start / stop lifecycle", () => {
    it("subscribes once with agent_state_change type filter", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(sub.spy).toHaveBeenCalledTimes(1);
      expect(sub.spy.mock.calls[0]![1]).toEqual({
        types: ["agent_state_change"],
      });
    });

    it("calls fetchAgents/fetchStatus/fetchTasks once on cold start", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(mockFetchAgents).toHaveBeenCalledTimes(1);
      expect(mockFetchAgents).toHaveBeenCalledWith("ws-1", expect.anything());
      expect(mockFetchStatus).toHaveBeenCalledTimes(1);
      expect(mockFetchTasks).toHaveBeenCalledTimes(1);
    });

    it("subscribe is invoked before the first fetchAgents call", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      const subscribeOrder = sub.spy.mock.invocationCallOrder[0]!;
      const fetchOrder = mockFetchAgents.mock.invocationCallOrder[0]!;
      expect(subscribeOrder).toBeLessThan(fetchOrder);
    });

    it("monitor 5s timer fires fetchStatus/fetchTasks every tick", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(mockFetchStatus).toHaveBeenCalledTimes(1);
      expect(mockFetchTasks).toHaveBeenCalledTimes(1);

      await vi.advanceTimersByTimeAsync(5000);
      expect(mockFetchStatus).toHaveBeenCalledTimes(2);
      expect(mockFetchTasks).toHaveBeenCalledTimes(2);

      await vi.advanceTimersByTimeAsync(5000);
      expect(mockFetchStatus).toHaveBeenCalledTimes(3);
      expect(mockFetchTasks).toHaveBeenCalledTimes(3);
    });

    it("monitor timer does NOT call fetchAgents (no fallback active)", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);

      await vi.advanceTimersByTimeAsync(15000);
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);
    });

    it("disposer tears down: no further fetches after dispose", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      const dispose = store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      const fetchAgentsCalls = mockFetchAgents.mock.calls.length;
      const fetchStatusCalls = mockFetchStatus.mock.calls.length;

      dispose();
      await vi.advanceTimersByTimeAsync(60000);

      expect(mockFetchAgents.mock.calls.length).toBe(fetchAgentsCalls);
      expect(mockFetchStatus.mock.calls.length).toBe(fetchStatusCalls);
    });

    it("disposer is idempotent (no throw on double-call)", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      const dispose = store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(() => dispose()).not.toThrow();
      expect(() => dispose()).not.toThrow();
      expect(sub.unsubscribeSpy).toHaveBeenCalledTimes(1);
    });

    it("emitting a signal after dispose does not call fetchAgents", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      const dispose = store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      const callsBefore = mockFetchAgents.mock.calls.length;

      dispose();
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      expect(mockFetchAgents.mock.calls.length).toBe(callsBefore);
      expect(sub.listenerCount()).toBe(0);
    });

    it("start('') is a no-op (no fetch, no subscribe, no timer)", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

      const dispose = store.getState().start("", sub.subscribe);
      await vi.advanceTimersByTimeAsync(30000);

      expect(sub.spy).not.toHaveBeenCalled();
      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(mockFetchStatus).not.toHaveBeenCalled();
      expect(mockFetchTasks).not.toHaveBeenCalled();
      expect(() => dispose()).not.toThrow();

      warnSpy.mockRestore();
    });

    it("calling start again tears down the previous subscription", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(sub.listenerCount()).toBe(1);

      store.getState().start("ws-2", sub.subscribe);
      await flushMicrotasks();

      expect(sub.unsubscribeSpy).toHaveBeenCalledTimes(1);
      expect(sub.listenerCount()).toBe(1);
    });
  });

  // -----------------------------------------------------------------------
  // fetchAgentData / fetchMonitorData isolation
  // -----------------------------------------------------------------------

  describe("fetchAgentData + fetchMonitorData isolation", () => {
    it("agent path drives isConnected on success", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      const state = store.getState();
      expect(state.agents).toHaveLength(1);
      expect(state.agents[0]!.name).toBe("nova");
      expect(state.isConnected).toBe(true);
      expect(state.connectionState).toBe("connected");
      expect(state.error).toBeNull();
      expect(state.lastUpdated).toBeTypeOf("number");
      expect(state.wasEverConnected).toBe(true);
    });

    it("agent path failure flips isConnected and schedules retry (when previously connected)", async () => {
      // First connect
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().isConnected).toBe(true);

      // Now fail on next refetch — emit an SSE signal with a rejecting fetch
      mockFetchAgents.mockRejectedValue(new Error("Network error"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      const state = store.getState();
      expect(state.isConnected).toBe(false);
      expect(state.error).toBeInstanceOf(Error);
      expect(state.error!.message).toBe("Network error");
      expect(state.retryCountdown).toBe(5);
    });

    it("monitor path failure logs warn and does NOT touch isConnected", async () => {
      mockFetchAgents.mockResolvedValue([makeAgent()]);
      mockFetchStatus.mockRejectedValue(new Error("Status failed"));
      mockFetchTasks.mockRejectedValue(new Error("Tasks failed"));

      const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      const state = store.getState();
      expect(state.isConnected).toBe(true);
      expect(state.error).toBeNull();
      expect(state.tasks).toEqual(INITIAL_STATE.tasks);
      expect(state.taskLists).toEqual(INITIAL_STATE.taskLists);
      expect(warnSpy).toHaveBeenCalledTimes(2);

      warnSpy.mockRestore();
    });

    it("populates monitor data on success", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      const state = store.getState();
      expect(state.tasks.needs_planning).toBe(1);
      expect(state.tasks.ready_to_implement).toBe(2);
      expect(state.agentTasks).toHaveProperty("nova");
      expect(state.sync.db_synced).toBe(true);
      expect(state.stats.open).toBe(5);
      expect(state.taskLists.readyToImplement).toHaveLength(1);
    });

    it("monitor failure does not prevent agent success", async () => {
      mockFetchAgents.mockResolvedValue([makeAgent()]);
      mockFetchStatus.mockRejectedValue(new Error("Status failed"));
      mockFetchTasks.mockResolvedValue({
        needsPlanning: [],
        readyToImplement: [],
        needsReview: [],
        inProgress: [],
        backlog: [],
        done: [],
      });
      const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(store.getState().isConnected).toBe(true);
      expect(store.getState().agents).toHaveLength(1);

      warnSpy.mockRestore();
    });
  });

  // -----------------------------------------------------------------------
  // Cold-start race
  // -----------------------------------------------------------------------

  describe("cold-start race", () => {
    it("coalesces signals during cold start into one refetch after resolve", async () => {
      let resolveAgents!: (v: LoomAgentStatus[]) => void;
      mockFetchAgents.mockImplementationOnce(
        () =>
          new Promise<LoomAgentStatus[]>((r) => {
            resolveAgents = r;
          }),
      );
      mockFetchAgents.mockResolvedValue([makeAgent()]);
      mockFetchStatus.mockResolvedValue({
        agents: [],
        tasks: INITIAL_STATE.tasks,
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: INITIAL_STATE.stats,
        timestamp: "",
      });
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);

      sub.emit(makeMutation("agent_state_change", "ws-1"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      // Cold start still in-flight — no extra fetches yet
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);

      resolveAgents([makeAgent()]);
      await flushMicrotasks();
      // Cold start + 1 coalesced refetch
      expect(mockFetchAgents).toHaveBeenCalledTimes(2);

      // Post-cold-start signal fires immediately
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(3);
    });

    it("no signal during cold start → no extra refetch after resolve", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(mockFetchAgents).toHaveBeenCalledTimes(1);
    });

    it("ignores signals for other workspaces", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);

      sub.emit(makeMutation("agent_state_change", "ws-2"));
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);

      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(2);
    });
  });

  // -----------------------------------------------------------------------
  // Fallback polling
  // -----------------------------------------------------------------------

  describe("fallback polling", () => {
    it("activates at threshold: immediate fetch + 5s interval", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      const baseline = mockFetchAgents.mock.calls.length;

      store.getState().setReconnectAttempts(10);
      await flushMicrotasks();
      // Immediate fetch
      expect(mockFetchAgents.mock.calls.length).toBe(baseline + 1);

      await vi.advanceTimersByTimeAsync(5000);
      // 5s interval tick (also a monitor tick — may also fire fetchStatus, but
      // those are tracked separately).
      expect(mockFetchAgents.mock.calls.length).toBe(baseline + 2);

      await vi.advanceTimersByTimeAsync(5000);
      expect(mockFetchAgents.mock.calls.length).toBe(baseline + 3);
    });

    it("deactivates below threshold", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      store.getState().setReconnectAttempts(10);
      await flushMicrotasks();
      const callsAtActivation = mockFetchAgents.mock.calls.length;

      store.getState().setReconnectAttempts(9);
      await vi.advanceTimersByTimeAsync(15000);
      // No further fallback-driven fetches
      expect(mockFetchAgents.mock.calls.length).toBe(callsAtActivation);
    });

    it("setReconnectAttempts(0) before threshold is a no-op", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      const calls = mockFetchAgents.mock.calls.length;

      store.getState().setReconnectAttempts(0);
      await flushMicrotasks();
      expect(mockFetchAgents.mock.calls.length).toBe(calls);
    });

    it("connectionLost flips on threshold", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().connectionLost).toBe(false);

      store.getState().setReconnectAttempts(10);
      expect(store.getState().connectionLost).toBe(true);
    });

    it("connectionLost remains true when reconnectAttempts drops (cleared via reportSuccess/retryNow)", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      store.getState().setReconnectAttempts(10);
      expect(store.getState().connectionLost).toBe(true);

      store.getState().setReconnectAttempts(0);
      expect(store.getState().connectionLost).toBe(true);
    });

    it("setReconnectAttempts(10) without an active session is a no-op", async () => {
      setupSuccessfulMocks();

      // No start() call — activeWorkspaceID is "".
      store.getState().setReconnectAttempts(10);
      // Advance to ensure no fallback timer was registered.
      await vi.advanceTimersByTimeAsync(15000);

      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(store.getState().connectionLost).toBe(false);
    });

    it("setReconnectAttempts(10) after reset is a no-op (no leaked interval)", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      store.getState().reset();

      mockFetchAgents.mockClear();
      store.getState().setReconnectAttempts(10);
      await vi.advanceTimersByTimeAsync(15000);

      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(store.getState().connectionLost).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // Cold-start race — generation guard for rapid start() re-entry
  // -----------------------------------------------------------------------

  describe("cold-start generation guard", () => {
    it("stale start's .finally does not clobber the new start's coldStartInFlight", async () => {
      // ws-1 cold-start hangs.
      let resolveWs1!: (v: LoomAgentStatus[]) => void;
      mockFetchAgents.mockImplementationOnce(
        () =>
          new Promise<LoomAgentStatus[]>((r) => {
            resolveWs1 = r;
          }),
      );
      // ws-2 cold-start also hangs so we can observe whether the SSE signal
      // mid-cold-start is coalesced or fires immediately.
      let resolveWs2!: (v: LoomAgentStatus[]) => void;
      mockFetchAgents.mockImplementationOnce(
        () =>
          new Promise<LoomAgentStatus[]>((r) => {
            resolveWs2 = r;
          }),
      );
      mockFetchAgents.mockResolvedValue([]);
      mockFetchStatus.mockResolvedValue({
        agents: [],
        tasks: INITIAL_STATE.tasks,
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: INITIAL_STATE.stats,
        timestamp: "",
      });
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);

      const sub = makeSubscribe();

      // 1. Start ws-1; ws-1 cold-start is hung.
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledTimes(1);

      // 2. Start ws-2 before ws-1 resolves. ws-1's controller is aborted,
      //    but its .finally is still pending until the rejection propagates.
      store.getState().start("ws-2", sub.subscribe);
      await flushMicrotasks();
      // Cold-start fetch fired for ws-2.
      expect(mockFetchAgents).toHaveBeenCalledTimes(2);
      // Resolve the (already-aborted) ws-1 fetch so its .finally runs.
      resolveWs1([makeAgent({ name: "ws1-stale" })]);
      await flushMicrotasks();

      // 3. With the generation guard, ws-1's .finally bails because
      //    startGeneration has incremented. coldStartInFlight remains true
      //    for ws-2. An SSE signal during ws-2's still-in-flight cold start
      //    must coalesce, NOT fire a third fetchAgents.
      const callsBefore = mockFetchAgents.mock.calls.length;
      sub.emit(makeMutation("agent_state_change", "ws-2"));
      sub.emit(makeMutation("agent_state_change", "ws-2"));
      await flushMicrotasks();
      expect(mockFetchAgents.mock.calls.length).toBe(callsBefore);

      // 4. Resolve ws-2 → coalesced refetch fires exactly once.
      resolveWs2([makeAgent()]);
      await flushMicrotasks();
      expect(mockFetchAgents.mock.calls.length).toBe(callsBefore + 1);
    });
  });

  // -----------------------------------------------------------------------
  // Backoff
  // -----------------------------------------------------------------------

  describe("backoff", () => {
    it("schedules retry after failure when previously connected", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().isConnected).toBe(true);

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      expect(store.getState().retryCountdown).toBe(5);

      // Countdown ticks
      await vi.advanceTimersByTimeAsync(1000);
      expect(store.getState().retryCountdown).toBe(4);

      // Retry fires after full delay → succeeds
      mockFetchAgents.mockResolvedValue([makeAgent()]);
      await vi.advanceTimersByTimeAsync(4000);
      await flushMicrotasks();
      // 1 (cold) + 1 (signal-fail) + 1 (retry-success) = 3
      expect(mockFetchAgents.mock.calls.length).toBe(3);
    });

    it("follows exponential backoff progression", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));

      // First failure: 5s retry
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(5);

      await vi.advanceTimersByTimeAsync(5000);
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(10);

      await vi.advanceTimersByTimeAsync(10000);
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(20);

      await vi.advanceTimersByTimeAsync(20000);
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(40);

      await vi.advanceTimersByTimeAsync(40000);
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(60);

      await vi.advanceTimersByTimeAsync(60000);
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(60);
    });

    it("does not schedule retry before first connection", async () => {
      mockFetchAgents.mockRejectedValue(new Error("fail"));
      mockFetchStatus.mockResolvedValue({
        agents: [],
        tasks: INITIAL_STATE.tasks,
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: INITIAL_STATE.stats,
        timestamp: "",
      });
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(store.getState().retryCountdown).toBe(0);
      expect(store.getState().wasEverConnected).toBe(false);

      // Advance well past the fallback threshold-less timer; no fallback
      // active and no scheduled retry, so no extra agent fetch.
      const initialCalls = mockFetchAgents.mock.calls.length;
      await vi.advanceTimersByTimeAsync(30000);
      // Monitor timer ticks fire status/tasks but never fetchAgents.
      expect(mockFetchAgents.mock.calls.length).toBe(initialCalls);
    });
  });

  // -----------------------------------------------------------------------
  // retryNow
  // -----------------------------------------------------------------------

  describe("retryNow", () => {
    it("resets backoff and fetches immediately", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      expect(store.getState().retryCountdown).toBe(5);

      mockFetchAgents.mockResolvedValue([makeAgent()]);
      const callsBefore = mockFetchAgents.mock.calls.length;
      store.getState().retryNow();
      await flushMicrotasks();

      expect(store.getState().connectionLost).toBe(false);
      expect(store.getState().retryCountdown).toBe(0);
      expect(mockFetchAgents.mock.calls.length).toBe(callsBefore + 1);
    });
  });

  // -----------------------------------------------------------------------
  // Stale banner
  // -----------------------------------------------------------------------

  describe("stale banner", () => {
    it("shows after 5s of disconnection", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      expect(store.getState().showStaleBanner).toBe(false);
      expect(store.getState().disconnectedSince).toBeTypeOf("number");

      await vi.advanceTimersByTimeAsync(5000);
      expect(store.getState().showStaleBanner).toBe(true);
    });

    it("clears on successful reconnect", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      await vi.advanceTimersByTimeAsync(5000);
      expect(store.getState().showStaleBanner).toBe(true);

      mockFetchAgents.mockResolvedValue([makeAgent()]);
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      expect(store.getState().showStaleBanner).toBe(false);
      expect(store.getState().disconnectedSince).toBeNull();
    });
  });

  // -----------------------------------------------------------------------
  // connectionLost (via backoff ceiling)
  // -----------------------------------------------------------------------

  describe("connectionLost", () => {
    it("becomes true after 5 failures at 60s ceiling", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));

      // Trigger first failure via SSE signal
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();
      // delay=5

      await vi.advanceTimersByTimeAsync(5000);
      await flushMicrotasks();
      // delay=10

      await vi.advanceTimersByTimeAsync(10000);
      await flushMicrotasks();
      // delay=20

      await vi.advanceTimersByTimeAsync(20000);
      await flushMicrotasks();
      // delay=40

      await vi.advanceTimersByTimeAsync(40000);
      await flushMicrotasks();
      // delay=60, first at ceiling
      expect(store.getState().connectionLost).toBe(false);

      // 4 more at ceiling = 5 total at ceiling
      for (let i = 0; i < 4; i++) {
        await vi.advanceTimersByTimeAsync(60000);
        await flushMicrotasks();
      }
      expect(store.getState().connectionLost).toBe(true);
    });

    it("resets on retryNow", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      // Drive past ceiling
      for (let i = 0; i < 20; i++) {
        await vi.advanceTimersByTimeAsync(60000);
        await flushMicrotasks();
      }

      mockFetchAgents.mockResolvedValue([makeAgent()]);
      store.getState().retryNow();
      await flushMicrotasks();
      expect(store.getState().connectionLost).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // Visibility change
  // -----------------------------------------------------------------------

  describe("visibility change", () => {
    it("refetches both paths on tab focus", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      const agentsBefore = mockFetchAgents.mock.calls.length;
      const statusBefore = mockFetchStatus.mock.calls.length;

      mockVisibilityState = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      await flushMicrotasks();

      expect(mockFetchAgents.mock.calls.length).toBe(agentsBefore + 1);
      expect(mockFetchStatus.mock.calls.length).toBe(statusBefore + 1);
    });

    it("does not refetch after dispose", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      const dispose = store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      dispose();

      mockFetchAgents.mockClear();
      mockFetchStatus.mockClear();

      mockVisibilityState = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      await flushMicrotasks();

      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(mockFetchStatus).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // getAgentByName
  // -----------------------------------------------------------------------

  describe("getAgentByName", () => {
    it("returns the matching agent", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      const agent = store.getState().getAgentByName("nova");
      expect(agent).toBeDefined();
      expect(agent!.name).toBe("nova");
    });

    it("returns undefined for nonexistent agent", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      expect(store.getState().getAgentByName("nonexistent")).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // reset
  // -----------------------------------------------------------------------

  describe("reset", () => {
    it("clears all state and tears down subscription/timers", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().agents).toHaveLength(1);

      store.getState().reset();

      const state = store.getState();
      expect(state.agents).toEqual([]);
      expect(state.isConnected).toBe(false);
      expect(state.wasEverConnected).toBe(false);
      expect(state.connectionState).toBe("never_connected");
      expect(state.lastUpdated).toBeNull();
      expect(sub.listenerCount()).toBe(0);

      mockFetchAgents.mockClear();
      mockFetchStatus.mockClear();
      await vi.advanceTimersByTimeAsync(60000);
      expect(mockFetchAgents).not.toHaveBeenCalled();
      expect(mockFetchStatus).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // Multiple instances
  // -----------------------------------------------------------------------

  describe("multiple instances", () => {
    it("creates independent stores", async () => {
      const store2 = createAgentStore();
      const sub = makeSubscribe();

      mockFetchAgents.mockResolvedValueOnce([makeAgent({ name: "alpha" })]);
      mockFetchAgents.mockResolvedValueOnce([makeAgent({ name: "beta" })]);
      mockFetchStatus.mockResolvedValue({
        agents: [],
        tasks: INITIAL_STATE.tasks,
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: INITIAL_STATE.stats,
        timestamp: "",
      });
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);

      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      const sub2 = makeSubscribe();
      store2.getState().start("ws-2", sub2.subscribe);
      await flushMicrotasks();

      expect(store.getState().agents[0]!.name).toBe("alpha");
      expect(store2.getState().agents[0]!.name).toBe("beta");

      store2.getState().reset();
    });
  });

  // -----------------------------------------------------------------------
  // connectionState derivation
  // -----------------------------------------------------------------------

  describe("connectionState derivation", () => {
    it("is 'connected' when isConnected is true", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().connectionState).toBe("connected");
    });

    it("is 'never_connected' initially", () => {
      expect(store.getState().connectionState).toBe("never_connected");
    });

    it("is 'never_connected' on first failure", async () => {
      mockFetchAgents.mockRejectedValue(new Error("fail"));
      mockFetchStatus.mockResolvedValue({
        agents: [],
        tasks: INITIAL_STATE.tasks,
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: INITIAL_STATE.stats,
        timestamp: "",
      });
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().connectionState).toBe("never_connected");
    });

    it("is 'reconnecting' during retry countdown", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      expect(store.getState().retryCountdown).toBeGreaterThan(0);
      expect(store.getState().connectionState).toBe("reconnecting");
    });

    it("is 'disconnected' when previously connected but no active retry", async () => {
      setupSuccessfulMocks();
      const sub = makeSubscribe();
      const dispose = store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      mockFetchAgents.mockRejectedValue(new Error("fail"));
      sub.emit(makeMutation("agent_state_change", "ws-1"));
      await flushMicrotasks();

      // Disposer clears retry timers and re-derives connectionState
      dispose();

      const state = store.getState();
      expect(state.wasEverConnected).toBe(true);
      expect(state.isConnected).toBe(false);
      expect(state.retryCountdown).toBe(0);
      expect(state.connectionState).toBe("disconnected");
    });
  });

  // -----------------------------------------------------------------------
  // Workspace snapshot binding (drops late writes after workspace switch)
  // -----------------------------------------------------------------------

  describe("workspace snapshot binding", () => {
    it("drops agents result when workspace switches mid-fetch", async () => {
      let resolvePrimary!: (v: LoomAgentStatus[]) => void;
      mockFetchAgents.mockImplementationOnce(
        () =>
          new Promise<LoomAgentStatus[]>((r) => {
            resolvePrimary = r;
          }),
      );
      mockFetchStatus.mockResolvedValue({
        agents: [],
        tasks: INITIAL_STATE.tasks,
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: INITIAL_STATE.stats,
        timestamp: "",
      });
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);

      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();
      expect(mockFetchAgents).toHaveBeenCalledWith("ws-1", expect.anything());

      // Switch to ws-2: tears down ws-1 (aborts in-flight) and starts new
      store.getState().start("ws-2", sub.subscribe);

      // Resolve the (now-aborted) ws-1 primary. The new fetch's
      // AbortController guards stale write.
      mockFetchAgents.mockResolvedValue([]);
      resolvePrimary([makeAgent({ name: "ws1-stale" })]);
      await flushMicrotasks();

      const state = store.getState();
      // ws1-stale must not appear
      expect(state.agents.find((a) => a.name === "ws1-stale")).toBeUndefined();
    });

    it("drops monitor status result when workspace switches mid-fetch", async () => {
      const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

      mockFetchAgents.mockResolvedValue([makeAgent()]);

      let resolveStaleStatus!: (v: {
        agents: LoomAgentStatus[];
        tasks: typeof INITIAL_STATE.tasks;
        agentTasks: Record<string, never>;
        sync: typeof INITIAL_STATE.sync;
        stats: typeof INITIAL_STATE.stats;
        timestamp: string;
      }) => void;
      mockFetchStatus.mockImplementationOnce(
        () =>
          new Promise((r) => {
            resolveStaleStatus = r;
          }),
      );
      const cleanStatus = {
        agents: [],
        tasks: { ...INITIAL_STATE.tasks, needs_planning: 1 },
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: { ...INITIAL_STATE.stats, open: 7 },
        timestamp: "",
      };
      mockFetchStatus.mockResolvedValue(cleanStatus);
      mockFetchTasks.mockResolvedValue(INITIAL_STATE.taskLists);

      const sub = makeSubscribe();
      store.getState().start("ws-1", sub.subscribe);
      await flushMicrotasks();

      store.getState().start("ws-2", sub.subscribe);
      await flushMicrotasks();
      expect(store.getState().stats.open).toBe(7);

      // Resolve the (aborted) ws-1 status. Without the controller-supersede
      // guard this would overwrite stats.open with 999.
      resolveStaleStatus({
        agents: [],
        tasks: { ...INITIAL_STATE.tasks, needs_planning: 999 },
        agentTasks: {},
        sync: INITIAL_STATE.sync,
        stats: { ...INITIAL_STATE.stats, open: 999 },
        timestamp: "",
      });
      await flushMicrotasks();

      expect(store.getState().stats.open).toBe(7);
      expect(store.getState().tasks.needs_planning).toBe(1);

      warnSpy.mockRestore();
    });
  });
});
