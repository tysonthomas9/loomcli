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
} from "../useEventProvider";
import type { MutationPayload } from "@/api/sse";

// Mock the get function from client.ts — default to 404 (open mode, no SSE token endpoint)
vi.mock("@/api/client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/api/client")>();
  return {
    ...mod,
    get: vi.fn().mockRejectedValue(new mod.ApiError(404, "Not Found")),
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

// Mock EventSource
class MockEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  static instances: MockEventSource[] = [];

  url: string;
  readyState: number = MockEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;

  private eventListeners: Map<string, ((e: MessageEvent) => void)[]> =
    new Map();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (e: MessageEvent) => void): void {
    if (!this.eventListeners.has(type)) {
      this.eventListeners.set(type, []);
    }
    this.eventListeners.get(type)!.push(listener);
  }

  removeEventListener(type: string, listener: (e: MessageEvent) => void): void {
    const listeners = this.eventListeners.get(type);
    if (listeners) {
      const index = listeners.indexOf(listener);
      if (index > -1) {
        listeners.splice(index, 1);
      }
    }
  }

  close(): void {
    this.readyState = MockEventSource.CLOSED;
  }

  simulateOpen(): void {
    this.readyState = MockEventSource.OPEN;
    this.onopen?.();
  }

  simulateError(readyState: number = MockEventSource.CONNECTING): void {
    this.readyState = readyState;
    this.onerror?.();
  }

  simulateMutation(data: unknown, lastEventId?: string): void {
    const listeners = this.eventListeners.get("mutation") ?? [];
    const parsed = data as { timestamp?: string };
    const eventId =
      lastEventId ??
      (parsed.timestamp ? String(Date.parse(parsed.timestamp)) : "");
    const event = {
      data: JSON.stringify(data),
      lastEventId: eventId,
    } as MessageEvent;
    for (const listener of listeners) {
      listener(event);
    }
  }

  static reset(): void {
    MockEventSource.instances = [];
  }

  static get lastInstance(): MockEventSource | undefined {
    return MockEventSource.instances.at(-1);
  }
}

async function flushConnect(): Promise<void> {
  await act(async () => {});
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <EventProvider>{children}</EventProvider>;
}

describe("useEventProvider", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    mockWorkspaceId = "test-ws-id";
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
  });

  afterEach(() => {
    global.EventSource = originalEventSource;
    vi.restoreAllMocks();
  });

  describe("Connection lifecycle", () => {
    it("creates exactly one BeadsSSEClient on mount", async () => {
      render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
    });

    it("destroys client on unmount", async () => {
      const { unmount } = render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      const esInstance = MockEventSource.lastInstance;
      unmount();

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });

    it("creates new client on workspaceId change", async () => {
      mockWorkspaceId = "ws-a";
      const { rerender } = render(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.instances[0].url).toContain("ws-a");

      const firstInstance = MockEventSource.lastInstance;

      mockWorkspaceId = "ws-b";
      rerender(
        <EventProvider>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.instances[1].url).toContain("ws-b");
    });
  });

  describe("Connection state via context", () => {
    it("exposes connection state that updates reactively", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      expect(result.current.state).toBe("connecting");

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("exposes reconnectAttempts", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.reconnectAttempts).toBe(0);

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.reconnectAttempts).toBe(1);
    });

    it("lastError stays null on transient errors (unified reconnect)", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);
      });

      // Unified reconnect: transient errors enter reconnecting, no onError
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "test-123",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      act(() => {
        MockEventSource.lastInstance?.simulateMutation(mutation);
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Should receive
      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      // Should NOT receive
      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
          type: "delete",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      // Should receive
      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
          type: "create",
          issue_id: "test-1",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
          type: "delete",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(2);
    });
  });

  describe("Unsubscribe", () => {
    it("unsubscribe stops future callbacks", async () => {
      const cb = vi.fn();

      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      let unsubscribe: () => void;
      act(() => {
        unsubscribe = result.current.subscribe(cb);
      });

      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
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
        MockEventSource.lastInstance?.simulateMutation({
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Send mutation — callback should fire
      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
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
        MockEventSource.lastInstance?.simulateMutation({
          type: "update",
          issue_id: "test-2",
          timestamp: "2025-01-23T12:00:01Z",
        });
      });

      expect(cb).toHaveBeenCalledTimes(1);
    });
  });

  describe("Error isolation", () => {
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
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

      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-a%2Crepo-b",
      );
    });

    it("reconnects when sourceRepos changes", async () => {
      const { rerender } = render(
        <EventProvider sourceRepos={["repo-a"]}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      const firstInstance = MockEventSource.lastInstance;

      rerender(
        <EventProvider sourceRepos={["repo-b"]}>
          <div>child</div>
        </EventProvider>,
      );
      await flushConnect();

      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-b",
      );
    });
  });

  describe("Outside provider", () => {
    it("useEventContext returns NO_EVENT_CONTEXT defaults outside provider", () => {
      const { result } = renderHook(() => useEventContext());

      expect(result.current.state).toBe("disconnected");
      expect(result.current.reconnectAttempts).toBe(0);
      expect(result.current.lastError).toBeNull();
      expect(result.current.isConnected).toBe(false);
      expect(typeof result.current.subscribe).toBe("function");
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      act(() => {
        window.dispatchEvent(new Event("auth-sign-out"));
      });

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Send mutation with first callback
      act(() => {
        MockEventSource.lastInstance?.simulateMutation({
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
        MockEventSource.lastInstance?.simulateMutation({
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
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.state).toBe("reconnecting");

      act(() => {
        result.current.retryNow();
      });
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(2);
    });

    it("disconnect delegates to client", async () => {
      const { result } = renderHook(() => useEventContext(), { wrapper });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.isConnected).toBe(true);

      act(() => {
        result.current.disconnect();
      });

      expect(result.current.state).toBe("disconnected");
    });
  });
});
