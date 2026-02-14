/**
 * @vitest-environment jsdom
 */
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useLogStream } from './useLogStream';

class MockEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: MockEventSource[] = [];

  url: string;
  readyState = MockEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (e: MessageEvent) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  close(): void {
    this.readyState = MockEventSource.CLOSED;
  }

  emit(type: string, payload: unknown): void {
    const event = { data: JSON.stringify(payload) } as MessageEvent;
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }

  simulateOpen(): void {
    this.readyState = MockEventSource.OPEN;
    this.onopen?.();
  }

  static reset(): void {
    MockEventSource.instances = [];
  }

  static get last(): MockEventSource | undefined {
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

  it('returns raw chunk contract', () => {
    const { result } = renderHook(() => useLogStream({ url: '/api/logs/stream', autoConnect: false }));

    expect(result.current.state).toBe('disconnected');
    expect(result.current.chunks).toEqual([]);
    expect(result.current.resetVersion).toBe(0);
    expect(typeof result.current.clearChunks).toBe('function');
    expect(typeof result.current.connect).toBe('function');
  });

  it('decodes log-chunk payload and stores byte offset', () => {
    const { result } = renderHook(() => useLogStream({ url: '/api/logs/stream', autoConnect: false }));

    act(() => {
      result.current.connect();
      MockEventSource.last?.simulateOpen();
    });

    const chunk = btoa('hello\r\n');
    act(() => {
      MockEventSource.last?.emit('log-chunk', { chunk_b64: chunk, byte_offset: 7 });
    });

    expect(result.current.chunks).toHaveLength(1);
    expect(new TextDecoder().decode(result.current.chunks[0]?.chunk)).toBe('hello\r\n');
    expect(result.current.chunks[0]?.byteOffset).toBe(7);
  });

  it('uses since_bytes on reconnect after receiving data', () => {
    const { result } = renderHook(() => useLogStream({ url: '/api/logs/stream', autoConnect: false }));

    act(() => {
      result.current.connect();
      MockEventSource.last?.simulateOpen();
      MockEventSource.last?.emit('log-chunk', { chunk_b64: btoa('abc'), byte_offset: 3 });
      result.current.disconnect();
      result.current.connect();
    });

    expect(MockEventSource.last?.url).toContain('since_bytes=3');
  });

  it('resets chunk buffer on truncated event', () => {
    const { result } = renderHook(() => useLogStream({ url: '/api/logs/stream', autoConnect: false }));

    act(() => {
      result.current.connect();
      MockEventSource.last?.simulateOpen();
      MockEventSource.last?.emit('log-chunk', { chunk_b64: btoa('abc'), byte_offset: 3 });
    });
    expect(result.current.chunks).toHaveLength(1);

    const beforeReset = result.current.resetVersion;
    act(() => {
      MockEventSource.last?.emit('truncated', {});
    });

    expect(result.current.chunks).toHaveLength(0);
    expect(result.current.resetVersion).toBe(beforeReset + 1);
  });
});
