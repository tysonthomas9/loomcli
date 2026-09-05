/**
 * @vitest-environment jsdom
 */
import { renderHook, act, render } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import React from "react";

import {
  EventProvider,
  useEventContext,
  useEventSubscription,
  useResyncSubscription,
} from "../useEventProvider";
import type { MutationPayload } from "@/api/common";
import { QueryRecoveryContext } from "../queryRecovery";

const mockFetchEventSource = vi.hoisted(() => vi.fn());
vi.mock("@microsoft/fetch-event-source", () => ({
  EventStreamContentType: "text/event-stream",
  fetchEventSource: mockFetchEventSource,
}));

// The stream library is mocked below, but keep the token seam valid if used.
vi.mock("@/api/common/client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/api/common/client")>();
  return {
    ...mod,
    get: vi.fn().mockResolvedValue({ disabled: true }),
  };
});

let mockWorkspaceId = "test-ws-id";
vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: mockWorkspaceId }),
  };
});

interface MockFetchEventSourceOptions {
  headers: Record<string, string>;
  fetch?: typeof fetch;
  signal?: AbortSignal;
  onopen?: (response: Response) => Promise<void> | void;
  onmessage?: (event: { id: string; event: string; data: string }) => void;
  onerror?: (error: unknown) => number | void;
}

// Lightweight fetch-event-source double for hook behavior. The SSE client unit
// tests exercise the real library parser against a pushable ReadableStream.
class MockFetchEventSourceAttempt {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  static instances: MockFetchEventSourceAttempt[] = [];

  url: string;
  readyState: number = MockFetchEventSourceAttempt.CONNECTING;
  private options: MockFetchEventSourceOptions;

  constructor(url: string, options: MockFetchEventSourceOptions) {
    this.url = url;
    this.options = options;
    // The library hands custom fetch its mutable resume headers before parsing.
    // Exercise that seam so message callbacks observe the effective checkpoint.
    void options
      .fetch?.(url, {
        headers: options.headers,
        signal: options.signal,
      })
      .catch(() => {});
    MockFetchEventSourceAttempt.instances.push(this);
    options.signal?.addEventListener("abort", () => {
      this.readyState = MockFetchEventSourceAttempt.CLOSED;
    });
  }

  simulateOpen(): void {
    this.readyState = MockFetchEventSourceAttempt.OPEN;
    void this.options.onopen?.(
      new Response(null, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      }),
    );
  }

  simulateConnected(): void {
    this.options.onmessage?.({ id: "", event: "connected", data: "" });
  }

  private simulateId(id: string): void {
    if (id) this.options.headers["last-event-id"] = id;
    else delete this.options.headers["last-event-id"];
  }

  simulateResync(
    id: string,
    reason: "cap" | "error" | "expired" | "overflow",
  ): void {
    this.simulateId(id);
    this.options.onmessage?.({
      id,
      event: "resync",
      data: JSON.stringify({ reason }),
    });
  }

  simulateError(
    readyState: number = MockFetchEventSourceAttempt.CONNECTING,
  ): void {
    this.readyState = readyState;
    const retryDelay =
      this.options.onerror?.(new Error("stream failed")) ?? 1000;
    window.setTimeout(() => {
      if (!this.options.signal?.aborted) {
        new MockFetchEventSourceAttempt(this.url, this.options);
      }
    }, retryDelay);
  }

  simulateMutation(data: unknown, lastEventId?: string): void {
    const parsed = data as { timestamp?: string };
    const eventId =
      lastEventId ??
      (parsed.timestamp ? String(Date.parse(parsed.timestamp)) : "");
    this.simulateId(eventId);
    this.options.onmessage?.({
      event: "mutation",
      id: eventId,
      data: JSON.stringify(data),
    });
  }

  simulateRawMutation(data: string, lastEventId = ""): void {
    this.simulateId(lastEventId);
    try {
      this.options.onmessage?.({ event: "mutation", id: lastEventId, data });
    } catch (error) {
      // The actual library routes parser callback failures through onerror.
      try {
        this.options.onerror?.(error);
      } catch {
        this.readyState = MockFetchEventSourceAttempt.CLOSED;
      }
    }
  }

  static reset(): void {
    MockFetchEventSourceAttempt.instances = [];
  }

  static get lastInstance(): MockFetchEventSourceAttempt | undefined {
    return MockFetchEventSourceAttempt.instances.at(-1);
  }
}

