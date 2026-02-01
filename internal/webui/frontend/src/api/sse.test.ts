/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { BeadsSSEClient, getSSEUrl } from './sse';
import type { MutationPayload } from './sse';

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

  private eventListeners: Map<string, ((e: MessageEvent) => void)[]> = new Map();

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
    const listeners = this.eventListeners.get('mutation') ?? [];
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
    const listeners = this.eventListeners.get('connected') ?? [];
    const event = { data: '' } as MessageEvent;
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

describe('BeadsSSEClient', () => {
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

  describe('Initialization', () => {
    it('creates a client with initial disconnected state', () => {
      const client = new BeadsSSEClient();

      expect(client.getState()).toBe('disconnected');
      expect(client.getReconnectAttempts()).toBe(0);
    });

    it('accepts callbacks in options', () => {
      const onMutation = vi.fn();
      const onError = vi.fn();
      const onStateChange = vi.fn();
      const onReconnect = vi.fn();

      const client = new BeadsSSEClient({
        onMutation,
        onError,
        onStateChange,
        onReconnect,
      });

      expect(client.getState()).toBe('disconnected');
    });
  });

  describe('Connection lifecycle', () => {
    it('connect() creates EventSource', () => {
      const client = new BeadsSSEClient();

      client.connect();

      expect(MockEventSource.lastInstance).toBeDefined();
      expect(MockEventSource.lastInstance?.url).toContain('/api/events');
    });

    it('connect() with since parameter adds query string', () => {
      const client = new BeadsSSEClient();

      client.connect(1706011200000);

      expect(MockEventSource.lastInstance?.url).toContain('since=1706011200000');
    });

    it('state transitions from disconnected to connecting to connected', () => {
      const onStateChange = vi.fn();
      const client = new BeadsSSEClient({ onStateChange });

      expect(client.getState()).toBe('disconnected');

      client.connect();

      expect(client.getState()).toBe('connecting');
      expect(onStateChange).toHaveBeenCalledWith('connecting');

      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe('connected');
      expect(onStateChange).toHaveBeenCalledWith('connected');
    });

    it('disconnect() closes EventSource and updates state', () => {
      const onStateChange = vi.fn();
      const client = new BeadsSSEClient({ onStateChange });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe('connected');

      const esInstance = MockEventSource.lastInstance;

      client.disconnect();

      expect(client.getState()).toBe('disconnected');
      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(onStateChange).toHaveBeenCalledWith('disconnected');
    });

    it('connect() when already connected does nothing', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(MockEventSource.instances.length).toBe(1);

      client.connect();

      expect(MockEventSource.instances.length).toBe(1);
    });

    it('connect() when connecting does nothing', () => {
      const client = new BeadsSSEClient();

      client.connect();

      expect(client.getState()).toBe('connecting');
      expect(MockEventSource.instances.length).toBe(1);

      client.connect();

      expect(MockEventSource.instances.length).toBe(1);
    });
  });

  describe('Message parsing and callback invocation', () => {
    it('onMutation called with parsed payload', () => {
      const onMutation = vi.fn();
      const client = new BeadsSSEClient({ onMutation });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: 'create',
        issue_id: 'beads-123',
        title: 'Test Issue',
        timestamp: '2025-01-23T12:00:00Z',
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      expect(onMutation).toHaveBeenCalledWith(mutation);
    });

    it('malformed JSON is ignored with warning', () => {
      const onMutation = vi.fn();
      const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
      const client = new BeadsSSEClient({ onMutation });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate invalid JSON by directly calling the listener with malformed data
      const listeners =
        (MockEventSource.lastInstance as MockEventSource)['eventListeners'].get('mutation') ?? [];
      const event = { data: 'not valid json' } as MessageEvent;
      for (const listener of listeners) {
        listener(event);
      }

      expect(onMutation).not.toHaveBeenCalled();
      expect(consoleWarnSpy).toHaveBeenCalledWith('[SSE] Received malformed mutation event');

      consoleWarnSpy.mockRestore();
    });

    it('connected event is handled', () => {
      const originalEnv = process.env.NODE_ENV;
      process.env.NODE_ENV = 'development';
      const consoleDebugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {});

      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();
      MockEventSource.lastInstance?.simulateConnectedEvent();

      expect(consoleDebugSpy).toHaveBeenCalledWith('[SSE] Received connected event');

      consoleDebugSpy.mockRestore();
      process.env.NODE_ENV = originalEnv;
    });
  });

  describe('Error handling and reconnect state tracking', () => {
    it('error during connecting state triggers reconnecting', () => {
      const onStateChange = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient({ onStateChange, onReconnect });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate error while browser is reconnecting
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);

      expect(client.getState()).toBe('reconnecting');
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onStateChange).toHaveBeenCalledWith('reconnecting');
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it('error during closed state triggers reconnecting and calls onError', () => {
      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient({ onError, onReconnect });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Simulate closed state error
      MockEventSource.lastInstance?.simulateError(MockEventSource.CLOSED);

      expect(client.getState()).toBe('reconnecting');
      expect(client.getReconnectAttempts()).toBe(1);
      expect(onError).toHaveBeenCalledWith('Connection closed');
      expect(onReconnect).toHaveBeenCalledWith(1);
    });

    it('reconnectAttempts increments on consecutive errors', () => {
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient({ onReconnect });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(1);

      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(2);

      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getReconnectAttempts()).toBe(3);
    });

    it('reconnectAttempts resets to 0 on successful open', () => {
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient({ onReconnect });

      client.connect();
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

    it('error after manual disconnect is ignored', () => {
      const onError = vi.fn();
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient({ onError, onReconnect });

      client.connect();
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

    it('logs warning after 5 connection failures', () => {
      const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      for (let i = 0; i < 5; i++) {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      }

      expect(consoleWarnSpy).toHaveBeenCalledWith(
        '[SSE] Multiple connection failures, will continue retrying'
      );

      consoleWarnSpy.mockRestore();
    });
  });

  describe('Last event ID tracking for reconnection catch-up', () => {
    it('getLastEventId returns undefined initially', () => {
      const client = new BeadsSSEClient();

      expect(client.getLastEventId()).toBeUndefined();
    });

    it('getLastEventId returns the last event ID after receiving a mutation', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: 'create',
        issue_id: 'beads-123',
        title: 'Test Issue',
        timestamp: '2025-01-23T12:00:00Z',
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      const expectedTime = Date.parse('2025-01-23T12:00:00Z');
      expect(client.getLastEventId()).toBe(expectedTime);
    });

    it('tracks last event ID from event.lastEventId', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: 'create',
        issue_id: 'beads-123',
        title: 'Test Issue',
        timestamp: '2025-01-23T12:00:00Z',
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      // Disconnect and reconnect - should use last event ID
      client.disconnect();
      client.connect();

      const expectedTime = Date.parse('2025-01-23T12:00:00Z');
      expect(MockEventSource.lastInstance?.url).toContain(`since=${expectedTime}`);
    });

    it('uses latest event ID when receiving multiple mutations', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation1: MutationPayload = {
        type: 'create',
        issue_id: 'beads-123',
        title: 'First Issue',
        timestamp: '2025-01-23T12:00:00Z',
      };

      const mutation2: MutationPayload = {
        type: 'update',
        issue_id: 'beads-456',
        title: 'Second Issue',
        timestamp: '2025-01-23T14:00:00Z',
      };

      MockEventSource.lastInstance?.simulateMutation(mutation1);
      MockEventSource.lastInstance?.simulateMutation(mutation2);

      client.disconnect();
      client.connect();

      const expectedTime = Date.parse('2025-01-23T14:00:00Z');
      expect(MockEventSource.lastInstance?.url).toContain(`since=${expectedTime}`);
    });

    it('connect with explicit since overrides last event ID', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation: MutationPayload = {
        type: 'create',
        issue_id: 'beads-123',
        title: 'Test Issue',
        timestamp: '2025-01-23T12:00:00Z',
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      client.disconnect();
      client.connect(1706100000000);

      expect(MockEventSource.lastInstance?.url).toContain('since=1706100000000');
    });

    it('invalid timestamp in mutation is ignored for tracking', () => {
      const onMutation = vi.fn();
      const client = new BeadsSSEClient({ onMutation });

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      const mutation = {
        type: 'create',
        issue_id: 'beads-123',
        title: 'Test Issue',
        timestamp: 'invalid-date',
      };

      MockEventSource.lastInstance?.simulateMutation(mutation);

      // Callback should still be called
      expect(onMutation).toHaveBeenCalled();

      client.disconnect();
      client.connect();

      // Should not have a since parameter since timestamp was invalid
      expect(MockEventSource.lastInstance?.url).not.toContain('since=');
    });
  });

  describe('retryNow', () => {
    it('only works when in reconnecting state', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      expect(client.getState()).toBe('connected');

      client.retryNow();

      // Should not create a new EventSource
      expect(MockEventSource.instances.length).toBe(1);
    });

    it('creates new connection immediately when in reconnecting state', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();

      // Trigger reconnecting state
      MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      expect(client.getState()).toBe('reconnecting');
      expect(MockEventSource.instances.length).toBe(1);

      client.retryNow();

      expect(MockEventSource.instances.length).toBe(2);
      expect(client.getState()).toBe('connecting');
    });

    it('resets reconnect counter on manual retry', () => {
      const onReconnect = vi.fn();
      const client = new BeadsSSEClient({ onReconnect });

      client.connect();
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

  describe('Cleanup on destroy', () => {
    it('destroy() closes EventSource and clears callbacks', () => {
      const onMutation = vi.fn();
      const onStateChange = vi.fn();
      const client = new BeadsSSEClient({ onMutation, onStateChange });

      client.connect();
      const esInstance = MockEventSource.lastInstance;
      MockEventSource.lastInstance?.simulateOpen();

      client.destroy();

      expect(esInstance?.readyState).toBe(MockEventSource.CLOSED);
      expect(client.getState()).toBe('disconnected');

      // Callbacks should not be called after destroy
      onStateChange.mockClear();
      onMutation.mockClear();

      // Try to trigger callbacks - they should not be called
      esInstance?.simulateOpen();
      esInstance?.simulateMutation({
        type: 'create',
        issue_id: 'beads-789',
        title: 'Should not trigger',
        timestamp: '2025-01-23T12:00:00Z',
      });

      expect(onStateChange).not.toHaveBeenCalled();
      expect(onMutation).not.toHaveBeenCalled();
    });

    it('instance should not be reused after destroy', () => {
      const client = new BeadsSSEClient();

      client.connect();
      MockEventSource.lastInstance?.simulateOpen();
      expect(client.getState()).toBe('connected');

      client.destroy();
      expect(client.getState()).toBe('disconnected');

      // Note: The client doesn't prevent reuse, but callbacks are cleared
      // This documents the behavior - destroy() clears callbacks
    });
  });
});

describe('getSSEUrl', () => {
  it('returns base URL without since parameter', () => {
    const url = getSSEUrl();

    expect(url).toBe(`${window.location.origin}/api/events`);
  });

  it('includes since parameter when provided', () => {
    const url = getSSEUrl(1706011200000);

    expect(url).toBe(`${window.location.origin}/api/events?since=1706011200000`);
  });

  it('handles since value of 0', () => {
    const url = getSSEUrl(0);

    expect(url).toBe(`${window.location.origin}/api/events?since=0`);
  });
});
