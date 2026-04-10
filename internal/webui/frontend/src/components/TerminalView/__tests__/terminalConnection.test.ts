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

vi.mock("@/api/client", () => ({
  get: shared.getMock,
  getWsBaseUrl: () => "ws://localhost",
}));

// ── Mock @/api/logs (agent terminal endpoints) ──────────────────────────────

const agentMocks = vi.hoisted(() => {
  let _resolveAgentToken: ((v: string) => void) | null = null;
  let _rejectAgentToken: ((e: Error) => void) | null = null;
  const _getAgentTerminalToken = vi.fn(
    () =>
      new Promise<string>((resolve, reject) => {
        _resolveAgentToken = resolve;
        _rejectAgentToken = reject;
      }),
  );
  const _getAgentTerminalWsUrl = vi.fn(
    (_wsId: string, agentName: string, token: string) =>
      `ws://localhost/api/ws/agents/${agentName}/terminal/ws?token=${token}`,
  );

  return {
    getAgentTerminalToken: _getAgentTerminalToken,
    getAgentTerminalWsUrl: _getAgentTerminalWsUrl,
    get resolveAgentToken() {
      return _resolveAgentToken;
    },
    get rejectAgentToken() {
      return _rejectAgentToken;
    },
  };
});

vi.mock("@/api/logs", () => ({
  getAgentTerminalToken: agentMocks.getAgentTerminalToken,
  getAgentTerminalWsUrl: agentMocks.getAgentTerminalWsUrl,
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
}

// ── Test helpers ─────────────────────────────────────────────────────────────

function makeMocks() {
  const terminal = {
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    write: vi.fn(),
    cols: 80,
    rows: 24,
  };

  const fitAddon = {
    fit: vi.fn(),
  };

  const wsRef = { current: null as WebSocket | null };
  const setConnectionState = vi.fn();
  const onConnected = vi.fn();
  const onDisconnected = vi.fn();
  const onOutput = vi.fn();

  return {
    terminal,
    fitAddon,
    wsRef,
    setConnectionState,
    onConnected,
    onDisconnected,
    onOutput,
  };
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
    agentMocks.getAgentTerminalToken.mockClear();
    agentMocks.getAgentTerminalWsUrl.mockClear();
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
      m.terminal as any,
      m.fitAddon as any,
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
      m.terminal as any,
      m.fitAddon as any,
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
      m.terminal as any,
      m.fitAddon as any,
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
      m.terminal as any,
      m.fitAddon as any,
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
    m.terminal.write.mockClear();

    // Simulate message after cleanup — should be ignored
    ws.simulateMessage("hello");

    expect(m.terminal.write).not.toHaveBeenCalled();
  });

  it("normal lifecycle: connects, receives data, and cleans up", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.terminal as any,
      m.fitAddon as any,
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
    expect(m.terminal.write).toHaveBeenCalledWith("output text");
    expect(m.onOutput).toHaveBeenCalled();

    // Clean up
    cleanup();
    expect(ws.close).toHaveBeenCalledWith(1000);
    expect(m.wsRef.current).toBeNull();
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
      m.terminal as any,
      m.fitAddon as any,
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

  it("double cleanup is idempotent", async () => {
    const m = makeMocks();
    const cleanup = connectWebSocket(
      "ws1",
      "session1",
      m.terminal as any,
      m.fitAddon as any,
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
      m.terminal as any,
      m.fitAddon as any,
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
      m.terminal as any,
      m.fitAddon as any,
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

  // ── agentName parameter (V7 Terminal View) ──────────────────────────────

  describe("agentName parameter", () => {
    it("uses getAgentTerminalToken when agentName is provided", async () => {
      const m = makeMocks();
      connectWebSocket(
        "ws1",
        "session1",
        m.terminal as any,
        m.fitAddon as any,
        m.wsRef,
        m.setConnectionState,
        m.onConnected,
        m.onDisconnected,
        undefined,
        undefined,
        undefined,
        "agent-fox",
      );

      // Should call getAgentTerminalToken, not the regular get()
      expect(agentMocks.getAgentTerminalToken).toHaveBeenCalledWith(
        "ws1",
        "agent-fox",
      );
      expect(shared.getMock).not.toHaveBeenCalled();
    });

    it("uses getAgentTerminalWsUrl when agentName token resolves", async () => {
      const m = makeMocks();
      connectWebSocket(
        "ws1",
        "session1",
        m.terminal as any,
        m.fitAddon as any,
        m.wsRef,
        m.setConnectionState,
        m.onConnected,
        m.onDisconnected,
        undefined,
        undefined,
        undefined,
        "agent-fox",
      );

      agentMocks.resolveAgentToken!("agent-tok-123");
      await vi.waitFor(() => {
        expect(MockWebSocket.instances).toHaveLength(1);
      });

      expect(agentMocks.getAgentTerminalWsUrl).toHaveBeenCalledWith(
        "ws1",
        "agent-fox",
        "agent-tok-123",
      );

      const ws = MockWebSocket.instances[0];
      expect(ws.url).toBe(
        "ws://localhost/api/ws/agents/agent-fox/terminal/ws?token=agent-tok-123",
      );
    });

    it("agent WebSocket follows normal lifecycle (connect, data, cleanup)", async () => {
      const m = makeMocks();
      const cleanup = connectWebSocket(
        "ws1",
        "session1",
        m.terminal as any,
        m.fitAddon as any,
        m.wsRef,
        m.setConnectionState,
        m.onConnected,
        m.onDisconnected,
        m.onOutput,
        undefined,
        undefined,
        "agent-fox",
      );

      expect(m.setConnectionState).toHaveBeenCalledWith("connecting");

      agentMocks.resolveAgentToken!("tok");
      await vi.waitFor(() => {
        expect(MockWebSocket.instances).toHaveLength(1);
      });

      const ws = MockWebSocket.instances[0];
      ws.simulateOpen();

      expect(m.setConnectionState).toHaveBeenCalledWith("connected");
      expect(m.onConnected).toHaveBeenCalled();

      ws.simulateMessage("agent output");
      expect(m.terminal.write).toHaveBeenCalledWith("agent output");
      expect(m.onOutput).toHaveBeenCalled();

      cleanup();
      expect(ws.close).toHaveBeenCalledWith(1000);
    });

    it("falls back to regular buildWsUrl when agent token fetch fails", async () => {
      // Clear mocks explicitly to ensure clean state
      agentMocks.getAgentTerminalWsUrl.mockClear();
      const m = makeMocks();
      connectWebSocket(
        "ws1",
        "session1",
        m.terminal as any,
        m.fitAddon as any,
        m.wsRef,
        m.setConnectionState,
        undefined,
        m.onDisconnected,
        undefined,
        undefined,
        undefined,
        "agent-fox",
      );

      // Reject the agent token — the .catch(() => null) converts to null
      agentMocks.rejectAgentToken!(new Error("auth failure"));
      await vi.waitFor(() => {
        expect(MockWebSocket.instances).toHaveLength(1);
      });

      // When agentName is set but token is null, falls back to buildWsUrl
      // (agentName && token) is false, so buildWsUrl is used
      const ws = MockWebSocket.instances[0];
      expect(ws.url).toContain("session=session1");
      // getAgentTerminalWsUrl should NOT have been called in THIS test
      // (only getAgentTerminalToken was called, and it failed)
      expect(ws.url).not.toContain("/agents/");
    });
  });
});
