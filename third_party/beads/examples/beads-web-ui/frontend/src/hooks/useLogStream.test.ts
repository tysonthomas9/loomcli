/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { useLogStream } from './useLogStream';

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
  onmessage: ((e: MessageEvent) => void) | null = null;

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

  simulateLogLine(data: { line: string; timestamp?: string }): void {
    const event = { data: JSON.stringify(data) } as MessageEvent;

    // Dispatch to 'log-line' event listeners (preferred path matching backend design)
    const listeners = this.eventListeners.get('log-line') ?? [];
    for (const listener of listeners) {
      listener(event);
    }
  }

  simulatePlainTextMessage(text: string): void {
    // Simulate a plain text message (not JSON)
    const event = { data: text } as MessageEvent;

    // Use log-line event listeners
    const listeners = this.eventListeners.get('log-line') ?? [];
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

describe('useLogStream', () => {
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
    it('returns expected shape with all methods and state', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      expect(result.current).toHaveProperty('lines');
      expect(result.current).toHaveProperty('state');
      expect(result.current).toHaveProperty('isConnected');
      expect(result.current).toHaveProperty('reconnectAttempts');
      expect(result.current).toHaveProperty('lastError');
      expect(result.current).toHaveProperty('clearLines');
      expect(result.current).toHaveProperty('connect');
      expect(result.current).toHaveProperty('disconnect');

      expect(typeof result.current.connect).toBe('function');
      expect(typeof result.current.disconnect).toBe('function');
      expect(typeof result.current.clearLines).toBe('function');
      expect(Array.isArray(result.current.lines)).toBe(true);
    });

    it('initial state is disconnected with empty lines', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      expect(result.current.state).toBe('disconnected');
      expect(result.current.isConnected).toBe(false);
      expect(result.current.lastError).toBeNull();
      expect(result.current.reconnectAttempts).toBe(0);
      expect(result.current.lines).toHaveLength(0);
    });
  });

  describe('Auto-connect option', () => {
    it('when true connects on mount', () => {
      renderHook(() => useLogStream({ url: '/api/logs/stream', autoConnect: true }));

      // EventSource should be created automatically
      expect(MockEventSource.instances.length).toBe(1);
      expect(MockEventSource.lastInstance?.url).toContain('/api/logs/stream');
    });

    it('when false does not connect on mount', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      // No EventSource should be created
      expect(MockEventSource.instances.length).toBe(0);
      expect(result.current.state).toBe('disconnected');
    });

    it('defaults to true', () => {
      renderHook(() => useLogStream({ url: '/api/logs/stream' }));

      // EventSource should be created automatically (default autoConnect: true)
      expect(MockEventSource.instances.length).toBe(1);
    });
  });

  describe('Connection lifecycle', () => {
    it('connect() creates EventSource', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      expect(MockEventSource.lastInstance).toBeDefined();
      expect(MockEventSource.lastInstance?.url).toContain('/api/logs/stream');
    });

    it('state transitions from disconnected to connecting to connected', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      expect(result.current.state).toBe('disconnected');

      act(() => {
        result.current.connect();
      });

      expect(result.current.state).toBe('connecting');

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe('connected');
      expect(result.current.isConnected).toBe(true);
    });

    it('disconnect() closes EventSource and updates state', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

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

      expect(result.current.state).toBe('disconnected');
      expect(result.current.isConnected).toBe(false);
    });

    it('component unmount cleans up EventSource', () => {
      const { result, unmount } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
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

    it('does not create duplicate connections when already connected', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      const instanceCount = MockEventSource.instances.length;

      act(() => {
        result.current.connect();
      });

      // Should not create a new EventSource
      expect(MockEventSource.instances.length).toBe(instanceCount);
    });
  });

  describe('Log line handling', () => {
    it('receives and stores log lines', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({
          line: 'First log message',
          timestamp: '2026-02-03T10:00:00Z',
        });
      });

      expect(result.current.lines).toHaveLength(1);
      expect(result.current.lines[0].line).toBe('First log message');
      expect(result.current.lines[0].lineNumber).toBe(1);
      expect(result.current.lines[0].timestamp).toBe('2026-02-03T10:00:00Z');
    });

    it('assigns sequential line numbers', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 1' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 2' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 3' });
      });

      expect(result.current.lines).toHaveLength(3);
      expect(result.current.lines[0].lineNumber).toBe(1);
      expect(result.current.lines[1].lineNumber).toBe(2);
      expect(result.current.lines[2].lineNumber).toBe(3);
    });

    it('handles plain text messages (non-JSON)', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulatePlainTextMessage('Plain text log');
      });

      expect(result.current.lines).toHaveLength(1);
      expect(result.current.lines[0].line).toBe('Plain text log');
    });

    it('generates timestamp when not provided', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'No timestamp' });
      });

      expect(result.current.lines[0].timestamp).toBeDefined();
      expect(result.current.lines[0].timestamp).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    });
  });

  describe('maxLines buffer management', () => {
    it('respects maxLines limit', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false, maxLines: 3 })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 1' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 2' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 3' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 4' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 5' });
      });

      // Should only keep the last 3 lines
      expect(result.current.lines).toHaveLength(3);
      expect(result.current.lines[0].line).toBe('Line 3');
      expect(result.current.lines[1].line).toBe('Line 4');
      expect(result.current.lines[2].line).toBe('Line 5');
    });

    it('preserves line numbers after buffer truncation', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false, maxLines: 2 })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 1' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 2' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 3' });
      });

      // Line numbers should still be 2 and 3
      expect(result.current.lines[0].lineNumber).toBe(2);
      expect(result.current.lines[1].lineNumber).toBe(3);
    });

    it('defaults to 5000 maxLines', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      // Can't easily test 5000 lines, but verify hook accepts default
      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Should accept many lines without issues
      act(() => {
        for (let i = 0; i < 100; i++) {
          MockEventSource.lastInstance?.simulateLogLine({ line: `Line ${i + 1}` });
        }
      });

      expect(result.current.lines).toHaveLength(100);
    });
  });

  describe('clearLines', () => {
    it('clears all lines from buffer', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 1' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 2' });
      });

      expect(result.current.lines).toHaveLength(2);

      act(() => {
        result.current.clearLines();
      });

      expect(result.current.lines).toHaveLength(0);
    });

    it('resets line numbers after clearing', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 1' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 2' });
      });

      act(() => {
        result.current.clearLines();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'New Line 1' });
      });

      // Line number should restart from 1
      expect(result.current.lines[0].lineNumber).toBe(1);
    });
  });

  describe('Reconnection handling', () => {
    it('transitions to reconnecting state on error while connecting', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.state).toBe('reconnecting');
    });

    it('increments reconnectAttempts on errors', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

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

    it('resets reconnectAttempts on successful connection', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateError(MockEventSource.CONNECTING);
      });

      expect(result.current.reconnectAttempts).toBe(1);

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.reconnectAttempts).toBe(0);
    });

    it('sets lastError when connection is closed', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
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

      expect(result.current.lastError).toBe('Connection closed');
    });

    it('clears lastError on successful connection', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
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

      expect(result.current.lastError).toBe('Connection closed');

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.lastError).toBeNull();
    });

    it('includes since parameter in reconnection URL', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      // Receive some lines
      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 1' });
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Line 2' });
      });

      // Disconnect and reconnect
      act(() => {
        result.current.disconnect();
      });

      act(() => {
        result.current.connect();
      });

      // URL should include since parameter
      expect(MockEventSource.lastInstance?.url).toContain('since=2');
    });
  });

  describe('URL change handling', () => {
    it('reconnects when URL changes', () => {
      const { result, rerender } = renderHook(
        ({ url }: { url: string }) => useLogStream({ url, autoConnect: true }),
        { initialProps: { url: '/api/logs/stream1' } }
      );

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.isConnected).toBe(true);
      const firstInstanceUrl = MockEventSource.lastInstance?.url;

      // Change URL
      rerender({ url: '/api/logs/stream2' });

      // Should create new connection
      expect(MockEventSource.lastInstance?.url).toContain('/api/logs/stream2');
      expect(MockEventSource.lastInstance?.url).not.toBe(firstInstanceUrl);
    });

    it('clears lines when URL changes', () => {
      const { result, rerender } = renderHook(
        ({ url }: { url: string }) => useLogStream({ url, autoConnect: true }),
        { initialProps: { url: '/api/logs/stream1' } }
      );

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateLogLine({ line: 'Old stream line' });
      });

      expect(result.current.lines).toHaveLength(1);

      // Change URL
      rerender({ url: '/api/logs/stream2' });

      // Lines should be cleared
      expect(result.current.lines).toHaveLength(0);
    });
  });

  describe('State reactivity', () => {
    it('state changes trigger re-renders', () => {
      const renderCount = { count: 0 };
      const { result } = renderHook(() => {
        renderCount.count++;
        return useLogStream({ url: '/api/logs/stream', autoConnect: false });
      });

      const initialCount = renderCount.count;

      act(() => {
        result.current.connect();
      });

      expect(renderCount.count).toBeGreaterThan(initialCount);
    });

    it('isConnected computed correctly based on state', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      expect(result.current.isConnected).toBe(false);
      expect(result.current.state).toBe('disconnected');

      act(() => {
        result.current.connect();
      });

      expect(result.current.state).toBe('connecting');
      expect(result.current.isConnected).toBe(false);

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      expect(result.current.state).toBe('connected');
      expect(result.current.isConnected).toBe(true);
    });
  });

  describe('Methods stability', () => {
    it('methods are stable across renders', () => {
      const { result, rerender } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      const initialConnect = result.current.connect;
      const initialDisconnect = result.current.disconnect;
      const initialClearLines = result.current.clearLines;

      rerender();

      expect(result.current.connect).toBe(initialConnect);
      expect(result.current.disconnect).toBe(initialDisconnect);
      expect(result.current.clearLines).toBe(initialClearLines);
    });
  });

  describe('SSR compatibility', () => {
    it('works when autoConnect is false (SSR-safe pattern)', () => {
      const { result } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      // Should have initial state and no EventSource created
      expect(result.current.state).toBe('disconnected');
      expect(result.current.isConnected).toBe(false);
      expect(MockEventSource.instances.length).toBe(0);

      // Hook should still return all expected methods
      expect(typeof result.current.connect).toBe('function');
      expect(typeof result.current.disconnect).toBe('function');
      expect(typeof result.current.clearLines).toBe('function');
    });
  });

  describe('Cleanup on unmount', () => {
    it('does not process messages after unmount', () => {
      const { result, unmount } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      unmount();

      // Try to send a message after unmount - should not throw
      expect(() => {
        esInstance?.simulateLogLine({ line: 'Should be ignored' });
      }).not.toThrow();
    });

    it('does not update state after unmount', () => {
      const { result, unmount } = renderHook(() =>
        useLogStream({ url: '/api/logs/stream', autoConnect: false })
      );

      act(() => {
        result.current.connect();
      });

      act(() => {
        MockEventSource.lastInstance?.simulateOpen();
      });

      const esInstance = MockEventSource.lastInstance;

      unmount();

      // Trigger error after unmount - should not throw
      expect(() => {
        esInstance?.simulateError(MockEventSource.CONNECTING);
      }).not.toThrow();
    });
  });
});
