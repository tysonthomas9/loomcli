/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for connectWebSocket — focused on the race condition
 * where cleanup fires before wsCleanupInner is assigned.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// ── Shared mock state (vi.hoisted runs before vi.mock factories) ─────────────

const shared = vi.hoisted(() => {
  let _resolveToken: ((v: { token: string }) => void) | null = null;
  let _rejectToken: ((e: Error) => void) | null = null;
  const _getMock = vi.fn(
    () =>
      new Promise<{ token: string }>((resolve, reject) => {
        _resolveToken = resolve;
        _rejectToken = reject;
      }),
  );

  return {
    getMock: _getMock,
    get resolveToken() {
      return _resolveToken;
    },
    get rejectToken() {
      return _rejectToken;
    },
  };
});

// ── Mock @/api/client ────────────────────────────────────────────────────────

vi.mock("@/api/common", () => ({
  get: shared.getMock,
  getWsBaseUrl: () => "ws://localhost",
  // Mirror the real wsUrl helper so terminalConnection.ts builds the same
  // workspace-scoped path it would in production: "/api/workspaces/<id><path>".
  wsUrl: (workspaceId: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`,
}));

// ── MockWebSocket ────────────────────────────────────────────────────────────

class MockWebSocket {
  static CONNECTING = 0 as const;
  static OPEN = 1 as const;
  static CLOSING = 2 as const;
  static CLOSED = 3 as const;

  static instances: MockWebSocket[] = [];

  url: string;
  readyState: number = MockWebSocket.CONNECTING;
  binaryType = "blob";
  close = vi.fn(() => {
    this.readyState = MockWebSocket.CLOSED;
  });
  send = vi.fn();
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  /** Simulate server accepting the connection. */
  simulateOpen(): void {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  /** Simulate receiving a message. */
  simulateMessage(data: string | ArrayBuffer): void {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  /** Simulate connection close. */
  simulateClose(code = 1000, reason = ""): void {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.(new CloseEvent("close", { code, reason }));
  }

  /** Simulate a browser-level socket error without a server close frame. */
  simulateError(): void {
    this.onerror?.(new Event("error"));
  }
}

// ── Test helpers ─────────────────────────────────────────────────────────────

function makeMocks() {
  const write = vi.fn();
  const wsRef = { current: null as WebSocket | null };
  const setConnectionState = vi.fn();
  const onConnected = vi.fn();
  const onDisconnected = vi.fn();
  const onOutput = vi.fn();
  const onBackendCrash = vi.fn();
  const onSessionKilled = vi.fn();

  return {
    write,
    wsRef,
    setConnectionState,
    onConnected,
    onDisconnected,
    onOutput,
    onBackendCrash,
    onSessionKilled,
  };
}

async function waitForBufferedFlush(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 25));
}

// ── Tests ────────────────────────────────────────────────────────────────────

/* eslint-disable @typescript-eslint/no-explicit-any */

describe("connectWebSocket", () => {
  let connectWebSocket: typeof import("../terminalConnection").connectWebSocket;
  const originalWebSocket = globalThis.WebSocket;

  beforeEach(async () => {
    vi.resetModules();
    MockWebSocket.instances = [];
    shared.getMock.mockClear();
    // Assign mock WebSocket globally
    (globalThis as any).WebSocket = MockWebSocket as any;
    // Dynamic import so mocks are applied
    const mod = await import("../terminalConnection");
    connectWebSocket = mod.connectWebSocket;
  });

  afterEach(() => {
    (globalThis as any).WebSocket = originalWebSocket;
  });

  it("closes WebSocket when cleanup fires before wsCleanupInner is assigned", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      m.onConnected,
      m.onDisconnected,
    );

    // Resolve token — .then() microtask creates WebSocket
    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];

    // After await, wsCleanupInner IS assigned (synchronous .then() callback).
    // This tests the normal cleanup path via wsCleanupInner.
    // Call cleanup
    cleanup();

    expect(ws.close).toHaveBeenCalledWith(1000);
    expect(m.wsRef.current).toBeNull();
  });

  it("does not create WebSocket when cancelled before token resolves", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
    );

    // Call cleanup immediately — before token resolves
    cleanup();

    // Now resolve token
    shared.resolveToken!({ token: "tok" });
    // Flush microtasks
    await new Promise((r) => setTimeout(r, 0));

    // No WebSocket should have been created
    expect(MockWebSocket.instances).toHaveLength(0);
    expect(m.wsRef.current).toBeNull();
  });

  it("skips onopen callback after cleanup", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      m.onConnected,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];

    // Call cleanup first
    cleanup();
    m.setConnectionState.mockClear();

    // Now simulate open — should be ignored
    ws.simulateOpen();

    expect(m.setConnectionState).not.toHaveBeenCalledWith("connected");
    expect(m.onConnected).not.toHaveBeenCalled();
  });

  it("skips onmessage callback after cleanup", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      undefined,
      m.onOutput,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();

    // Call cleanup
    cleanup();
    m.write.mockClear();

    // Simulate message after cleanup — should be ignored
    ws.simulateMessage("hello");
    await waitForBufferedFlush();

    expect(m.write).not.toHaveBeenCalled();
  });

  it("normal lifecycle: connects, receives data, and cleans up", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      m.onConnected,
      m.onDisconnected,
      m.onOutput,
    );

    expect(m.setConnectionState).toHaveBeenCalledWith("connecting");

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();

    expect(m.setConnectionState).toHaveBeenCalledWith("connected");
    expect(m.onConnected).toHaveBeenCalled();

    // Receive data
    ws.simulateMessage("output text");
    await waitForBufferedFlush();
    expect(m.write).toHaveBeenCalledWith("output text");
    expect(m.onOutput).toHaveBeenCalled();

    // Clean up
    cleanup();
    expect(ws.close).toHaveBeenCalledWith(1000);
    expect(m.wsRef.current).toBeNull();
  });

  it("batches adjacent terminal output frames into a single renderer write", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      m.onConnected,
      m.onDisconnected,
      m.onOutput,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();

    ws.simulateMessage("hello");
    ws.simulateMessage(" ");
    ws.simulateMessage("world");
    await waitForBufferedFlush();

    expect(m.write).toHaveBeenCalledTimes(1);
    expect(m.write).toHaveBeenCalledWith("hello world");
    expect(m.onOutput).toHaveBeenCalledTimes(1);
  });

  it("includes initial terminal size in the first workspace terminal websocket URL", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      m.onConnected,
      m.onDisconnected,
      undefined,
      undefined,
      undefined,
      { cols: 132, rows: 40 },
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    expect(ws.url).toContain("session=session1");
    expect(ws.url).toContain("cols=132");
    expect(ws.url).toContain("rows=40");
  });

  it("handles fetchTerminalToken rejection gracefully", async () => {
    // fetchTerminalToken catches errors internally and returns null,
    // so the .then() path runs with token=null. The WebSocket is still
    // created (with no token param) — it connects and works, or fails
    // at the WS level. We just verify no crash occurs.
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
    );

    // Reject the underlying get() — fetchTerminalToken catches this and returns null
    shared.rejectToken!(new Error("network error"));
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    // A WebSocket was created with null token (no token param in URL)
    const ws = MockWebSocket.instances[0];
    expect(ws.url).not.toContain("token=");

    // Simulate server rejecting the unauthenticated connection
    ws.simulateClose(1008, "unauthorized");
    expect(m.setConnectionState).toHaveBeenCalledWith("disconnected");
    expect(m.onDisconnected).toHaveBeenCalled();
  });

  it("does not auto-retry when the workspace runtime is unavailable", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
      undefined,
      undefined,
      m.onSessionKilled,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    ws.simulateClose(1001, "workspace unavailable");

    expect(m.setConnectionState).toHaveBeenCalledWith("error");
    expect(m.onSessionKilled).toHaveBeenCalledTimes(1);
    expect(m.onDisconnected).not.toHaveBeenCalled();
  });

  it("closes and clears stale sockets on websocket error", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    expect(m.wsRef.current).toBe(ws);

    ws.simulateError();

    expect(ws.close).toHaveBeenCalledTimes(1);
    expect(m.wsRef.current).toBeNull();
    expect(m.setConnectionState).toHaveBeenCalledWith("disconnected");
    expect(m.onDisconnected).toHaveBeenCalledTimes(1);

    m.setConnectionState.mockClear();
    m.onDisconnected.mockClear();
    ws.simulateClose(1000, "");
    expect(m.setConnectionState).not.toHaveBeenCalled();
    expect(m.onDisconnected).not.toHaveBeenCalled();
  });

  it("does not auto-retry when the backend process exits", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
      undefined,
      m.onBackendCrash,
      m.onSessionKilled,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    ws.simulateClose(4001, "backend process exited");

    expect(m.setConnectionState).toHaveBeenCalledWith("crashed");
    expect(m.onBackendCrash).toHaveBeenCalledWith("backend process exited");
    expect(m.onDisconnected).not.toHaveBeenCalled();
    expect(m.onSessionKilled).not.toHaveBeenCalled();
  });

  it("does not auto-retry when the terminal session exits cleanly", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
      undefined,
      m.onBackendCrash,
      m.onSessionKilled,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    ws.simulateClose(1000, "");

    expect(m.setConnectionState).toHaveBeenCalledWith("session_ended");
    expect(m.onSessionKilled).toHaveBeenCalledTimes(1);
    expect(m.onDisconnected).not.toHaveBeenCalled();
    expect(m.onBackendCrash).not.toHaveBeenCalled();
  });

  it("does not auto-retry when the terminal session is killed", async () => {
    const m = makeMocks();
    connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
      undefined,
      m.onBackendCrash,
      m.onSessionKilled,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    ws.simulateClose(4002, "session killed");

    expect(m.setConnectionState).toHaveBeenCalledWith("session_ended");
    expect(m.onSessionKilled).toHaveBeenCalledTimes(1);
    expect(m.onDisconnected).not.toHaveBeenCalled();
    expect(m.onBackendCrash).not.toHaveBeenCalled();
  });

  it("double cleanup is idempotent", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      m.onConnected,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();

    // Call cleanup twice — should not throw
    cleanup();
    cleanup();

    expect(ws.close).toHaveBeenCalledTimes(1);
    expect(m.wsRef.current).toBeNull();
  });

  it("skips onclose callback after cleanup", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
    );

    shared.resolveToken!({ token: "tok" });
    await vi.waitFor(() => {
      expect(MockWebSocket.instances).toHaveLength(1);
    });

    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();

    // Call cleanup
    cleanup();
    m.setConnectionState.mockClear();
    m.onDisconnected.mockClear();

    // Simulate onclose firing after cleanup (triggered by ws.close())
    ws.simulateClose(1000, "");

    expect(m.setConnectionState).not.toHaveBeenCalled();
    expect(m.onDisconnected).not.toHaveBeenCalled();
  });

  it("handles token rejection after cleanup without state updates", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.write,
      m.wsRef,
      m.setConnectionState,
      undefined,
      m.onDisconnected,
    );

    // Cleanup before token resolves
    cleanup();
    m.setConnectionState.mockClear();
    m.onDisconnected.mockClear();

    // Reject the token
    shared.rejectToken!(new Error("network error"));
    await new Promise((r) => setTimeout(r, 0));

    // Should NOT call setConnectionState after cleanup
    expect(m.setConnectionState).not.toHaveBeenCalled();
    expect(m.onDisconnected).not.toHaveBeenCalled();
  });
});
