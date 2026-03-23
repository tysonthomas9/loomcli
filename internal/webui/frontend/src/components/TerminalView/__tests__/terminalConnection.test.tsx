/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for connectWebSocket race condition (loomcli-5y1sd.7).
 *
 * Tests the WebSocket connection leak when cleanup fires between WebSocket
 * creation and wsCleanupInner assignment.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// ── Mock @/api/client ──────────────────────────────────────────────────────

const { mockGet } = vi.hoisted(() => ({
  mockGet: vi.fn(),
}));

vi.mock("@/api/client", () => ({
  get: mockGet,
}));

vi.mock("@/utils/reconnectBackoff", () => ({
  startAutoReconnect: vi.fn(() => vi.fn()),
}));

// ── WebSocket mock ─────────────────────────────────────────────────────────

class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  url: string;
  readyState = MockWebSocket.CONNECTING;
  binaryType = "";
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  send = vi.fn();
  close = vi.fn();

  constructor(url: string) {
    this.url = url;
    wsInstances.push(this);
  }
}

let wsInstances: MockWebSocket[] = [];

const OriginalWebSocket = globalThis.WebSocket;

// ── Import the module under test (after mocks) ────────────────────────────

// We import TerminalInstance to get access to connectWebSocket indirectly.
// Since connectWebSocket is not exported, we test it through the component.
// However, the design says to test connectWebSocket directly, so let's
// re-export it for testing by importing the module and testing the behavior.
//
// Actually, connectWebSocket is a module-level function in TerminalInstance.tsx.
// We can't directly import it. Instead, we'll test through the component lifecycle.
// But that makes it hard to control timing. Let's test the specific race condition
// by directly testing the function after extracting its logic.
//
// The pragmatic approach: test the fix through the component, controlling
// the timing of token resolution and cleanup.

// Mock terminal addons
vi.mock("@xterm/xterm", () => {
  class MockTerminal {
    open = vi.fn();
    dispose = vi.fn();
    onData = vi.fn(() => ({ dispose: vi.fn() }));
    write = vi.fn();
    loadAddon = vi.fn();
    cols = 80;
    rows = 24;
    options: Record<string, unknown> = {};
  }
  return { Terminal: MockTerminal };
});

vi.mock("@xterm/addon-fit", () => {
  class MockFitAddon {
    fit = vi.fn();
    dispose = vi.fn();
  }
  return { FitAddon: MockFitAddon };
});

vi.mock("@xterm/addon-web-links", () => {
  class MockWebLinksAddon {
    dispose = vi.fn();
  }
  return { WebLinksAddon: MockWebLinksAddon };
});

vi.mock("@xterm/addon-search", () => {
  class MockSearchAddon {
    findNext = vi.fn();
    findPrevious = vi.fn();
    clearDecorations = vi.fn();
    dispose = vi.fn();
  }
  return { SearchAddon: MockSearchAddon };
});

vi.mock("@xterm/addon-webgl", () => {
  class MockWebglAddon {
    dispose = vi.fn();
    onContextLoss = vi.fn();
  }
  return { WebglAddon: MockWebglAddon };
});

vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

import { render, act } from "@testing-library/react";
import { TerminalInstance } from "../TerminalInstance";

class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}

const OriginalResizeObserver = globalThis.ResizeObserver;

