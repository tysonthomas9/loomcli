/** @vitest-environment jsdom */
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { WorkspaceSSEClient } from "../sse";

class ReplayEventSource {
  static instances: ReplayEventSource[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;

  onerror: (() => void) | null = null;
  onopen: (() => void) | null = null;
  readyState = ReplayEventSource.CONNECTING;
  private listeners = new Map<string, Array<(event: MessageEvent) => void>>();

  constructor(readonly url: string) {
    ReplayEventSource.instances.push(this);
  }

  addEventListener(
    type: string,
    listener: (event: MessageEvent) => void,
  ): void {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  emit(type: string, data: string, id: string): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data, lastEventId: id } as MessageEvent);
    }
  }
  close(): void {
    this.readyState = ReplayEventSource.CLOSED;
  }
}

let originalEventSource: typeof EventSource;

beforeEach(() => {
  vi.useFakeTimers();
  originalEventSource = globalThis.EventSource;
  globalThis.EventSource = ReplayEventSource as unknown as typeof EventSource;
  ReplayEventSource.instances = [];
});

afterEach(() => {
  globalThis.EventSource = originalEventSource;
  vi.useRealTimers();
});

it("#577 retries a transient token exchange failure", async () => {
  const fetchToken = vi
    .fn()
    .mockResolvedValueOnce({ kind: "error", message: "serve restarting" })
    .mockResolvedValue({ kind: "token", token: "fresh" });
  const client = new WorkspaceSSEClient("ws-replay", {
    fetchToken,
    initialReconnectDelay: 10,
    maxReconnectDelay: 10,
  });
  await client.connect();
  await vi.advanceTimersByTimeAsync(10);
  expect(fetchToken).toHaveBeenCalledTimes(2);
  expect(ReplayEventSource.instances).toHaveLength(1);
  client.destroy();
});

it("#627a stops a stream after a malformed mutation frame", async () => {
  const onError = vi.fn();
  const client = new WorkspaceSSEClient("ws-replay", {
    fetchToken: async () => ({ kind: "token", token: "ok" }),
    onError,
  });
  await client.connect();
  ReplayEventSource.instances[0]!.emit("mutation", "{malformed", "1-0");
  expect(onError).toHaveBeenCalledWith(expect.stringContaining("Malformed"));
  expect(client.getState()).toBe("disconnected");
  client.destroy();
});
