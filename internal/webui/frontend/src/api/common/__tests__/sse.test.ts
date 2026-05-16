/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError } from "../client";
import { WorkspaceSSEClient, getSSEUrl, fetchSseToken } from "../sse";
import type { MutationPayload, SseTokenResult } from "../sse";

// Mock the get function from client.ts — default to 404 (open mode, no SSE token endpoint)
const mockGet = vi.fn();
vi.mock("../client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../client")>();
  return {
    ...mod,
    get: (...args: unknown[]) => mockGet(...args),
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

  simulateError(): void {
    this.onerror?.();
  }

  simulateMutation(data: MutationPayload, eventId?: string): void {
    const listeners = this.eventListeners.get("mutation") ?? [];
    // Compute eventId from timestamp if not provided (simulates server behavior)
    const lastEventId = eventId ?? String(Date.parse(data.timestamp));
    const event = {
      data: JSON.stringify(data),
      lastEventId,
    } as MessageEvent;
    for (const listener of listeners) {
      listener(event);
    }
  }

  simulateRawMutation(data: string, eventId = ""): void {
    const listeners = this.eventListeners.get("mutation") ?? [];
    const event = { data, lastEventId: eventId } as MessageEvent;
    for (const listener of listeners) {
      listener(event);
    }
  }

  simulateConnectedEvent(): void {
    const listeners = this.eventListeners.get("connected") ?? [];
    const event = { data: "" } as MessageEvent;
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

describe("WorkspaceSSEClient", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    vi.useFakeTimers();
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
    // Default: 404 (open mode — no SSE token endpoint)
    mockGet.mockRejectedValue(new ApiError(404, "Not Found"));
  });

  afterEach(() => {
    vi.useRealTimers();
    global.EventSource = originalEventSource;
    vi.restoreAllMocks();
  });

  describe("Initialization", () => {
    it("creates a client with initial disconnected state", () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      expect(client.getState()).toBe("disconnected");
      expect(client.getReconnectAttempts()).toBe(0);
    });

    it("accepts callbacks in options", () => {
      const onMutation = vi.fn();
      const onError = vi.fn();
      const onStateChange = vi.fn();
      const onReconnect = vi.fn();

      const client = new WorkspaceSSEClient("test-ws-id", {
        onMutation,
        onError,
        onStateChange,
        onReconnect,
      });

      expect(client.getState()).toBe("disconnected");
    });
  });

  describe("Connection lifecycle", () => {
    it("connect() creates EventSource", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();

      expect(MockEventSource.lastInstance).toBeDefined();
      expect(MockEventSource.lastInstance?.url).toContain(
        "/api/workspaces/test-ws-id/events",
      );
    });

    it("connect() with since parameter adds query string", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect(1706011200000);

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
    });

    it("state transitions from disconnected to connecting to connected", async () => {
      const onStateChange = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onStateChange });

      expect(client.getState()).toBe("disconnected");

      await client.connect();

      expect(client.getState()).toBe("connecting");
      expect(onStateChange).toHaveBeenCalledWith("connecting");

      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe("connected");
      expect(onStateChange).toHaveBeenCalledWith("connected");
    });

    it("disconnect() closes EventSource and updates state", async () => {
      const onStateChange = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onStateChange });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe("connected");

      const esInstance = MockEventSource.lastInstance;

      client.disconnect();

      expect(client.getState()).toBe("disconnected");
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(onStateChange).toHaveBeenCalledWith("disconnected");
    });

    it("connect() when already connected does nothing", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(MockEventSource.instances.length).toBe(1);

      await client.connect();

      expect(MockEventSource.instances.length).toBe(1);
    });

    it("handles EventSource constructor throwing with reconnect", async () => {
      const onReconnect = vi.fn();
      const consoleErrorSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});

      // Override EventSource to throw on construction
      const ThrowingEventSource = function () {
        throw new Error("SecurityError");
      } as unknown as typeof EventSource;
      ThrowingEventSource.CONNECTING = 0;
      ThrowingEventSource.OPEN = 1;
      ThrowingEventSource.CLOSED = 2;

      global.EventSource = ThrowingEventSource;

      const client = new WorkspaceSSEClient("test-ws-id", {
        onReconnect,
        initialReconnectDelay: 100,
      });
      await client.connect();

      // Should enter reconnecting state (EventSource constructor failure is transient)
      expect(client.getState()).toBe("reconnecting");
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onReconnect).toHaveBeenCalledWith(1);
      expect(consoleErrorSpy).toHaveBeenCalledWith(
        "[SSE] Failed to create EventSource:",
        expect.any(Error),
      );

      consoleErrorSpy.mockRestore();
      // Restore MockEventSource for subsequent tests
      global.EventSource = MockEventSource as unknown as typeof EventSource;
    });

    it("disconnect works after EventSource constructor failure", async () => {
      const consoleErrorSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});

      global.EventSource = class ThrowingEventSource {
        static readonly CONNECTING = 0;
        static readonly OPEN = 1;
        static readonly CLOSED = 2;
        constructor() {
          throw new Error("EventSource not supported");
        }
      } as unknown as typeof EventSource;

      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();

      // In reconnecting state after constructor failure
      expect(client.getState()).toBe("reconnecting");

      // disconnect() should work without error
      client.disconnect();
      expect(client.getState()).toBe("disconnected");

      consoleErrorSpy.mockRestore();
    });

    it("connect() when connecting does nothing", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();

      expect(client.getState()).toBe("connecting");
      expect(MockEventSource.instances.length).toBe(1);

      await client.connect();

      expect(MockEventSource.instances.length).toBe(1);
    });
  });

  describe("Message parsing and callback invocation", () => {
    it("onMutation called with parsed payload", async () => {
      const onMutation = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      expect(onMutation).toHaveBeenCalledWith(mutation);
    });

    it("onMutation receives generic non-issue agent payloads", async () => {
      const onMutation = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "status",
        entity_type: "agent",
        entity_id: "agent-alpha",
        action: "agent.status",
        title: "agent-alpha",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "agent-cursor-1");

      expect(onMutation).toHaveBeenCalledWith(mutation);
      expect(client.getLastEventId()).toBe("agent-cursor-1");
    });

    it("malformed JSON is ignored with warning", async () => {
      const onMutation = vi.fn();
      const consoleWarnSpy = vi
        .spyOn(console, "warn")
        .mockImplementation(() => {});
      const client = new WorkspaceSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      MockEventSource.lastInstance?.simulateRawMutation("not valid json");

      expect(onMutation).not.toHaveBeenCalled();
      expect(consoleWarnSpy).toHaveBeenCalledWith(
        "[SSE] Received malformed mutation event",
      );

      consoleWarnSpy.mockRestore();
    });

    it("onConnected callback fires on connected SSE event", async () => {
      const onConnected = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onConnected });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();
      MockEventSource.lastInstance?.simulateConnectedEvent();

      expect(onConnected).toHaveBeenCalledTimes(1);
    });
  });

  describe("Error handling and reconnect state tracking", () => {
    it("error triggers reconnecting with unified manual reconnect", async () => {
      const onStateChange = vi.fn();
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onStateChange,
        onReconnect,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const esInstance = MockEventSource.lastInstance;

      // Simulate error — EventSource should be closed (manual reconnect)
      esInstance?.simulateError();

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(client.getState()).toBe("reconnecting");
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onStateChange).toHaveBeenCalledWith("reconnecting");
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("reconnectAttempts increments on consecutive errors without successful open", async () => {
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onReconnect,
        initialReconnectDelay: 100,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Consecutive errors without successful opens — attempts accumulate
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(1);

      // Advance timer to trigger reconnect, then error again immediately
      await vi.advanceTimersByTimeAsync(100);
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(2);

      // Advance timer (200ms = 100 * 2^1)
      await vi.advanceTimersByTimeAsync(200);
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(3);
    });

    it("reconnectAttempts resets to 0 on successful open", async () => {
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onReconnect,
        initialReconnectDelay: 100,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate error
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(1);

      // Advance timer to trigger reconnect
      await vi.advanceTimersByTimeAsync(100);

      // Simulate successful reconnection
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getReconnectAttempts()).toBe(0);
      expect(onReconnect).toHaveBeenCalledWith(0);
    });

    it("error after manual disconnect is ignored", async () => {
      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onError,
        onReconnect,
      });

      await client.connect();
      const esInstance = MockEventSource.lastInstance;
      MockEventSource.lastInstance?.simulateOpen();

      // Clear mocks after open
      onError.mockClear();
      onReconnect.mockClear();

      client.disconnect();

      // Simulate error after disconnect
      esInstance?.simulateError();

      expect(onError).not.toHaveBeenCalled();
      expect(onReconnect).not.toHaveBeenCalled();
      expect(client.getReconnectAttempts()).toBe(0);
    });

    it("errors are processed again after disconnect then reconnect", async () => {
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onReconnect });

      // First connection
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Disconnect (sets manualDisconnect = true)
      client.disconnect();

      // Reconnect (should reset manualDisconnect = false)
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      onReconnect.mockClear();

      // Error on the new connection should be processed, not suppressed
      MockEventSource.lastInstance?.simulateError();

      expect(client.getState()).toBe("reconnecting");
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("logs warning after 5 connection failures", async () => {
      const consoleWarnSpy = vi
        .spyOn(console, "warn")
        .mockImplementation(() => {});
      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 10,
        maxReconnectDelay: 1000,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Consecutive errors without successful opens accumulate attempts
      for (let i = 0; i < 5; i++) {
        MockEventSource.lastInstance?.simulateError();
        // Advance timer to trigger reconnect: delay = 10 * 2^i
        const delay = Math.min(10 * Math.pow(2, i), 1000);
        await vi.advanceTimersByTimeAsync(delay);
        // connect() creates a new EventSource, but we immediately error again
        // (no simulateOpen — the next iteration's simulateError hits the new instance)
      }

      expect(consoleWarnSpy).toHaveBeenCalledWith(
        "[SSE] Multiple connection failures, will continue retrying",
      );

      consoleWarnSpy.mockRestore();
    });
  });

  describe("Last event ID tracking for reconnection catch-up", () => {
    it("getLastEventId returns undefined initially", () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("getLastEventId returns the last event ID after receiving a mutation", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      const expectedTime = Date.parse("2025-01-23T12:00:00Z");
      expect(client.getLastEventId()).toBe(String(expectedTime));
    });

    it("tracks last event ID from event.lastEventId", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      // Disconnect and reconnect - should use last event ID
      client.disconnect();
      await client.connect();

      const expectedTime = Date.parse("2025-01-23T12:00:00Z");
      expect(MockEventSource.lastInstance?.url).toContain(
        `since=${expectedTime}`,
      );
    });

    it("uses latest event ID when receiving multiple mutations", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation1: MutationPayload = {
        type: "create",
        issue_id: "issue-123",
        title: "First Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      const mutation2: MutationPayload = {
        type: "update",
        issue_id: "issue-456",
        title: "Second Issue",
        timestamp: "2025-01-23T14:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation1);
      MockEventSource.lastInstance?.simulateMutation(mutation2);

      client.disconnect();
      await client.connect();

      const expectedTime = Date.parse("2025-01-23T14:00:00Z");
      expect(MockEventSource.lastInstance?.url).toContain(
        `since=${expectedTime}`,
      );
    });

    it("connect with explicit since overrides last event ID", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      client.disconnect();
      await client.connect(1706100000000);

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706100000000",
      );
    });

    it("stores lastEventId of 0", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-100",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "0");

      expect(client.getLastEventId()).toBe("0");
    });

    it("stores negative lastEventId", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-101",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "-1");

      expect(client.getLastEventId()).toBe("-1");
    });

    it("stores opaque lastEventId", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-102",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "abc");

      expect(client.getLastEventId()).toBe("abc");
    });

    it("stores the last delivered event ID", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation1: MutationPayload = {
        type: "create",
        issue_id: "issue-103",
        title: "First",
        timestamp: "2025-01-23T12:00:00Z",
      };

      const mutation2: MutationPayload = {
        type: "update",
        issue_id: "issue-104",
        title: "Second",
        timestamp: "2025-01-23T11:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation1, "2000");
      MockEventSource.lastInstance?.simulateMutation(mutation2, "1000");

      expect(client.getLastEventId()).toBe("1000");
    });

    it("handles empty lastEventId string", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "issue-105",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("invalid timestamp in mutation is ignored for tracking", async () => {
      const onMutation = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation = {
        type: "create",
        issue_id: "issue-123",
        title: "Test Issue",
        timestamp: "invalid-date",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "");

      // Callback should still be called
      expect(onMutation).toHaveBeenCalled();

      client.disconnect();
      await client.connect();

      // Should not have a since parameter since timestamp was invalid
      expect(MockEventSource.lastInstance?.url).not.toContain("since=");
    });
  });

  describe("retryNow", () => {
    it("only works when in reconnecting state", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe("connected");

      client.retryNow();

      // Should not create a new EventSource
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("creates new connection immediately when in reconnecting state", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger reconnecting state
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");
      expect(MockEventSource.instances.length).toBe(1);

      client.retryNow();
      // retryNow calls connect() which is async — wait for microtasks
      await vi.waitFor(() => {
        expect(MockEventSource.instances.length).toBe(2);
      });
      expect(client.getState()).toBe("connecting");
    });

    it("resets reconnect counter on manual retry", async () => {
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onReconnect,
        initialReconnectDelay: 100,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger error
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(1);

      client.retryNow();

      expect(client.getReconnectAttempts()).toBe(0);
      expect(onReconnect).toHaveBeenCalledWith(0);
    });

    it("clears pending retry timer", async () => {
      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 5000,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger error — starts a 5s retry timer
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");

      // retryNow should clear the timer and connect immediately
      client.retryNow();
      await vi.waitFor(() => {
        expect(MockEventSource.instances.length).toBe(2);
      });

      // Advance past original timer — should NOT create a third connection
      await vi.advanceTimersByTimeAsync(5000);
      expect(MockEventSource.instances.length).toBe(2);
    });
  });

  describe("Cleanup on destroy", () => {
    it("destroy() closes EventSource and clears callbacks", async () => {
      const onMutation = vi.fn();
      const onStateChange = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onMutation,
        onStateChange,
      });

      await client.connect();
      const esInstance = MockEventSource.lastInstance;
      MockEventSource.lastInstance?.simulateOpen();

      client.destroy();

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);

      // Callbacks should not be called after destroy
      onStateChange.mockClear();
      onMutation.mockClear();

      // Try to trigger callbacks - they should not be called
      esInstance?.simulateOpen();
      esInstance?.simulateMutation({
        type: "create",
        issue_id: "issue-789",
        title: "Should not trigger",
        timestamp: "2025-01-23T12:00:00Z",
      });

      expect(onStateChange).not.toHaveBeenCalled();
      expect(onMutation).not.toHaveBeenCalled();
    });

    it("connect() is a no-op after destroy", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      client.destroy();

      // Try to connect again
      await client.connect();

      // Should not have created a new EventSource
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("retryNow() is a no-op after destroy", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Put in reconnecting state
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");

      client.destroy();

      client.retryNow();
      // Should not create a new EventSource
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("disconnect() is a no-op after destroy", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      client.destroy();

      // Should not throw
      client.disconnect();
    });

    it("getState() returns last known state after destroy", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();
      expect(client.getState()).toBe("connected");

      client.destroy();

      // State is preserved (callbacks cleared, not state)
      expect(client.getState()).toBe("connected");
    });

    it("destroy() clears pending retry timer", async () => {
      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 5000,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger error — starts a retry timer
      MockEventSource.lastInstance?.simulateError();

      client.destroy();

      // Advance past timer — should NOT create a new EventSource
      await vi.advanceTimersByTimeAsync(5000);
      expect(MockEventSource.instances.length).toBe(1);
    });
  });

  describe("Injectable fetchToken", () => {
    it("uses custom fetchToken instead of default", async () => {
      const customFetchToken = vi
        .fn()
        .mockResolvedValue({ kind: "token", token: "custom-token-abc" });

      // Clear mock call history so we can verify no default token fetch occurs
      mockGet.mockClear();

      const client = new WorkspaceSSEClient("test-ws-id", {
        fetchToken: customFetchToken,
      });
      await client.connect();

      expect(customFetchToken).toHaveBeenCalledTimes(1);
      // Default fetchSseToken should NOT have been called
      expect(mockGet).not.toHaveBeenCalled();
      expect(MockEventSource.lastInstance?.url).toContain(
        "token=custom-token-abc",
      );
    });

    it("custom fetchToken returning disabled works like open mode", async () => {
      const customFetchToken = vi
        .fn()
        .mockResolvedValue({ kind: "disabled" } as SseTokenResult);

      const client = new WorkspaceSSEClient("test-ws-id", {
        fetchToken: customFetchToken,
      });
      await client.connect();

      expect(MockEventSource.lastInstance?.url).not.toContain("token=");
    });

    it("custom fetchToken that throws is handled gracefully", async () => {
      const onError = vi.fn();
      const customFetchToken = vi
        .fn()
        .mockRejectedValue(new Error("Token service down"));

      const client = new WorkspaceSSEClient("test-ws-id", {
        fetchToken: customFetchToken,
        onError,
      });
      await client.connect();

      expect(onError).toHaveBeenCalledWith(
        "SSE auth failed: Token service down",
      );
      expect(client.getState()).toBe("disconnected");
      expect(MockEventSource.lastInstance).toBeUndefined();
    });
  });

  describe("AbortController cancellation", () => {
    it("disconnect during token fetch aborts cleanly", async () => {
      // Make fetchToken take a while by using a deferred promise
      let resolveToken: (value: SseTokenResult) => void;
      const customFetchToken = vi.fn().mockReturnValue(
        new Promise<SseTokenResult>((resolve) => {
          resolveToken = resolve;
        }),
      );

      const client = new WorkspaceSSEClient("test-ws-id", {
        fetchToken: customFetchToken,
      });
      const connectPromise = client.connect();

      // Disconnect while fetching token
      client.disconnect();

      // Resolve the token call
      resolveToken!({ kind: "token", token: "opaque-123" });
      await connectPromise;

      // Should not have created an EventSource
      expect(MockEventSource.lastInstance).toBeUndefined();
      expect(client.getState()).toBe("disconnected");
    });

    it("destroy during token fetch aborts cleanly", async () => {
      let resolveToken: (value: SseTokenResult) => void;
      const customFetchToken = vi.fn().mockReturnValue(
        new Promise<SseTokenResult>((resolve) => {
          resolveToken = resolve;
        }),
      );

      const client = new WorkspaceSSEClient("test-ws-id", {
        fetchToken: customFetchToken,
      });
      const connectPromise = client.connect();

      // Destroy while fetching token
      client.destroy();

      // Resolve the token call
      resolveToken!({ kind: "token", token: "opaque-123" });
      await connectPromise;

      // Should not have created an EventSource
      expect(MockEventSource.lastInstance).toBeUndefined();
    });
  });

  describe("Unified reconnect (manual for all modes)", () => {
    it("open mode uses manual reconnect (EventSource closed on error)", async () => {
      // Open mode — no opaque token
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const esInstance = MockEventSource.lastInstance;
      esInstance?.simulateError();

      // EventSource should be closed — no browser auto-reconnect
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(client.getState()).toBe("reconnecting");
    });

    it("token mode uses same manual reconnect path", async () => {
      mockGet.mockResolvedValue({ token: "opaque-token-123" });

      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onReconnect });
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const esInstance = MockEventSource.lastInstance;
      esInstance?.simulateError();

      // EventSource should be closed
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(client.getState()).toBe("reconnecting");
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("fresh token fetched on each reconnect", async () => {
      let callCount = 0;
      mockGet.mockImplementation(() => {
        callCount++;
        return Promise.resolve({ token: `opaque-token-${callCount}` });
      });

      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 100,
      });
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-1",
      );

      // Trigger error → reconnecting
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");

      // Advance timer to trigger reconnect
      await vi.advanceTimersByTimeAsync(100);

      // New connection should use fresh token
      expect(MockEventSource.instances.length).toBe(2);
      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-2",
      );
    });
  });

  describe("Configurable backoff", () => {
    it("uses default delays (1000ms initial, 30000ms max)", async () => {
      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // First error: 1000ms delay
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");

      // Before 1000ms: still reconnecting
      await vi.advanceTimersByTimeAsync(999);
      expect(MockEventSource.instances.length).toBe(1);

      // At 1000ms: reconnect fires
      await vi.advanceTimersByTimeAsync(1);
      expect(MockEventSource.instances.length).toBe(2);
    });

    it("respects custom initialReconnectDelay and maxReconnectDelay", async () => {
      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 500,
        maxReconnectDelay: 2000,
      });
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Consecutive errors without opens — backoff escalates:
      // attempt 1: 500 * 2^0 = 500ms
      // attempt 2: 500 * 2^1 = 1000ms
      // attempt 3: 500 * 2^2 = 2000ms (capped)
      // attempt 4: 500 * 2^3 = 4000ms → capped at 2000ms

      // 1st error → attempt 1 → 500ms delay
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(1);
      await vi.advanceTimersByTimeAsync(499);
      expect(MockEventSource.instances.length).toBe(1);
      await vi.advanceTimersByTimeAsync(1);
      expect(MockEventSource.instances.length).toBe(2);

      // 2nd error → attempt 2 → 1000ms delay
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(2);
      await vi.advanceTimersByTimeAsync(999);
      expect(MockEventSource.instances.length).toBe(2);
      await vi.advanceTimersByTimeAsync(1);
      expect(MockEventSource.instances.length).toBe(3);

      // 3rd error → attempt 3 → 2000ms delay (capped)
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(3);
      await vi.advanceTimersByTimeAsync(1999);
      expect(MockEventSource.instances.length).toBe(3);
      await vi.advanceTimersByTimeAsync(1);
      expect(MockEventSource.instances.length).toBe(4);

      // 4th error → attempt 4 → 2000ms delay (still capped)
      MockEventSource.lastInstance?.simulateError();
      expect(client.getReconnectAttempts()).toBe(4);
      await vi.advanceTimersByTimeAsync(1999);
      expect(MockEventSource.instances.length).toBe(4);
      await vi.advanceTimersByTimeAsync(1);
      expect(MockEventSource.instances.length).toBe(5);
    });
  });

  describe("SSE token exchange", () => {
    it("connect() with opaque token adds token to URL", async () => {
      mockGet.mockResolvedValue({ token: "opaque-token-123" });

      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/events/token",
      );
      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-123",
      );
    });

    it("connect() in open mode (404) creates EventSource without token", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/events/token",
      );
      expect(MockEventSource.lastInstance?.url).not.toContain("token=");
    });

    it("connect() in open mode disabled response creates EventSource without token", async () => {
      mockGet.mockResolvedValue({ disabled: true });

      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/events/token",
      );
      expect(MockEventSource.lastInstance?.url).not.toContain("token=");
    });

    it("connect() with token error emits onError and sets disconnected", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      const onError = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", { onError });
      await client.connect();

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/events/token",
      );
      expect(onError).toHaveBeenCalledWith(
        expect.stringContaining("SSE auth failed"),
      );
      expect(client.getState()).toBe("disconnected");
      expect(MockEventSource.lastInstance).toBeUndefined();
    });

    it("connect() bails out if disconnected during token fetch", async () => {
      // Make fetchSseToken take a while by using a deferred promise
      let resolveGet: (value: unknown) => void;
      mockGet.mockReturnValue(
        new Promise((resolve) => {
          resolveGet = resolve;
        }),
      );

      const client = new WorkspaceSSEClient("test-ws-id");
      const connectPromise = client.connect();

      // Disconnect while fetching token
      client.disconnect();

      // Resolve the get call
      resolveGet!({ token: "opaque-123" });
      await connectPromise;

      // Should not have created an EventSource
      expect(MockEventSource.lastInstance).toBeUndefined();
      expect(client.getState()).toBe("disconnected");
    });

    it("retryNow() fetches fresh opaque token", async () => {
      let callCount = 0;
      mockGet.mockImplementation(() => {
        callCount++;
        return Promise.resolve({ token: `opaque-token-${callCount}` });
      });

      const client = new WorkspaceSSEClient("test-ws-id");
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-1",
      );

      // Trigger reconnecting state
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");

      // retryNow triggers a new connect with fresh token
      client.retryNow();
      await vi.waitFor(() => {
        expect(MockEventSource.instances.length).toBe(2);
      });
      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-2",
      );
    });

    it("token fetch failure (kind: error) does NOT enter reconnect loop", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new WorkspaceSSEClient("test-ws-id", {
        onError,
        onReconnect,
      });
      await client.connect();

      expect(client.getState()).toBe("disconnected");
      expect(onReconnect).not.toHaveBeenCalled();
    });
  });

  describe("Retry timer cleanup", () => {
    it("disconnect clears pending retry timer", async () => {
      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 5000,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger error → starts retry timer
      MockEventSource.lastInstance?.simulateError();
      expect(client.getState()).toBe("reconnecting");

      // Disconnect before timer fires
      client.disconnect();

      // Advance past timer — no new EventSource should be created
      await vi.advanceTimersByTimeAsync(5000);
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("retryNow clears timer then connects immediately", async () => {
      const client = new WorkspaceSSEClient("test-ws-id", {
        initialReconnectDelay: 5000,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger error → starts retry timer
      MockEventSource.lastInstance?.simulateError();

      // retryNow should clear timer and connect immediately
      client.retryNow();
      await vi.waitFor(() => {
        expect(MockEventSource.instances.length).toBe(2);
      });

      expect(client.getReconnectAttempts()).toBe(0);

      // Advance past original timer — should NOT create another connection
      await vi.advanceTimersByTimeAsync(5000);
      expect(MockEventSource.instances.length).toBe(2);
    });
  });
});

describe("fetchSseToken", () => {
  it("is exported and callable", () => {
    expect(typeof fetchSseToken).toBe("function");
  });
});

describe("getSSEUrl", () => {
  it("returns base URL without since parameter", () => {
    const url = getSSEUrl("test-ws-id");

    expect(url).toBe(
      `${window.location.origin}/api/workspaces/test-ws-id/events`,
    );
  });

  it("includes since parameter when provided", () => {
    const url = getSSEUrl("test-ws-id", 1706011200000);

    expect(url).toBe(
      `${window.location.origin}/api/workspaces/test-ws-id/events?since=1706011200000`,
    );
  });

  it("handles since value of 0", () => {
    const url = getSSEUrl("test-ws-id", 0);

    expect(url).toBe(
      `${window.location.origin}/api/workspaces/test-ws-id/events?since=0`,
    );
  });

  it("appends source_repos param when sourceRepos is provided", () => {
    const url = getSSEUrl("test-ws-id", undefined, ["repo-a", "repo-b"]);

    expect(url).toContain("source_repos=repo-a%2Crepo-b");
  });

  it("omits source_repos param when sourceRepos is empty", () => {
    const url = getSSEUrl("test-ws-id", undefined, []);

    expect(url).not.toContain("source_repos");
  });

  it("omits source_repos param when sourceRepos is undefined", () => {
    const url = getSSEUrl("test-ws-id", undefined, undefined);

    expect(url).not.toContain("source_repos");
  });

  it("includes both since and source_repos when both are provided", () => {
    const url = getSSEUrl("test-ws-id", 1706011200000, ["repo-a"]);

    expect(url).toContain("since=1706011200000");
    expect(url).toContain("source_repos=repo-a");
  });

  it("includes opaqueToken in URL when provided", () => {
    const url = getSSEUrl("test-ws-id", undefined, undefined, "opaque-abc");

    expect(url).toContain("token=opaque-abc");
  });

  it("omits token when opaqueToken is undefined", () => {
    const url = getSSEUrl("test-ws-id", undefined, undefined, undefined);

    expect(url).not.toContain("token=");
  });
});

describe("WorkspaceSSEClient sourceRepos support", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    vi.useFakeTimers();
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
    mockGet.mockRejectedValue(new ApiError(404, "Not Found"));
  });

  afterEach(() => {
    vi.useRealTimers();
    global.EventSource = originalEventSource;
    vi.restoreAllMocks();
  });

  it("connect() passes sourceRepos to URL", async () => {
    const client = new WorkspaceSSEClient("test-ws-id");

    await client.connect(undefined, ["repo-a", "repo-b"]);

    expect(MockEventSource.lastInstance?.url).toContain(
      "source_repos=repo-a%2Crepo-b",
    );
  });

  it("connect() without sourceRepos omits source_repos from URL", async () => {
    const client = new WorkspaceSSEClient("test-ws-id");

    await client.connect();

    expect(MockEventSource.lastInstance?.url).not.toContain("source_repos");
  });

  it("connect() with both since and sourceRepos includes both in URL", async () => {
    const client = new WorkspaceSSEClient("test-ws-id");

    await client.connect(1706011200000, ["repo-x"]);

    expect(MockEventSource.lastInstance?.url).toContain("since=1706011200000");
    expect(MockEventSource.lastInstance?.url).toContain("source_repos=repo-x");
  });

  it("retryNow() uses stored sourceRepos from last connect()", async () => {
    const client = new WorkspaceSSEClient("test-ws-id");

    await client.connect(undefined, ["repo-a", "repo-b"]);
    MockEventSource.lastInstance?.simulateOpen();

    // Trigger reconnecting state
    MockEventSource.lastInstance?.simulateError();
    expect(client.getState()).toBe("reconnecting");

    client.retryNow();
    await vi.waitFor(() => {
      expect(MockEventSource.instances.length).toBe(2);
    });

    // New connection should use stored sourceRepos
    expect(MockEventSource.lastInstance?.url).toContain(
      "source_repos=repo-a%2Crepo-b",
    );
  });

  it("retryNow() without sourceRepos omits source_repos from URL", async () => {
    const client = new WorkspaceSSEClient("test-ws-id");

    await client.connect();
    MockEventSource.lastInstance?.simulateOpen();

    // Trigger reconnecting state
    MockEventSource.lastInstance?.simulateError();
    expect(client.getState()).toBe("reconnecting");

    client.retryNow();
    await vi.waitFor(() => {
      expect(MockEventSource.instances.length).toBe(2);
    });

    expect(MockEventSource.lastInstance?.url).not.toContain("source_repos");
  });
});