async function flushConnect(): Promise<void> {
  await act(async () => {});
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <EventProvider>{children}</EventProvider>;
}

describe("useEventProvider", () => {
  beforeEach(() => {
    mockWorkspaceId = "test-ws-id";
    MockFetchEventSourceAttempt.reset();
    mockFetchEventSource.mockReset();
    vi.spyOn(window, "fetch").mockResolvedValue(
      new Response(null, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      }),
    );
    mockFetchEventSource.mockImplementation(
      (input: RequestInfo, options: MockFetchEventSourceOptions) => {
        const source = new MockFetchEventSourceAttempt(String(input), options);
        return new Promise<void>((resolve) => {
          options.signal?.addEventListener("abort", () => resolve());
          if (source.readyState === MockFetchEventSourceAttempt.CLOSED)
            resolve();
        });
      },
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("Connection lifecycle", () => {
    it("creates exactly one WorkspaceSSEClient on mount", async () => {
      render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockFetchEventSourceAttempt.instances.length).toBe(1);
    });

    it("destroys client on unmount", async () => {
      const { unmount } = render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      const esInstance = MockFetchEventSourceAttempt.lastInstance;
      unmount();

      expect(esInstance?.readyState).toBe(MockFetchEventSourceAttempt.CLOSED);
    });

    it("creates new client on workspaceId change", async () => {
      mockWorkspaceId = "ws-a";
      const { rerender } = render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockFetchEventSourceAttempt.instances.length).toBe(1);
      expect(MockFetchEventSourceAttempt.instances[0].url).toContain("ws-a");

      const firstInstance = MockFetchEventSourceAttempt.lastInstance;

      mockWorkspaceId = "ws-b";
      rerender(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(firstInstance?.readyState).toBe(
        MockFetchEventSourceAttempt.CLOSED,
      );
      expect(MockFetchEventSourceAttempt.instances.length).toBe(2);
      expect(MockFetchEventSourceAttempt.instances[1].url).toContain("ws-b");
    });
  });

  describe("Connection state via context", () => {
    it("exposes connection state that updates reactively", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      expect(result.current.state).toBe("connecting");

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connecting");
      expect(result.current.isConnected).toBe(false);
      expect(result.current.connectionEpoch).toBe(0);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
      expect(result.current.connectionEpoch).toBe(1);
    });

    it("increments connectionEpoch after each completed retry handshake", async () => {
      vi.useFakeTimers();
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });
      expect(result.current.connectionEpoch).toBe(1);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateError();
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(MockFetchEventSourceAttempt.instances).toHaveLength(2);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });
      expect(result.current.connectionEpoch).toBe(2);
    });

    it("ignores duplicate connected frames on the same open stream", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });

      expect(result.current.connectionEpoch).toBe(1);
    });

    it("starts registered recovery on resync and cancels it on repo scope change", async () => {
      let finish!: () => void;
      const pending = new Promise<void>((resolve) => {
        finish = resolve;
      });
      let signal: AbortSignal | undefined;
      const refresh = vi.fn((value: AbortSignal) => {
        signal = value;
        return pending;
      });
      function Participant(): null {
        const recovery = React.useContext(QueryRecoveryContext);
        React.useEffect(
          () => recovery?.register("test query", refresh),
          [recovery],
        );
        return null;
      }
      const view = render(
        <EventProvider sourceRepos={["repo-a"]}>
          <Participant />
        </EventProvider>,
      );
      await flushConnect();
      await act(async () => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateResync(
          "c1.saved",
          "overflow",
        );
        await Promise.resolve();
      });
      expect(refresh).toHaveBeenCalledTimes(1);
      expect(signal?.aborted).toBe(false);
      view.rerender(
        <EventProvider sourceRepos={["repo-b"]}>
          <Participant />
        </EventProvider>,
      );
      expect(signal?.aborted).toBe(true);
      await act(async () => {
        finish();
        await pending;
      });
      view.unmount();
    });

    it("handles handshake resync as one epoch and one legacy refresh without accepting its checkpoint", async () => {
      const mutationListener = vi.fn();
      const resyncListener = vi.fn();

      function Listener(): null {
        useEventSubscription(mutationListener);
        useResyncSubscription(resyncListener);
        return null;
      }

      const { result } = renderHook(() => useEventContext(), {
        wrapper: ({ children }) => (
          <EventProvider>
            <Listener />
            {children}
          </EventProvider>
        ),
      });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateResync(
          "c1.floor",
          "expired",
        );
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });

      expect(result.current.connectionEpoch).toBe(1);
      expect(resyncListener).toHaveBeenCalledOnce();
      expect(resyncListener).toHaveBeenCalledWith({
        from: undefined,
        // Scheduling refresh does not prove that the floor was observed.
        to: "",
        reason: "expired",
      });
      expect(mutationListener).toHaveBeenCalledOnce();
      expect(mutationListener).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "refresh",
          workspace_id: "test-ws-id",
        }),
      );
    });

    it("does not suppress the next open after a handshake resync stream fails", async () => {
      vi.useFakeTimers();
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateResync(
          "c1.floor",
          "expired",
        );
        MockFetchEventSourceAttempt.lastInstance?.simulateError();
      });
      expect(result.current.connectionEpoch).toBe(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(MockFetchEventSourceAttempt.instances).toHaveLength(2);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });

      expect(result.current.connectionEpoch).toBe(2);
    });

    it("exposes reconnectAttempts", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      expect(result.current.reconnectAttempts).toBe(0);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateError(
          MockFetchEventSourceAttempt.CONNECTING,
        );
      });

      expect(result.current.reconnectAttempts).toBe(1);
    });

    it("lastError stays null on transient errors (unified reconnect)", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        for (let attempt = 0; attempt < 5; attempt++) {
          MockFetchEventSourceAttempt.lastInstance?.simulateError(
            MockFetchEventSourceAttempt.CLOSED,
          );
        }
      });

      // Unified reconnect: even repeated transient errors enter reconnecting,
      // without surfacing an onError notice to the UI.
      expect(result.current.lastError).toBeNull();
      expect(result.current.state).toBe("reconnecting");
    });
  });

  describe("Subscriber fan-out", () => {
    it("multiple subscribers receive the same mutation event", async () => {
      const cb1 = vi.fn();
      const cb2 = vi.fn();

      function Sub1() {
        useEventSubscription(cb1);
        return null;
      }
      function Sub2() {
        useEventSubscription(cb2);
        return null;
      }

      render(
        <EventProvider>
          <Sub1 />
          <Sub2 />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "test-123",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation(mutation);
      });

      expect(cb1).toHaveBeenCalledWith(mutation);
      expect(cb2).toHaveBeenCalledWith(mutation);
    });
  });

  describe("Type-filtered subscriptions", () => {
    it("subscriber with types filter only receives matching mutations", async () => {
      const cb = vi.fn();

      function FilteredSub() {
        useEventSubscription(cb, { types: ["create", "update"] });
        return null;
      }

      render(
        <EventProvider>
          <FilteredSub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      // Should receive
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      // Should NOT receive
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "delete",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      // Should receive
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "update",
          issue_id: "test-3",
          timestamp: "2025-01-23T12:00:02Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(2);
      expect(cb).toHaveBeenCalledWith(
        expect.objectContaining({ type: "create" }),
      );
      expect(cb).toHaveBeenCalledWith(
        expect.objectContaining({ type: "update" }),
      );
    });

    it("empty types array receives all events", async () => {
      const cb = vi.fn();

      function Sub() {
        useEventSubscription(cb, { types: [] });
        return null;
      }

      render(
        <EventProvider>
          <Sub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "delete",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(2);
    });

    it("subscriber with entity/action filters only receives matching mutations", async () => {
      const cb = vi.fn();

      function FilteredSub() {
        useEventSubscription(cb, {
          entityTypes: ["terminal"],
          actions: ["terminal.metadata"],
        });
        return null;
      }

      render(
        <EventProvider>
          <FilteredSub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "terminal_metadata",
          entity_type: "terminal",
          entity_id: "session-1",
          action: "terminal.metadata",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "terminal_session_change",
          entity_type: "terminal",
          entity_id: "session-1",
          action: "terminal.session_change",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);
      expect(cb).toHaveBeenCalledWith(
        expect.objectContaining({ action: "terminal.metadata" }),
      );
    });

    it("subscriber with agent filters receives generic agent status events", async () => {
      const cb = vi.fn();

      function FilteredSub() {
        useEventSubscription(cb, {
          entityTypes: ["agent"],
          actions: ["agent.status"],
        });
        return null;
      }

      render(
        <EventProvider>
          <FilteredSub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "status",
          entity_type: "agent",
          entity_id: "agent-alpha",
          action: "agent.status",
          title: "agent-alpha",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "terminal_metadata",
          entity_type: "terminal",
          entity_id: "session-1",
          action: "terminal.metadata",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "update",
          entity_type: "issue",
          entity_id: "issue-1",
          issue_id: "issue-1",
          action: "issue.update",
          timestamp: "2025-01-23T12:00:02Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);
      expect(cb).toHaveBeenCalledWith(
        expect.objectContaining({
          entity_type: "agent",
          entity_id: "agent-alpha",
          action: "agent.status",
        }),
      );
    });
  });

  describe("Unsubscribe", () => {
    it("unsubscribe stops future callbacks", async () => {
      const cb = vi.fn();

      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      let unsubscribe: () => void;
      act(() => {
        unsubscribe = result.current.subscribe(cb);
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);

      act(() => {
        unsubscribe();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "update",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);
    });
  });

  describe("useEventSubscription cleanup", () => {
    it("automatically unsubscribes on component unmount", async () => {
      const cb = vi.fn();

      function Sub() {
        useEventSubscription(cb);
        return <div data-testid="sub">sub</div>;
      }

      const { rerender } = render(
        <EventProvider>
          <Sub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      // Send mutation — callback should fire
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);

      // Unmount Sub by re-rendering without it
      rerender(
        <EventProvider>
          <div>no sub</div>
        </EventProvider>,
      );

      // Send another mutation — callback should NOT fire
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "update",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);
    });
  });

  describe("Error isolation", () => {
    it("malformed mutation events stop the stream with a visible error", async () => {
      const cb = vi.fn();
      const observed = { state: "", error: null as string | null };
      const consoleWarnSpy = vi
        .spyOn(console, "warn")
        .mockImplementation(() => {});

      function AgentSub() {
        const context = useEventContext();
        observed.state = context.state;
        observed.error = context.lastError;
        useEventSubscription(cb, { entityTypes: ["agent"] });
        return null;
      }

      render(
        <EventProvider>
          <AgentSub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateRawMutation(
          "{not-json",
        );
      });

      expect(consoleWarnSpy).toHaveBeenCalledWith(
        "[SSE] Received malformed mutation event",
      );
      expect(cb).not.toHaveBeenCalled();
      expect(observed.state).toBe("disconnected");
      expect(observed.error).toBe("Malformed SSE mutation payload");
      expect(MockFetchEventSourceAttempt.lastInstance?.readyState).toBe(
        MockFetchEventSourceAttempt.CLOSED,
      );
    });

    it("a throwing subscriber does not prevent other subscribers from receiving events", async () => {
      const throwingCb = vi.fn(() => {
        throw new Error("subscriber error");
      });
      const safeCb = vi.fn();

      const consoleSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});

      function ThrowingSub() {
        useEventSubscription(throwingCb);
        return null;
      }
      function SafeSub() {
        useEventSubscription(safeCb);
        return null;
      }

      render(
        <EventProvider>
          <ThrowingSub />
          <SafeSub />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      expect(throwingCb).toHaveBeenCalledTimes(1);
      expect(safeCb).toHaveBeenCalledTimes(1);
      expect(consoleSpy).toHaveBeenCalled();

      consoleSpy.mockRestore();
    });
  });

  describe("sourceRepos", () => {
    it("passes sourceRepos to SSE client", async () => {
      render(
        <EventProvider sourceRepos={["repo-a", "repo-b"]}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockFetchEventSourceAttempt.lastInstance?.url).toContain(
        "source_repos=repo-a%2Crepo-b",
      );
    });

    it("reconnects the first undefined-to-array change with one handshake epoch and no refresh", async () => {
      const epochValues: number[] = [];
      const mutationListener = vi.fn();
      function EventProbe(): null {
        const { connectionEpoch } = useEventContext();
        React.useEffect(() => {
          epochValues.push(connectionEpoch);
        }, [connectionEpoch]);
        useEventSubscription(mutationListener);
        return null;
      }
      const { rerender } = render(
        <EventProvider sourceRepos={undefined}>
          <EventProbe />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });
      expect(epochValues).toEqual([0, 1]);

      const firstInstance = MockFetchEventSourceAttempt.lastInstance;
      rerender(
        <EventProvider sourceRepos={["repo-a"]}>
          <EventProbe />
        </EventProvider>,
      );
      await flushConnect();

      expect(firstInstance?.readyState).toBe(
        MockFetchEventSourceAttempt.CLOSED,
      );
      expect(MockFetchEventSourceAttempt.instances).toHaveLength(2);
      expect(MockFetchEventSourceAttempt.lastInstance?.url).toContain(
        "source_repos=repo-a",
      );
      expect(epochValues).toEqual([0, 1]);
      expect(mutationListener).not.toHaveBeenCalled();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });

      expect(epochValues).toEqual([0, 1, 2]);
      expect(mutationListener).not.toHaveBeenCalled();
    });

    it("reconnects without source-repository scoping when the filter is cleared", async () => {
      const { rerender } = render(
        <EventProvider sourceRepos={["repo-a"]}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();
      const firstInstance = MockFetchEventSourceAttempt.lastInstance;

      rerender(
        <EventProvider sourceRepos={undefined}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(firstInstance?.readyState).toBe(
        MockFetchEventSourceAttempt.CLOSED,
      );
      expect(MockFetchEventSourceAttempt.instances).toHaveLength(2);
      expect(MockFetchEventSourceAttempt.lastInstance?.url).not.toContain(
        "source_repos",
      );
    });

    it("does not reconnect when sourceRepos are only reordered", async () => {
      const { rerender } = render(
        <EventProvider sourceRepos={["repo-a", "repo-b"]}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();
      const firstInstance = MockFetchEventSourceAttempt.lastInstance;

      act(() => {
        firstInstance?.simulateOpen();
      });
      rerender(
        <EventProvider sourceRepos={["repo-b", "repo-a"]}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockFetchEventSourceAttempt.instances).toHaveLength(1);
      expect(firstInstance?.readyState).toBe(MockFetchEventSourceAttempt.OPEN);
    });

    it("reconnects when sourceRepos changes", async () => {
      let epoch = 0;
      function EpochProbe(): null {
        epoch = useEventContext().connectionEpoch;
        return null;
      }
      const { rerender } = render(
        <EventProvider sourceRepos={["repo-a"]}>
          <EpochProbe />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });
      expect(epoch).toBe(1);

      expect(MockFetchEventSourceAttempt.instances.length).toBe(1);
      const firstInstance = MockFetchEventSourceAttempt.lastInstance;

      rerender(
        <EventProvider sourceRepos={["repo-b"]}>
          <EpochProbe />
        </EventProvider>,
      );
      await flushConnect();

      expect(firstInstance?.readyState).toBe(
        MockFetchEventSourceAttempt.CLOSED,
      );
      expect(MockFetchEventSourceAttempt.instances.length).toBe(2);
      expect(MockFetchEventSourceAttempt.lastInstance?.url).toContain(
        "source_repos=repo-b",
      );
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });
      expect(epoch).toBe(2);
    });
  });

  describe("Outside provider", () => {
    it("useEventContext returns NO_EVENT_CONTEXT defaults outside provider", () => {
      const { result } = renderHook(() => useEventContext());

      expect(result.current.state).toBe("disconnected");
      expect(result.current.reconnectAttempts).toBe(0);
      expect(result.current.lastError).toBeNull();
      expect(result.current.isConnected).toBe(false);
      expect(result.current.connectionEpoch).toBe(0);
      expect(typeof result.current.subscribe).toBe("function");
      expect(typeof result.current.onResync).toBe("function");
      expect(typeof result.current.retryNow).toBe("function");
      expect(typeof result.current.disconnect).toBe("function");

      // subscribe returns a no-op unsubscribe
      const unsub = result.current.subscribe(() => {});
      expect(typeof unsub).toBe("function");
      unsub(); // should not throw
    });
  });

  describe("auth-sign-out", () => {
    it("destroys client on auth-sign-out event", async () => {
      render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      const esInstance = MockFetchEventSourceAttempt.lastInstance;

      act(() => {
        window.dispatchEvent(new Event("auth-sign-out"));
      });

      expect(esInstance?.readyState).toBe(MockFetchEventSourceAttempt.CLOSED);
    });
  });

  describe("Callback ref stability", () => {
    it("useEventSubscription uses ref for callback - identity changes do not re-subscribe", async () => {
      const cb1 = vi.fn();
      const cb2 = vi.fn();

      function Sub({ callback }: { callback: (m: MutationPayload) => void }) {
        useEventSubscription(callback);
        return null;
      }

      const { rerender } = render(
        <EventProvider>
          <Sub callback={cb1} />
        </EventProvider>,
      );
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      // Send mutation with first callback
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      expect(cb1).toHaveBeenCalledTimes(1);

      // Change callback identity
      rerender(
        <EventProvider>
          <Sub callback={cb2} />
        </EventProvider>,
      );

      // Send another mutation — should use new callback via ref
      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateMutation({
          type: "update",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb1).toHaveBeenCalledTimes(1); // not called again
      expect(cb2).toHaveBeenCalledTimes(1); // new callback used
    });
  });

  describe("Control methods", () => {
    it("retryNow delegates to client", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateError(
          MockFetchEventSourceAttempt.CONNECTING,
        );
      });

      expect(result.current.state).toBe("reconnecting");

      act(() => {
        result.current.retryNow();
      });
      await flushConnect();

      expect(MockFetchEventSourceAttempt.instances.length).toBe(2);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });
      expect(result.current.connectionEpoch).toBe(1);
    });

    it("disconnect delegates to client", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connecting");
      expect(result.current.isConnected).toBe(false);
      expect(result.current.connectionEpoch).toBe(0);

      act(() => {
        MockFetchEventSourceAttempt.lastInstance?.simulateConnected();
      });

      expect(result.current.isConnected).toBe(true);
      expect(result.current.connectionEpoch).toBe(1);

      act(() => {
        result.current.disconnect();
      });

      expect(result.current.state).toBe("disconnected");
    });
  });
});
