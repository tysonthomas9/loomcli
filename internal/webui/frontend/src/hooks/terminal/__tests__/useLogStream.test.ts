/**
 * @vitest-environment jsdom
 */

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useLogStream } from "../useLogStream";

class MockEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: MockEventSource[] = [];

  readonly url: string;
  readyState = MockEventSource.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  private readonly listeners = new Map<string, Set<EventListener>>();

  constructor(url: string | URL) {
    this.url = String(url);
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListener): void {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListener): void {
    this.listeners.get(type)?.delete(listener);
  }

  close(): void {
    this.closed = true;
    this.readyState = MockEventSource.CLOSED;
  }

  simulateOpen(): void {
    this.readyState = MockEventSource.OPEN;
    this.onopen?.(new Event("open"));
  }

  simulateError(): void {
    this.onerror?.(new Event("error"));
  }

  simulateEvent(type: string, data = "{}"): void {
    const event = new MessageEvent(type, { data });
    for (const listener of this.listeners.get(type) ?? []) {
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

function encodeBytes(bytes: number[]): string {
  return btoa(String.fromCharCode(...bytes));
}

function logChunk(bytes: number[], byteOffset: number): string {
  return JSON.stringify({
    chunk_b64: encodeBytes(bytes),
    byte_offset: byteOffset,
    timestamp: "2026-08-30T12:00:00Z",
  });
}

async function waitForSource(): Promise<MockEventSource> {
  await waitFor(() => expect(MockEventSource.lastInstance).toBeDefined());
  return MockEventSource.lastInstance as MockEventSource;
}

describe("useLogStream", () => {
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    originalEventSource = global.EventSource;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.reset();
  });

  afterEach(() => {
    global.EventSource = originalEventSource;
    vi.useRealTimers();
  });

  it("fetches a one-time token and opens the initial tail window", async () => {
    const fetchToken = vi
      .fn()
      .mockResolvedValue({ kind: "token", token: "fresh-token" });

    const { result } = renderHook(() =>
      useLogStream({
        workspaceId: "ws-a",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
        tailBytes: 64,
        fetchToken,
      }),
    );

    const source = await waitForSource();
    expect(fetchToken).toHaveBeenCalledTimes(1);
    expect(source.url).toContain(
      "/api/workspaces/ws-a/agents/ember/logs/stream",
    );
    expect(source.url).toContain("tail_bytes=64");
    expect(source.url).toContain("token=fresh-token");
    expect(source.url).not.toContain("offset=");

    act(() => source.simulateOpen());
    expect(result.current.state).toBe("connected");
  });

  it("decodes multibyte text through one streaming TextDecoder", async () => {
    const fetchToken = vi.fn().mockResolvedValue({ kind: "disabled" });
    const { result } = renderHook(() =>
      useLogStream({
        workspaceId: "ws-a",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
        fetchToken,
      }),
    );
    const source = await waitForSource();

    act(() => {
      source.simulateEvent("log-chunk", logChunk([0xf0, 0x9f], 2));
    });
    expect(result.current.content).toBe("");

    act(() => {
      source.simulateEvent("log-chunk", logChunk([0x98, 0x80], 4));
    });
    expect(result.current.content).toBe("😀");
  });

  it("clears content, offset, and decoder on truncated", async () => {
    const fetchToken = vi.fn().mockResolvedValue({ kind: "disabled" });
    const { result } = renderHook(() =>
      useLogStream({
        workspaceId: "ws-a",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
        fetchToken,
      }),
    );
    const source = await waitForSource();

    act(() => {
      source.simulateEvent("log-chunk", logChunk([0x62, 0x65, 0x66], 3));
    });
    expect(result.current.content).toBe("bef");

    act(() => source.simulateEvent("truncated"));
    expect(result.current.content).toBe("");

    act(() => {
      source.simulateEvent("log-chunk", logChunk([0x6e, 0x65, 0x77], 3));
    });
    expect(result.current.content).toBe("new");
  });

  it("closes native retry and reconnects with a fresh token at the latest offset", async () => {
    vi.useFakeTimers();
    const fetchToken = vi
      .fn()
      .mockResolvedValueOnce({ kind: "token", token: "token-one" })
      .mockResolvedValueOnce({ kind: "token", token: "token-two" });
    const { result } = renderHook(() =>
      useLogStream({
        workspaceId: "ws-a",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
        tailBytes: 64,
        fetchToken,
      }),
    );

    await act(async () => {
      await Promise.resolve();
    });
    const first = MockEventSource.lastInstance as MockEventSource;
    act(() => {
      first.simulateOpen();
      first.simulateEvent("log-chunk", logChunk([0x61, 0x62, 0x63], 3));
      first.simulateError();
    });
    expect(first.closed).toBe(true);
    expect(result.current.state).toBe("reconnecting");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(fetchToken).toHaveBeenCalledTimes(2);
    expect(MockEventSource.instances).toHaveLength(2);
    const second = MockEventSource.lastInstance as MockEventSource;
    expect(second.url).toContain("token=token-two");
    expect(second.url).toContain("offset=3");
    expect(second.url).not.toContain("tail_bytes=");
  });

  it("keeps the tail window on a reconnect that received no chunks", async () => {
    vi.useFakeTimers();
    const fetchToken = vi
      .fn()
      .mockResolvedValueOnce({ kind: "token", token: "token-one" })
      .mockResolvedValueOnce({ kind: "token", token: "token-two" });
    renderHook(() =>
      useLogStream({
        workspaceId: "ws-a",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
        tailBytes: 64,
        fetchToken,
      }),
    );

    await act(async () => {
      await Promise.resolve();
    });
    const first = MockEventSource.lastInstance as MockEventSource;
    act(() => first.simulateError());

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(MockEventSource.instances).toHaveLength(2);
    const second = MockEventSource.lastInstance as MockEventSource;
    expect(second.url).toContain("tail_bytes=64");
    expect(second.url).not.toContain("offset=");
  });

  it("closes the EventSource and cancels reconnects on unmount", async () => {
    vi.useFakeTimers();
    const fetchToken = vi.fn().mockResolvedValue({ kind: "disabled" });
    const { unmount } = renderHook(() =>
      useLogStream({
        workspaceId: "ws-a",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
        fetchToken,
      }),
    );
    await act(async () => {
      await Promise.resolve();
    });
    const source = MockEventSource.lastInstance as MockEventSource;
    act(() => source.simulateError());

    unmount();
    expect(source.closed).toBe(true);
    await vi.advanceTimersByTimeAsync(30000);
    expect(MockEventSource.instances).toHaveLength(1);
  });
});