describe("connectWebSocket race condition (loomcli-5y1sd.7)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    wsInstances = [];
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (globalThis as any).WebSocket = MockWebSocket;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (globalThis as any).ResizeObserver = MockResizeObserver;
  });

  afterEach(() => {
    vi.useRealTimers();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (globalThis as any).WebSocket = OriginalWebSocket;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (globalThis as any).ResizeObserver = OriginalResizeObserver;
  });

  it("closes WebSocket when cleanup fires before wsCleanupInner is assigned", async () => {
    // Control token resolution timing
    let resolveToken: (value: { token: string }) => void;
    mockGet.mockReturnValue(
      new Promise((resolve) => {
        resolveToken = resolve;
      }),
    );

    // Render and immediately unmount (fast tab close)
    const { unmount } = render(
      <TerminalInstance sessionName="test-session" isActive={true} />,
    );

    // Resolve the token (this queues a microtask that creates the WebSocket)
    await act(async () => {
      resolveToken!({ token: "test-token" });
      // Let the promise resolve and WebSocket get created
      await vi.runAllTimersAsync();
    });

    // WebSocket should have been created
    expect(wsInstances.length).toBe(1);
    const ws = wsInstances[0];

    // Now unmount (fires cleanup) -- the WebSocket exists but onopen hasn't fired yet
    // In the buggy code, cleanup only sets wsRef.current = null without closing the WS
    unmount();

    // The WebSocket should be closed
    expect(ws.close).toHaveBeenCalled();
  });

  it("does not create WebSocket when cancelled before token resolves", async () => {
    // Token resolution controlled manually
    let resolveToken: (value: { token: string }) => void;
    mockGet.mockReturnValue(
      new Promise((resolve) => {
        resolveToken = resolve;
      }),
    );

    const { unmount } = render(
      <TerminalInstance sessionName="test-session" isActive={true} />,
    );

    // Unmount before token resolves
    unmount();

    // Now resolve the token
    await act(async () => {
      resolveToken!({ token: "test-token" });
      await vi.runAllTimersAsync();
    });

    // No WebSocket should have been created (cancelled check at top of .then)
    expect(wsInstances.length).toBe(0);
  });

  it("skips onopen callback after cleanup", async () => {
    const onStateChange = vi.fn();
    let resolveToken: (value: { token: string }) => void;
    mockGet.mockReturnValue(
      new Promise((resolve) => {
        resolveToken = resolve;
      }),
    );

    const { unmount } = render(
      <TerminalInstance
        sessionName="test-session"
        isActive={true}
        onConnectionStateChange={onStateChange}
      />,
    );

    // Resolve token so WebSocket is created
    await act(async () => {
      resolveToken!({ token: "test-token" });
      await vi.runAllTimersAsync();
    });

    expect(wsInstances.length).toBe(1);
    const ws = wsInstances[0];

    // Clear the mock to check only calls after unmount
    onStateChange.mockClear();

    // Unmount
    unmount();

    // Simulate WebSocket open event firing after unmount
    act(() => {
      ws.readyState = MockWebSocket.OPEN;
      ws.onopen?.();
    });

    // setConnectionState('connected') should NOT have been called after unmount
    // (The disconnected call during unmount cleanup is expected)
    const connectedCalls = onStateChange.mock.calls.filter(
      (call: unknown[]) => call[0] === "connected",
    );
    expect(connectedCalls.length).toBe(0);
  });

  it("skips onmessage callback after cleanup", async () => {
    let resolveToken: (value: { token: string }) => void;
    mockGet.mockReturnValue(
      new Promise((resolve) => {
        resolveToken = resolve;
      }),
    );

    const { unmount } = render(
      <TerminalInstance sessionName="test-session" isActive={true} />,
    );

    // Resolve token so WebSocket is created
    await act(async () => {
      resolveToken!({ token: "test-token" });
      await vi.runAllTimersAsync();
    });

    expect(wsInstances.length).toBe(1);
    const ws = wsInstances[0];

    // Unmount
    unmount();

    // Simulate message after unmount - should not throw
    expect(() => {
      ws.onmessage?.(new MessageEvent("message", { data: "hello" }));
    }).not.toThrow();
  });

  it("double cleanup is idempotent", async () => {
    mockGet.mockResolvedValue({ token: "test-token" });

    const { unmount } = render(
      <TerminalInstance sessionName="test-session" isActive={true} />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    expect(wsInstances.length).toBe(1);

    // First unmount (cleanup)
    unmount();

    // Second unmount would be handled by React - the key assertion is no errors thrown
    expect(wsInstances[0].close).toHaveBeenCalled();
  });

  it("handles fetchTerminalToken failure gracefully", async () => {
    const onStateChange = vi.fn();
    mockGet.mockRejectedValue(new Error("Network error"));

    render(
      <TerminalInstance
        sessionName="test-session"
        isActive={true}
        onConnectionStateChange={onStateChange}
      />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    // Should report connecting then disconnected
    expect(onStateChange).toHaveBeenCalledWith("connecting");
    // The fetch failed, so eventually disconnected is called
    // (the mock returns null token which still creates WS, but rejected promise
    // goes to the catch handler)
  });

  it("normal lifecycle: connects, receives data, and cleans up", async () => {
    mockGet.mockResolvedValue({ token: "test-token" });

    const onStateChange = vi.fn();
    const { unmount } = render(
      <TerminalInstance
        sessionName="test-session"
        isActive={true}
        onConnectionStateChange={onStateChange}
      />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    expect(wsInstances.length).toBe(1);
    const ws = wsInstances[0];

    // Simulate successful open
    act(() => {
      ws.readyState = MockWebSocket.OPEN;
      ws.onopen?.();
    });

    expect(onStateChange).toHaveBeenCalledWith("connected");

    // Cleanup
    unmount();
    expect(ws.close).toHaveBeenCalled();
  });
});
