/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ApiError } from "./client";
import { BeadsSSEClient, getSSEUrl } from "./sse";
import type { MutationPayload } from "./sse";

// Mock the get function from client.ts — default to 404 (open mode, no SSE token endpoint)
const mockGet = vi.fn();
vi.mock("./client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("./client")>();
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

  simulateError(readyState: number = MockEventSource.CONNECTING): void {
    this.readyState = readyState;
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

describe("BeadsSSEClient", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
    // Default: 404 (open mode — no SSE token endpoint)
    mockGet.mockRejectedValue(new ApiError(404, "Not Found"));
  });

  afterEach(() => {
    global.EventSource = originalEventSource;
    vi.restoreAllMocks();
  });

  describe("Initialization", () => {
    it("creates a client with initial disconnected state", () => {
      const client = new BeadsSSEClient("test-ws-id");

      expect(client.getState()).toBe("disconnected");
      expect(client.getReconnectAttempts()).toBe(0);
    });

    it("accepts callbacks in options", () => {
      const onMutation = vi.fn();
      const onError = vi.fn();
      const onStateChange = vi.fn();
      const onReconnect = vi.fn();

      const client = new BeadsSSEClient("test-ws-id", {
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
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();

      expect(MockEventSource.lastInstance).toBeDefined();
      expect(MockEventSource.lastInstance?.url).toContain(
        "/api/workspaces/test-ws-id/events",
      );
    });

    it("connect() with since parameter adds query string", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect(1706011200000);

      expect(MockEventSource.lastInstance?.url).toContain(
        "since=1706011200000",
      );
    });

    it("state transitions from disconnected to connecting to connected", async () => {
      const onStateChange = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onStateChange });

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
      const client = new BeadsSSEClient("test-ws-id", { onStateChange });

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
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(MockEventSource.instances.length).toBe(1);

      await client.connect();

      expect(MockEventSource.instances.length).toBe(1);
    });

    it("handles EventSource constructor throwing", async () => {
      const onStateChange = vi.fn();
      const onError = vi.fn();
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

      const client = new BeadsSSEClient("test-ws-id", {
        onStateChange,
        onError,
      });
      await client.connect();

      // handleError is called but eventSource is null, so state stays 'connecting'
      expect(client.getState()).toBe("connecting");
      expect(consoleErrorSpy).toHaveBeenCalledWith(
        "[SSE] Failed to create EventSource:",
        expect.any(Error),
      );
      // onError is NOT called because handleError requires non-null eventSource
      expect(onError).not.toHaveBeenCalled();

      consoleErrorSpy.mockRestore();
      // Restore MockEventSource for subsequent tests
      global.EventSource = MockEventSource as unknown as typeof EventSource;
    });

    it("eventSource remains null after constructor failure and disconnect works", async () => {
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

      const client = new BeadsSSEClient("test-ws-id");
      await client.connect();

      // Verify no active connection
      expect(client.getState()).toBe("connecting");

      // disconnect() should work without error even though eventSource is null
      client.disconnect();
      expect(client.getState()).toBe("disconnected");

      consoleErrorSpy.mockRestore();
    });

    it("connect() when connecting does nothing", async () => {
      const client = new BeadsSSEClient("test-ws-id");

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
      const client = new BeadsSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      expect(onMutation).toHaveBeenCalledWith(mutation);
    });

    it("malformed JSON is ignored with warning", async () => {
      const onMutation = vi.fn();
      const consoleWarnSpy = vi
        .spyOn(console, "warn")
        .mockImplementation(() => {});
      const client = new BeadsSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate invalid JSON by directly calling the listener with malformed data
      const listeners =
        (MockEventSource.lastInstance as MockEventSource)["eventListeners"].get(
          "mutation",
        ) ?? [];
      const event = { data: "not valid json" } as MessageEvent;
      for (const listener of listeners) {
        listener(event);
      }

      expect(onMutation).not.toHaveBeenCalled();
      expect(consoleWarnSpy).toHaveBeenCalledWith(
        "[SSE] Received malformed mutation event",
      );

      consoleWarnSpy.mockRestore();
    });

    it("connected event is handled", async () => {
      const originalEnv = process.env.NODE_ENV;
      process.env.NODE_ENV = "development";
      const consoleDebugSpy = vi
        .spyOn(console, "debug")
        .mockImplementation(() => {});

      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();
      MockEventSource.lastInstance?.simulateConnectedEvent();

      expect(consoleDebugSpy).toHaveBeenCalledWith(
        "[SSE] Received connected event",
      );

      consoleDebugSpy.mockRestore();
      process.env.NODE_ENV = originalEnv;
    });
  });

  describe("Error handling and reconnect state tracking", () => {
    it("error during connecting state triggers reconnecting", async () => {
      const onStateChange = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", {
        onStateChange,
        onReconnect,
      });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate error while browser is reconnecting
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);

      expect(client.getState()).toBe("reconnecting");
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onStateChange).toHaveBeenCalledWith("reconnecting");
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("error during closed state triggers reconnecting and calls onError", async () => {
      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onError, onReconnect });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate closed state error
      MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);

      expect(client.getState()).toBe("reconnecting");
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onError).toHaveBeenCalledWith("Connection closed");
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("reconnectAttempts increments on consecutive errors", async () => {
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onReconnect });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(1);

      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(2);

      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(3);
    });

    it("reconnectAttempts resets to 0 on successful open", async () => {
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onReconnect });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate some errors
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(2);

      // Simulate successful reconnection
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getReconnectAttempts()).toBe(0);
      expect(onReconnect).toHaveBeenCalledWith(0);
    });

    it("error after manual disconnect is ignored", async () => {
      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onError, onReconnect });

      await client.connect();
      const esInstance = MockEventSource.lastInstance;
      MockEventSource.lastInstance?.simulateOpen();

      // Clear mocks after open (open triggers onReconnect(0))
      onError.mockClear();
      onReconnect.mockClear();

      client.disconnect();

      // Simulate error after disconnect
      esInstance?.simulateError(MockEventSource.CLOSED);

      expect(onError).not.toHaveBeenCalled();
      expect(onReconnect).not.toHaveBeenCalled();
      expect(client.getReconnectAttempts()).toBe(0);
    });

    it("errors are processed again after disconnect then reconnect", async () => {
      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onError, onReconnect });

      // First connection
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Disconnect (sets manualDisconnect = true)
      client.disconnect();

      // Reconnect (should reset manualDisconnect = false)
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      onError.mockClear();
      onReconnect.mockClear();

      // Error on the new connection should be processed, not suppressed
      MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);

      expect(client.getState()).toBe("reconnecting");
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onError).toHaveBeenCalledWith("Connection closed");
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("logs warning after 5 connection failures", async () => {
      const consoleWarnSpy = vi
        .spyOn(console, "warn")
        .mockImplementation(() => {});
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      for (let i = 0; i < 5; i++) {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      }

      expect(consoleWarnSpy).toHaveBeenCalledWith(
        "[SSE] Multiple connection failures, will continue retrying",
      );

      consoleWarnSpy.mockRestore();
    });
  });

  describe("Last event ID tracking for reconnection catch-up", () => {
    it("getLastEventId returns undefined initially", () => {
      const client = new BeadsSSEClient("test-ws-id");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("getLastEventId returns the last event ID after receiving a mutation", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
        title: "Test Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      const expectedTime = Date.parse("2025-01-23T12:00:00Z");
      expect(client.getLastEventId()).toBe(expectedTime);
    });

    it("tracks last event ID from event.lastEventId", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
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
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation1: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
        title: "First Issue",
        timestamp: "2025-01-23T12:00:00Z",
      };

      const mutation2: MutationPayload = {
        type: "update",
        issue_id: "beads-456",
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
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-123",
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

    it("ignores lastEventId of 0", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-100",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "0");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("ignores negative lastEventId", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-101",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "-1");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("ignores non-numeric lastEventId", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-102",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "abc");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("does not overwrite newer ID with older ID (out-of-order)", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation1: MutationPayload = {
        type: "create",
        issue_id: "beads-103",
        title: "First",
        timestamp: "2025-01-23T12:00:00Z",
      };

      const mutation2: MutationPayload = {
        type: "update",
        issue_id: "beads-104",
        title: "Second",
        timestamp: "2025-01-23T11:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation1, "2000");
      MockEventSource.lastInstance?.simulateMutation(mutation2, "1000");

      expect(client.getLastEventId()).toBe(2000);
    });

    it("handles empty lastEventId string", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: "create",
        issue_id: "beads-105",
        title: "Test",
        timestamp: "2025-01-23T12:00:00Z",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation, "");

      expect(client.getLastEventId()).toBeUndefined();
    });

    it("invalid timestamp in mutation is ignored for tracking", async () => {
      const onMutation = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onMutation });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation = {
        type: "create",
        issue_id: "beads-123",
        title: "Test Issue",
        timestamp: "invalid-date",
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

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
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe("connected");

      client.retryNow();

      // Should not create a new EventSource
      expect(MockEventSource.instances.length).toBe(1);
    });

    it("creates new connection immediately when in reconnecting state", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger reconnecting state
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
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
      const client = new BeadsSSEClient("test-ws-id", { onReconnect });

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger multiple errors
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(2);

      client.retryNow();

      expect(client.getReconnectAttempts()).toBe(0);
      expect(onReconnect).toHaveBeenCalledWith(0);
    });
  });

  describe("Cleanup on destroy", () => {
    it("destroy() closes EventSource and clears callbacks", async () => {
      const onMutation = vi.fn();
      const onStateChange = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", {
        onMutation,
        onStateChange,
      });

      await client.connect();
      const esInstance = MockEventSource.lastInstance;
      MockEventSource.lastInstance?.simulateOpen();

      client.destroy();

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(client.getState()).toBe("disconnected");

      // Callbacks should not be called after destroy
      onStateChange.mockClear();
      onMutation.mockClear();

      // Try to trigger callbacks - they should not be called
      esInstance?.simulateOpen();
      esInstance?.simulateMutation({
        type: "create",
        issue_id: "beads-789",
        title: "Should not trigger",
        timestamp: "2025-01-23T12:00:00Z",
      });

      expect(onStateChange).not.toHaveBeenCalled();
      expect(onMutation).not.toHaveBeenCalled();
    });

    it("instance should not be reused after destroy", async () => {
      const client = new BeadsSSEClient("test-ws-id");

      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();
      expect(client.getState()).toBe("connected");

      client.destroy();
      expect(client.getState()).toBe("disconnected");

      // Note: The client doesn't prevent reuse, but callbacks are cleared
      // This documents the behavior - destroy() clears callbacks
    });
  });

  describe("SSE token exchange", () => {
    it("connect() with opaque token adds token to URL", async () => {
      mockGet.mockResolvedValue({ token: "opaque-token-123" });

      const client = new BeadsSSEClient("test-ws-id");
      await client.connect();

      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-123",
      );
    });

    it("connect() in open mode (404) creates EventSource without token", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const client = new BeadsSSEClient("test-ws-id");
      await client.connect();

      expect(MockEventSource.lastInstance?.url).not.toContain("token=");
    });

    it("connect() with token error emits onError and sets disconnected", async () => {
      mockGet.mockRejectedValue(new ApiError(500, "Internal Server Error"));

      const onError = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onError });
      await client.connect();

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

      const client = new BeadsSSEClient("test-ws-id");
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

    it("handleError with opaque token closes EventSource", async () => {
      mockGet.mockResolvedValue({ token: "opaque-token-123" });

      const onReconnect = vi.fn();
      const client = new BeadsSSEClient("test-ws-id", { onReconnect });
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const esInstance = MockEventSource.lastInstance;
      esInstance?.simulateError(MockEventSource.CONNECTING);

      // EventSource should be closed (not left for browser to auto-reconnect)
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(client.getState()).toBe("reconnecting");
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it("handleError without opaque token preserves browser auto-reconnect", async () => {
      // Open mode — no opaque token
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      const client = new BeadsSSEClient("test-ws-id");
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const esInstance = MockEventSource.lastInstance;
      esInstance?.simulateError(MockEventSource.CONNECTING);

      // EventSource should NOT be closed — browser handles reconnect
      expect(esInstance?.readyState).toBe(MockEventSource.CONNECTING);
      expect(client.getState()).toBe("reconnecting");
    });

    it("retryNow() fetches fresh opaque token", async () => {
      let callCount = 0;
      mockGet.mockImplementation(() => {
        callCount++;
        return Promise.resolve({ token: `opaque-token-${callCount}` });
      });

      const client = new BeadsSSEClient("test-ws-id");
      await client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(MockEventSource.lastInstance?.url).toContain(
        "token=opaque-token-1",
      );

      // Trigger reconnecting state
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
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

describe("BeadsSSEClient sourceRepos support", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
    mockGet.mockRejectedValue(new ApiError(404, "Not Found"));
  });

  afterEach(() => {
    global.EventSource = originalEventSource;
    vi.restoreAllMocks();
  });

  it("connect() passes sourceRepos to URL", async () => {
    const client = new BeadsSSEClient("test-ws-id");

    await client.connect(undefined, ["repo-a", "repo-b"]);

    expect(MockEventSource.lastInstance?.url).toContain(
      "source_repos=repo-a%2Crepo-b",
    );
  });

  it("connect() without sourceRepos omits source_repos from URL", async () => {
    const client = new BeadsSSEClient("test-ws-id");

    await client.connect();

    expect(MockEventSource.lastInstance?.url).not.toContain("source_repos");
  });

  it("connect() with both since and sourceRepos includes both in URL", async () => {
    const client = new BeadsSSEClient("test-ws-id");

    await client.connect(1706011200000, ["repo-x"]);

    expect(MockEventSource.lastInstance?.url).toContain("since=1706011200000");
    expect(MockEventSource.lastInstance?.url).toContain("source_repos=repo-x");
  });

  it("retryNow() uses stored sourceRepos from last connect()", async () => {
    const client = new BeadsSSEClient("test-ws-id");

    await client.connect(undefined, ["repo-a", "repo-b"]);
    MockEventSource.lastInstance?.simulateOpen();

    // Trigger reconnecting state
    MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
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
    const client = new BeadsSSEClient("test-ws-id");

    await client.connect();
    MockEventSource.lastInstance?.simulateOpen();

    // Trigger reconnecting state
    MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
    expect(client.getState()).toBe("reconnecting");

    client.retryNow();
    await vi.waitFor(() => {
      expect(MockEventSource.instances.length).toBe(2);
    });

    expect(MockEventSource.lastInstance?.url).not.toContain("source_repos");
  });
});
