/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useSSE } from "./useSSE";
import type { MutationPayload } from "../api/sse";

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
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

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
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      expect(result.current.state).toBe("disconnected");
      expect(result.current.isConnected).toBe(false);
      expect(result.current.lastError).toBeNull();
      expect(result.current.reconnectAttempts).toBe(0);
      expect(result.current.lastEventId).toBeUndefined();
    });
  });

  describe("Auto-connect option", () => {
    it("when true connects on mount", () => {
      renderHook(() => useSSE({ autoConnect: true }));

      // EventSource should be created automatically
      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain("/api/events");
    });

    it("when false does not connect on mount", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      // No EventSource should be created
      expect(MockEventSource.instances.length).toBe(0);
      expect(result.current.state).toBe("disconnected");
    });

    it("defaults to true", () => {
      renderHook(() => useSSE());

      // EventSource should be created automatically (default autoConnect: true)
      expect(MockEventSource.instances.length).toBe(1);
    });
  });

  describe("Connection lifecycle", () => {
    it("connect() creates EventSource", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

      expect(MockEventSource.lastInstance).toBeDefined();
      expect(MockEventSource.lastInstance?.url).toContain("/api/events");
    });

    it("state transitions from disconnected to connecting to connected", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      expect(result.current.state).toBe("disconnected");

      act(() => {
        result.current.connect();
      });

      expect(result.current.state).toBe("connecting");

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("disconnect() closes EventSource and updates state", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

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

    it("component unmount calls destroy and cleans up", () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });

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
    it("state changes trigger re-renders", () => {
      const renderCount = { count: 0 };
      const { result } = renderHook(() => {
        renderCount.count++;
        return useSSE({ autoConnect: false });
      });

      const initialCount = renderCount.count;

      act(() => {
        result.current.connect();
      });

      expect(renderCount.count).toBeGreaterThan(initialCount);

      const afterConnectCount = renderCount.count;

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(renderCount.count).toBeGreaterThan(afterConnectCount);
    });

    it("isConnected computed correctly based on state", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      expect(result.current.isConnected).toBe(false);
      expect(result.current.state).toBe("disconnected");

      act(() => {
        result.current.connect();
      });

      expect(result.current.state).toBe("connecting");
      expect(result.current.isConnected).toBe(false);

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("lastError updates on errors", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);
      });

      expect(result.current.lastError).toBe("Connection closed");
    });

    it("lastError is cleared on successful connection", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

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

    it("reconnectAttempts updates reactively", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

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

    it("reconnectAttempts resets to 0 on successful connection", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

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
    it("onMutation called with payload", () => {
      const onMutation = vi.fn();
      const { result } = renderHook(() =>
        useSSE({
          autoConnect: false,
          onMutation,
        }),
      );

      act(() => {
        result.current.connect();
      });

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

    it("lastEventId is updated when mutation is received", () => {
      const { result } = renderHook(() =>
        useSSE({
          autoConnect: false,
        }),
      );

      // Initially undefined
      expect(result.current.lastEventId).toBeUndefined();

      act(() => {
        result.current.connect();
      });

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

    it("onError called with error message", () => {
      const onError = vi.fn();
      const { result } = renderHook(() =>
        useSSE({
          autoConnect: false,
          onError,
        }),
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);
      });

      expect(onError).toHaveBeenCalledWith("Connection closed");
    });

    it("onStateChange called on transitions", () => {
      const onStateChange = vi.fn();
      const { result } = renderHook(() =>
        useSSE({
          autoConnect: false,
          onStateChange,
        }),
      );

      act(() => {
        result.current.connect();
      });

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

    it("callbacks are not called after unmount", () => {
      const onMutation = vi.fn();
      const { result, unmount } = renderHook(() =>
        useSSE({
          autoConnect: false,
          onMutation,
        }),
      );

      act(() => {
        result.current.connect();
      });

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
    it("passes since parameter to client on auto-connect", () => {
      renderHook(() =>
        useSSE({
          autoConnect: true,
          since: 1706011200000,
        }),
      );

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
    });

    it("passes since parameter to client on manual connect", () => {
      const { result } = renderHook(() =>
        useSSE({
          autoConnect: false,
          since: 1706011200000,
        }),
      );

      act(() => {
        result.current.connect();
      });

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
    });

    it("uses updated since value when it changes", () => {
      const { rerender, result } = renderHook(
        ({ since }: { since: number | undefined }) =>
          useSSE({
            autoConnect: false,
            since,
          }),
        { initialProps: { since: 1706011200000 } },
      );

      act(() => {
        result.current.connect();
      });

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

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706100000000",
      );
    });
  });

  describe("Methods stability", () => {
    it("methods are stable across renders", () => {
      const { result, rerender } = renderHook(() =>
        useSSE({ autoConnect: false }),
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
    it("triggers immediate reconnection when in reconnecting state", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

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

      // Should have created a new EventSource immediately
      expect(MockEventSource.instances.length).toBe(2);
      expect(result.current.state).toBe("connecting");
    });

    it("resets reconnectAttempts to 0", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

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
    // Note: These tests verify the hook's SSR guard behavior.
    // The actual SSR check `typeof window === 'undefined'` cannot be fully tested
    // in jsdom since window exists and React needs it for cleanup.
    // These tests verify the hook works correctly in jsdom environment.

    it("works when autoConnect is false (SSR-safe pattern)", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      // Should have initial state and no EventSource created
      expect(result.current.state).toBe("disconnected");
      expect(result.current.isConnected).toBe(false);
      expect(MockEventSource.instances.length).toBe(0);

      // Hook should still return all expected methods
      expect(typeof result.current.connect).toBe("function");
      expect(typeof result.current.disconnect).toBe("function");
      expect(typeof result.current.retryNow).toBe("function");
    });

    it("manual connect works after mount (typical SSR hydration pattern)", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      expect(result.current.state).toBe("disconnected");
      expect(MockEventSource.instances.length).toBe(0);

      // Simulate client-side hydration by manually connecting
      act(() => {
        result.current.connect();
      });

      expect(MockEventSource.instances.length).toBe(1);
      expect(result.current.state).toBe("connecting");
    });
  });

  describe("Cleanup on unmount", () => {
    it("destroys client on unmount", () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      act(() => {
        unmount();
      });

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });

    it("does not call callbacks after unmount even on state changes", () => {
      const onStateChange = vi.fn();
      const { result, unmount } = renderHook(() =>
        useSSE({
          autoConnect: false,
          onStateChange,
        }),
      );

      act(() => {
        result.current.connect();
      });

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
    it("cleans up EventSource when unmounted during connecting state", () => {
      const { result, unmount } = renderHook(() =>
        useSSE({ autoConnect: false }),
      );

      act(() => {
        result.current.connect();
      });

      // State should be connecting (EventSource created but not yet open)
      expect(result.current.state).toBe("connecting");
      const esInstance = MockEventSource.lastInstance;
      expect(esInstance).toBeDefined();

      // Unmount before simulateOpen
      unmount();

      // EventSource should be closed
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
    });

    it("no state update callbacks fire after unmount during connecting", () => {
      const onStateChange = vi.fn();
      const onMutation = vi.fn();
      const { result, unmount } = renderHook(() =>
        useSSE({
          autoConnect: false,
          onStateChange,
          onMutation,
        }),
      );

      act(() => {
        result.current.connect();
      });

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
    it("handles rapid connect→disconnect→connect cycle", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

      const firstInstance = MockEventSource.lastInstance;

      act(() => {
        result.current.disconnect();
      });

      act(() => {
        result.current.connect();
      });

      const secondInstance = MockEventSource.lastInstance;

      // First instance should be closed
      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      // Second instance should be created (new EventSource)
      expect(secondInstance).not.toBe(firstInstance);
      expect(result.current.state).toBe("connecting");
    });

    it("handles rapid connect→disconnect→connect→open cycle", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      act(() => {
        result.current.connect();
      });

      const firstInstance = MockEventSource.lastInstance;

      act(() => {
        result.current.disconnect();
      });

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // First instance should be closed
      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      // Should be connected via the second instance
      expect(result.current.state).toBe("connected");
      expect(result.current.isConnected).toBe(true);
    });

    it("five rapid connect/disconnect cycles produce clean state", () => {
      const { result } = renderHook(() => useSSE({ autoConnect: false }));

      for (let i = 0; i < 5; i++) {
        act(() => {
          result.current.connect();
        });
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
    it("passes sourceRepos to client.connect() on auto-connect", () => {
      renderHook(() =>
        useSSE({
          autoConnect: true,
          sourceRepos: ["repo-a", "repo-b"],
        }),
      );

      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-a%2Crepo-b",
      );
    });

    it("passes sourceRepos to client on manual connect", () => {
      const { result } = renderHook(() =>
        useSSE({
          autoConnect: false,
          sourceRepos: ["repo-x"],
        }),
      );

      act(() => {
        result.current.connect();
      });

      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-x",
      );
    });

    it("changing sourceRepos triggers disconnect + reconnect", () => {
      const { rerender } = renderHook(
        ({ sourceRepos }: { sourceRepos: string[] | undefined }) =>
          useSSE({
            autoConnect: true,
            sourceRepos,
          }),
        { initialProps: { sourceRepos: ["repo-a"] as string[] | undefined } },
      );

      expect(MockEventSource.instances.length).toBe(1);
      const firstInstance = MockEventSource.lastInstance;
      expect(firstInstance?.url).toContain("source_repos=repo-a");

      // Change sourceRepos
      rerender({ sourceRepos: ["repo-b"] });

      // Should have disconnected (closed first) and reconnected (new instance)
      expect(firstInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-b",
      );
    });

    it("identical sourceRepos (reordered) does NOT trigger reconnect", () => {
      const { rerender } = renderHook(
        ({ sourceRepos }: { sourceRepos: string[] | undefined }) =>
          useSSE({
            autoConnect: true,
            sourceRepos,
          }),
        {
          initialProps: {
            sourceRepos: ["repo-a", "repo-b"] as string[] | undefined,
          },
        },
      );

      expect(MockEventSource.instances.length).toBe(1);

      // Rerender with same repos in different order
      rerender({ sourceRepos: ["repo-b", "repo-a"] });

      // Should NOT have created a new connection
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("reconnect uses sinceRef.current for catch-up", () => {
      const { rerender } = renderHook(
        ({
          sourceRepos,
          since,
        }: {
          sourceRepos: string[] | undefined;
          since: number | undefined;
        }) =>
          useSSE({
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

      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );

      // Change sourceRepos to trigger reconnect
      rerender({ sourceRepos: ["repo-b"], since: 1706011200000 });

      // New connection should use the since value for catch-up
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
      expect(MockEventSource.lastInstance?.url).toContain(
        "source_repos=repo-b",
      );
    });

    it("switching from sourceRepos to undefined triggers reconnect", () => {
      const { rerender } = renderHook(
        ({ sourceRepos }: { sourceRepos: string[] | undefined }) =>
          useSSE({
            autoConnect: true,
            sourceRepos,
          }),
        { initialProps: { sourceRepos: ["repo-a"] as string[] | undefined } },
      );

      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain("source_repos");

      // Change to undefined
      rerender({ sourceRepos: undefined });

      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).not.toContain("source_repos");
    });
  });
});
