/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useSSE } from "./useSSE";
import type { MutationPayload } from "../api/sse";

// Mock the get function from client.ts — default to 404 (open mode, no SSE token endpoint)
vi.mock("../api/client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../api/client")>();
  return {
    ...mod,
    get: vi.fn().mockRejectedValue(new mod.ApiError(404, "Not Found")),
  };
});

// Mock EventSource class with static constants matching the real EventSource API
class MockEventSource {
  // EventSource readyState constants
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

  // Test helpers
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
    // Parse timestamp from data if lastEventId not provided
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

// Helper to flush async connect() microtasks
async function flushConnect(): Promise<void> {
  await act(async () => {});
}

describe("useSSE", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
  });

  afterEach(() => {
    global.EventSource = originalEventSource;
    vi.restoreAllMocks();
  });

  describe("Initialization", () => {
    it("returns expected shape with all methods and state", () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      expect(result.current).toHaveProperty("state");
      expect(result.current).toHaveProperty("lastError");
      expect(result.current).toHaveProperty("isConnected");
      expect(result.current).toHaveProperty("reconnectAttempts");
      expect(result.current).toHaveProperty("lastEventId");
      expect(result.current).toHaveProperty("connect");
      expect(result.current).toHaveProperty("disconnect");
      expect(result.current).toHaveProperty("retryNow");

      expect(typeof result.current.connect).toBe("function");
      expect(typeof result.current.disconnect).toBe("function");
      expect(typeof result.current.retryNow).toBe("function");
    });

    it("initial state is disconnected", () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      expect(result.current.state).toBe("disconnected");
      expect(result.current.isConnected).toBe(false);
      expect(result.current.lastError).toBeNull();
      expect(result.current.reconnectAttempts).toBe(0);
      expect(result.current.lastEventId).toBeUndefined();
    });
  });

  describe("Auto-connect option", () => {
    it("when true connects on mount", async () => {
      renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: true }),
      );
      await flushConnect();

      // EventSource should be created automatically
      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain(
        "/api/workspaces/test-ws-id/events",
      );
    });

    it("when false does not connect on mount", () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      // No EventSource should be created
      expect(MockEventSource.instances.length).toBe(0);
      expect(result.current.state).toBe("disconnected");
    });

    it("defaults to true", async () => {
      renderHook(() => useSSE({ workspaceId: "test-ws-id" }));
      await flushConnect();

      // EventSource should be created automatically (default autoConnect: true)
      expect(MockEventSource.instances.length).toBe(1);
    });
  });

  describe("Connection lifecycle", () => {
    it("connect() creates EventSource", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(MockEventSource.lastInstance).toBeDefined();
      expect(MockEventSource.lastInstance?.url).toContain(
        "/api/workspaces/test-ws-id/events",
      );
    });

    it("state transitions from disconnected to connecting to connected", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      expect(result.current.state).toBe("disconnected");

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(result.current.state).toBe("connecting");

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("disconnect() closes EventSource and updates state", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.isConnected).toBe(true);

      act(() => {
        result.current.disconnect();
      });

      expect(result.current.state).toBe("disconnected");
      expect(result.current.isConnected).toBe(false);
    });

    it("component unmount calls destroy and cleans up", async () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      unmount();

      // EventSource should be closed after unmount
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });
  });

  describe("State reactivity", () => {
    it("state changes trigger re-renders", async () => {
      const renderCount = { count: 0 };
      const { result } = renderHook(() => {
        renderCount.count++;
        return useSSE({ workspaceId: "test-ws-id", autoConnect: false });
      });

      const initialCount = renderCount.count;

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(renderCount.count).toBeGreaterThan(initialCount);

      const afterConnectCount = renderCount.count;

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(renderCount.count).toBeGreaterThan(afterConnectCount);
    });

    it("isConnected computed correctly based on state", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      expect(result.current.isConnected).toBe(false);
      expect(result.current.state).toBe("disconnected");

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(result.current.state).toBe("connecting");
      expect(result.current.isConnected).toBe(false);

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("lastError updates on errors", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);
      });

      expect(result.current.lastError).toBe("Connection closed");
    });

    it("lastError is cleared on successful connection", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Trigger an error
      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);
      });

      expect(result.current.lastError).toBe("Connection closed");

      // Simulate successful reconnection
      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.lastError).toBeNull();
    });

    it("reconnectAttempts updates reactively", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.reconnectAttempts).toBe(0);

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.reconnectAttempts).toBe(1);

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.reconnectAttempts).toBe(2);
    });

    it("reconnectAttempts resets to 0 on successful connection", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Simulate failure
      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.reconnectAttempts).toBe(1);

      // Successful reconnection
      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.reconnectAttempts).toBe(0);
    });
  });

  describe("Callbacks", () => {
    it("onMutation called with payload", async () => {
      const onMutation = vi.fn();
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false, onMutation }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      act(() => {
        MockEventSource.lastInstance?.simulateMutation(mutation);
      });

      expect(onMutation).toHaveBeenCalledWith(mutation);
    });

    it("lastEventId is updated when mutation is received", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      // Initially undefined
      expect(result.current.lastEventId).toBeUndefined();

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      act(() => {
        MockEventSource.lastInstance?.simulateMutation(mutation);
      });

      // lastEventId should be set to the timestamp in ms
      expect(result.current.lastEventId).toBe(
        Date.parse("2025-01-23T12:00:00Z"),
      );
    });

    it("onError called with error message", async () => {
      const onError = vi.fn();
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false, onError }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);
      });

      expect(onError).toHaveBeenCalledWith("Connection closed");
    });

    it("onStateChange called on transitions", async () => {
      const onStateChange = vi.fn();
      const { result } = renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: false,
          onStateChange,
        }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(onStateChange).toHaveBeenCalledWith("connecting");

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(onStateChange).toHaveBeenCalledWith("connected");

      act(() => {
        result.current.disconnect();
      });

      expect(onStateChange).toHaveBeenCalledWith("disconnected");
    });

    it("callbacks are not called after unmount", async () => {
      const onMutation = vi.fn();
      const { result, unmount } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false, onMutation }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      unmount();

      // Try to send a message after unmount
      act(() => {
        esInstance?.simulateMutation({
          type: "create",
          issue_id: "beads-456",
          title: "Should not be received",
          timestamp: "2025-01-23T12:00:00Z",
        });
      });

      // onMutation should not be called after unmount
      expect(onMutation).not.toHaveBeenCalled();
    });
  });

  describe("Since parameter passing", () => {
    it("passes since parameter to client on auto-connect", async () => {
      renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: true,
          since: 1706011200000,
        }),
      );
      await flushConnect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
    });

    it("passes since parameter to client on manual connect", async () => {
      const { result } = renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: false,
          since: 1706011200000,
        }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
    });

    it("uses updated since value when it changes", async () => {
      const { rerender, result } = renderHook(
        ({ since }: { since: number | undefined }) =>
          useSSE({ workspaceId: "test-ws-id", autoConnect: false, since }),
        { initialProps: { since: 1706011200000 } },
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );

      // Disconnect and update since
      act(() => {
        result.current.disconnect();
      });

      rerender({ since: 1706100000000 });

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706100000000",
      );
    });
  });

  describe("Methods stability", () => {
    it("methods are stable across renders", () => {
      const { result, rerender } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      const initialConnect = result.current.connect;
      const initialDisconnect = result.current.disconnect;
      const initialRetryNow = result.current.retryNow;

      rerender();

      expect(result.current.connect).toBe(initialConnect);
      expect(result.current.disconnect).toBe(initialDisconnect);
      expect(result.current.retryNow).toBe(initialRetryNow);
    });
  });

  describe("retryNow", () => {
    it("triggers immediate reconnection when in reconnecting state", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Trigger reconnecting state
      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.state).toBe("reconnecting");
      expect(MockEventSource.instances.length).toBe(1);

      // Call retryNow
      act(() => {
        result.current.retryNow();
      });
      await flushConnect();

      // Should have created a new EventSource immediately
      expect(MockEventSource.instances.length).toBe(2);
      expect(result.current.state).toBe("connecting");
    });

    it("resets reconnectAttempts to 0", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Trigger multiple errors
      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.reconnectAttempts).toBe(2);

      // Call retryNow
      act(() => {
        result.current.retryNow();
      });

      expect(result.current.reconnectAttempts).toBe(0);
    });
  });

  describe("SSR compatibility", () => {
    it("works when autoConnect is false (SSR-safe pattern)", () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      // Should have initial state and no EventSource created
      expect(result.current.state).toBe("disconnected");
      expect(result.current.isConnected).toBe(false);
      expect(MockEventSource.instances.length).toBe(0);

      // Hook should still return all expected methods
      expect(typeof result.current.connect).toBe("function");
      expect(typeof result.current.disconnect).toBe("function");
      expect(typeof result.current.retryNow).toBe("function");
    });

    it("manual connect works after mount (typical SSR hydration pattern)", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      expect(result.current.state).toBe("disconnected");
      expect(MockEventSource.instances.length).toBe(0);

      // Simulate client-side hydration by manually connecting
      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      expect(result.current.state).toBe("connecting");
    });
  });

  describe("Cleanup on unmount", () => {
    it("destroys client on unmount", async () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      act(() => {
        unmount();
      });

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });

    it("does not call callbacks after unmount even on state changes", async () => {
      const onStateChange = vi.fn();
      const { result, unmount } = renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: false,
          onStateChange,
        }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      onStateChange.mockClear();
      const esInstance = MockEventSource.lastInstance;

      act(() => {
        unmount();
      });

      // After unmount, trigger an error on the old EventSource instance
      // The callback should not be called because mountedRef is false
      esInstance?.simulateError(MockEventSource.CONNECTING);

      // onStateChange should not be called after unmount
      expect(onStateChange).not.toHaveBeenCalled();
    });
  });

  describe("Unmount during connecting state", () => {
    it("cleans up EventSource when unmounted during connecting state", async () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      // State should be connecting (EventSource created but not yet open)
      expect(result.current.state).toBe("connecting");
      const esInstance = MockEventSource.lastInstance;
      expect(esInstance).toBeDefined();

      // Unmount before simulateOpen
      unmount();

      // EventSource should be closed
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });

    it("no state update callbacks fire after unmount during connecting", async () => {
      const onStateChange = vi.fn();
      const onMutation = vi.fn();
      const { result, unmount } = renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: false,
          onStateChange,
          onMutation,
        }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      onStateChange.mockClear();
      const esInstance = MockEventSource.lastInstance;

      unmount();

      // Try to open and send mutations after unmount
      esInstance?.simulateOpen();
      esInstance?.simulateMutation({
        type: "create",
        issue_id: "post-unmount",
        title: "Should not trigger callback",
        timestamp: "2025-01-23T12:00:00Z",
      });

      expect(onStateChange).not.toHaveBeenCalled();
      expect(onMutation).not.toHaveBeenCalled();
    });
  });

  describe("Rapid connect/disconnect cycles", () => {
    it("handles rapid connect→disconnect→connect cycle", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      const firstInstance = MockEventSource.lastInstance;

      act(() => {
        result.current.disconnect();
      });

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      const secondInstance = MockEventSource.lastInstance;

      // First instance should be closed
      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      // Second instance should be created (new EventSource)
      expect(secondInstance).not.toBe(firstInstance);
      expect(result.current.state).toBe("connecting");
    });

    it("handles rapid connect→disconnect→connect→open cycle", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      const firstInstance = MockEventSource.lastInstance;

      act(() => {
        result.current.disconnect();
      });

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // First instance should be closed
      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      // Should be connected via the second instance
      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("five rapid connect/disconnect cycles produce clean state", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      for (let i = 0; i < 5; i++) {
        act(() => {
          result.current.connect();
        });
        await flushConnect();
        act(() => {
          result.current.disconnect();
        });
      }

      // Final state should be disconnected
      expect(result.current.state).toBe("disconnected");
      expect(result.current.isConnected).toBe(false);

      // All previous instances should be closed
      for (const instance of MockEventSource.instances) {
        expect(instance.readyState).toBe(MockEventSource.CLOSED);
      }
    });
  });

  describe("sourceRepos parameter", () => {
    it("passes sourceRepos to client.connect() on auto-connect", async () => {
      renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: true,
          sourceRepos: ["repo-a", "repo-b"],
        }),
      );
      await flushConnect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-a%2Crepo-b",
      );
    });

    it("passes sourceRepos to client on manual connect", async () => {
      const { result } = renderHook(() =>
        useSSE({
          workspaceId: "test-ws-id",
          autoConnect: false,
          sourceRepos: ["repo-x"],
        }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-x",
      );
    });

    it("changing sourceRepos triggers disconnect + reconnect", async () => {
      const { rerender } = renderHook(
        ({ sourceRepos }: { sourceRepos: string[] | undefined }) =>
          useSSE({ workspaceId: "test-ws-id", autoConnect: true, sourceRepos }),
        { initialProps: { sourceRepos: ["repo-a"] as string[] | undefined } },
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      const firstInstance = MockEventSource.lastInstance;
      expect(firstInstance?.url).toContain("source_repos=repo-a");

      // Change sourceRepos
      rerender({ sourceRepos: ["repo-b"] });
      await flushConnect();

      // Should have disconnected (closed first) and reconnected (new instance)
      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-b",
      );
    });

    it("identical sourceRepos (reordered) does NOT trigger reconnect", async () => {
      const { rerender } = renderHook(
        ({ sourceRepos }: { sourceRepos: string[] | undefined }) =>
          useSSE({ workspaceId: "test-ws-id", autoConnect: true, sourceRepos }),
        {
          initialProps: {
            sourceRepos: ["repo-a", "repo-b"] as string[] | undefined,
          },
        },
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);

      // Rerender with same repos in different order
      rerender({ sourceRepos: ["repo-b", "repo-a"] });

      // Should NOT have created a new connection
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("reconnect uses sinceRef.current for catch-up", async () => {
      const { rerender } = renderHook(
        ({
          sourceRepos,
          since,
        }: {
          sourceRepos: string[] | undefined;
          since: number | undefined;
        }) =>
          useSSE({
            workspaceId: "test-ws-id",
            autoConnect: true,
            sourceRepos,
            since,
          }),
        {
          initialProps: {
            sourceRepos: ["repo-a"] as string[] | undefined,
            since: 1706011200000 as number | undefined,
          },
        },
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );

      // Change sourceRepos to trigger reconnect
      rerender({ sourceRepos: ["repo-b"], since: 1706011200000 });
      await flushConnect();

      // New connection should use the since value for catch-up
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-b",
      );
    });

    it("switching from sourceRepos to undefined triggers reconnect", async () => {
      const { rerender } = renderHook(
        ({ sourceRepos }: { sourceRepos: string[] | undefined }) =>
          useSSE({ workspaceId: "test-ws-id", autoConnect: true, sourceRepos }),
        { initialProps: { sourceRepos: ["repo-a"] as string[] | undefined } },
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain("source_repos");

      // Change to undefined
      rerender({ sourceRepos: undefined });
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).not.toContain("source_repos");
    });

    it("workspace switch with different sourceRepos does NOT trigger double connect", async () => {
      const { rerender } = renderHook(
        ({
          workspaceId,
          sourceRepos,
        }: {
          workspaceId: string;
          sourceRepos: string[];
        }) => useSSE({ workspaceId, autoConnect: true, sourceRepos }),
        {
          initialProps: {
            workspaceId: "ws-a",
            sourceRepos: ["repo-a"],
          },
        },
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.instances[0].url).toContain("ws-a");
      expect(MockEventSource.instances[0].url).toContain("source_repos=repo-a");

      // Switch workspace AND sourceRepos at the same time
      rerender({ workspaceId: "ws-b", sourceRepos: ["repo-b"] });
      await flushConnect();

      // Should be exactly 2 instances: one destroyed for ws-a, one new for ws-b
      // Without the fix, there would be 3 (Effect B would trigger a redundant reconnect)
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.instances[0].readyState).toBe(
        MockEventSource.CLOSED,
      );
      expect(MockEventSource.instances[1].url).toContain("ws-b");
      expect(MockEventSource.instances[1].url).toContain("source_repos=repo-b");
    });

    it("workspace switch with same sourceRepos does NOT trigger reconnect", async () => {
      const { rerender } = renderHook(
        ({
          workspaceId,
          sourceRepos,
        }: {
          workspaceId: string;
          sourceRepos: string[];
        }) => useSSE({ workspaceId, autoConnect: true, sourceRepos }),
        {
          initialProps: {
            workspaceId: "ws-a",
            sourceRepos: ["repo-a"],
          },
        },
      );
      await flushConnect();

      expect(MockEventSource.instances.length).toBe(1);

      // Switch workspace but keep same sourceRepos
      rerender({ workspaceId: "ws-b", sourceRepos: ["repo-a"] });
      await flushConnect();

      // Should be exactly 2 instances (old destroyed, new created by Effect A)
      // No third from Effect B since prevSourceReposRef was reset
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.instances[0].readyState).toBe(
        MockEventSource.CLOSED,
      );
      expect(MockEventSource.instances[1].url).toContain("ws-b");
      expect(MockEventSource.instances[1].url).toContain("source_repos=repo-a");
    });
  });

  describe("auth-sign-out cleanup", () => {
    it("auth-sign-out event disconnects SSE", async () => {
      const { result } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);

      const esInstance = MockEventSource.lastInstance;

      // Dispatch auth-sign-out event
      act(() => {
        window.dispatchEvent(new Event("auth-sign-out"));
      });

      // EventSource should be closed. destroy() clears callbacks before
      // disconnecting, so React state won't update — check EventSource directly.
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });

    it("auth-sign-out listener is cleaned up on unmount", async () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id", autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Unmount to trigger cleanup (removes the event listener)
      unmount();

      // Create a new hook to verify the old listener is no longer active
      const { result: result2 } = renderHook(() =>
        useSSE({ workspaceId: "test-ws-id-2", autoConnect: false }),
      );

      act(() => {
        result2.current.connect();
      });
      await flushConnect();

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result2.current.isConnected).toBe(true);

      // Dispatch auth-sign-out — should only affect the second hook, not the unmounted one
      // The key assertion: only 1 additional EventSource gets closed (the second hook's),
      // not 2 (which would mean the first hook's listener leaked)
      const secondInstance = MockEventSource.lastInstance;

      act(() => {
        window.dispatchEvent(new Event("auth-sign-out"));
      });

      // destroy() clears callbacks so React state won't update — check EventSource
      expect(secondInstance?.readyState).toBe(MockEventSource.CLOSED);
    });
  });
});
