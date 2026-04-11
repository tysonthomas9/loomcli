/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalInstance component.
 */

import { render, screen, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";
import { createRef } from "react";

import {
  TerminalInstance,
  type TerminalInstanceHandle,
} from "../TerminalInstance";

// ── Shared mock state (vi.hoisted runs before vi.mock factories) ─────────────

const shared = vi.hoisted(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let _terminalInstance: any = null;
  let _webglOnContextLoss: (() => void) | null = null;
  let _webglShouldThrow = false;
  const _findNext = vi.fn(() => true);
  const _findPrevious = vi.fn(() => true);
  const _clearDecorations = vi.fn();
  const _webglDispose = vi.fn();

  return {
    get terminalInstance() {
      return _terminalInstance;
    },
    set terminalInstance(v) {
      _terminalInstance = v;
    },
    get webglOnContextLoss() {
      return _webglOnContextLoss;
    },
    set webglOnContextLoss(v) {
      _webglOnContextLoss = v;
    },
    get webglShouldThrow() {
      return _webglShouldThrow;
    },
    set webglShouldThrow(v) {
      _webglShouldThrow = v;
    },
    findNext: _findNext,
    findPrevious: _findPrevious,
    clearDecorations: _clearDecorations,
    webglDispose: _webglDispose,
  };
});

// ── xterm mocks ──────────────────────────────────────────────────────────────

vi.mock("@xterm/xterm", () => {
  class MockTerminal {
    open = vi.fn();
    dispose = vi.fn();
    onData = vi.fn(() => ({ dispose: vi.fn() }));
    onScroll = vi.fn(() => ({ dispose: vi.fn() }));
    onSelectionChange = vi.fn(() => ({ dispose: vi.fn() }));
    getSelection = vi.fn(() => "");
    attachCustomKeyEventHandler = vi.fn();
    paste = vi.fn();
    write = vi.fn();
    loadAddon = vi.fn();
    scrollToBottom = vi.fn();
    cols = 80;
    rows = 24;
    options: Record<string, unknown> = {};
    buffer = { active: { viewportY: 0, baseY: 0 } };
    constructor(opts?: Record<string, unknown>) {
      if (opts) {
        this.options = { ...opts };
      }
      shared.terminalInstance = this;
    }
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
    findNext = shared.findNext;
    findPrevious = shared.findPrevious;
    clearDecorations = shared.clearDecorations;
    onDidChangeResults = vi.fn(() => ({ dispose: vi.fn() }));
    dispose = vi.fn();
  }
  return { SearchAddon: MockSearchAddon };
});

vi.mock("@xterm/addon-webgl", () => {
  class MockWebglAddon {
    dispose = shared.webglDispose;
    constructor() {
      if (shared.webglShouldThrow) {
        throw new Error("WebGL not available");
      }
    }
    onContextLoss(cb: () => void) {
      shared.webglOnContextLoss = cb;
    }
  }
  return { WebglAddon: MockWebglAddon };
});

vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

// ── API mocks ────────────────────────────────────────────────────────────────

vi.mock("@/api/client", () => ({
  get: vi.fn(() => Promise.resolve({ token: "test-token" })),
  // Mirror the real wsUrl helper so terminalConnection.ts builds the same
  // workspace-scoped path it would in production.
  wsUrl: (workspaceId: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`,
}));

vi.mock("@/utils/reconnectBackoff", () => ({
  startAutoReconnect: vi.fn(() => vi.fn()),
  DEFAULT_RECONNECT_CONFIG: {
    baseDelay: 1000,
    maxDelay: 30000,
    maxAttempts: 10,
    jitterFactor: 0.5,
  },
}));

// ── WebSocket & ResizeObserver mocks ─────────────────────────────────────────

let lastMockWs: MockWebSocket | null = null;

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
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    lastMockWs = this;
    setTimeout(() => {
      this.readyState = MockWebSocket.OPEN;
      this.onopen?.();
    }, 0);
  }
}

class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}

const OriginalWebSocket = globalThis.WebSocket;
const OriginalResizeObserver = globalThis.ResizeObserver;

type GlobalWithMocks = typeof globalThis & {
  WebSocket: typeof MockWebSocket | typeof WebSocket;
  ResizeObserver: typeof MockResizeObserver | typeof ResizeObserver;
};

beforeEach(() => {
  (globalThis as GlobalWithMocks).WebSocket = MockWebSocket as never;
  (globalThis as GlobalWithMocks).ResizeObserver = MockResizeObserver as never;
  lastMockWs = null;
  shared.webglOnContextLoss = null;
  shared.webglShouldThrow = false;
  shared.webglDispose.mockClear();
  shared.findNext.mockClear();
  shared.findPrevious.mockClear();
  shared.clearDecorations.mockClear();
});

afterEach(() => {
  (globalThis as GlobalWithMocks).WebSocket = OriginalWebSocket;
  (globalThis as GlobalWithMocks).ResizeObserver = OriginalResizeObserver;
});

// ── Tests ────────────────────────────────────────────────────────────────────

describe("TerminalInstance", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders terminal container with data-testid", () => {
    render(<TerminalInstance sessionName="test-session" isActive={true} />);
    expect(screen.getByTestId("terminal-instance")).toBeInTheDocument();
  });

  it("creates Terminal with correct font options", () => {
    render(
      <TerminalInstance
        sessionName="test-session"
        isActive={true}
        fontFamily="Fira Code"
        fontSize={16}
      />,
    );

    expect(shared.terminalInstance).toBeDefined();
    expect(shared.terminalInstance.options.fontFamily).toBe("Fira Code");
    expect(shared.terminalInstance.options.fontSize).toBe(16);
  });

  it("uses default font props when not specified", () => {
    render(<TerminalInstance sessionName="test-session" isActive={true} />);

    expect(shared.terminalInstance.options.fontFamily).toBe(
      'Menlo, Monaco, "Courier New", monospace',
    );
    expect(shared.terminalInstance.options.fontSize).toBe(14);
  });

  it("loads all addons (FitAddon, WebLinksAddon, SearchAddon, WebglAddon)", () => {
    render(<TerminalInstance sessionName="test-session" isActive={true} />);

    expect(shared.terminalInstance.loadAddon).toHaveBeenCalledTimes(4);
  });

  it("handles WebGL context loss by disposing addon", () => {
    render(<TerminalInstance sessionName="test-session" isActive={true} />);

    expect(shared.webglOnContextLoss).not.toBeNull();

    act(() => {
      shared.webglOnContextLoss?.();
    });

    expect(shared.webglDispose).toHaveBeenCalled();
  });

  it("falls back gracefully when WebGL constructor throws", () => {
    shared.webglShouldThrow = true;

    render(<TerminalInstance sessionName="test-session" isActive={true} />);

    // Only 3 addons loaded (no WebGL)
    expect(shared.terminalInstance.loadAddon).toHaveBeenCalledTimes(3);
    expect(screen.getByTestId("terminal-instance")).toBeInTheDocument();
  });

  it("connects WebSocket with session name in URL", async () => {
    render(<TerminalInstance sessionName="my-terminal" isActive={true} />);

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    expect(lastMockWs).not.toBeNull();
    expect(lastMockWs!.url).toContain("session=my-terminal");
    expect(lastMockWs!.url).toContain("token=test-token");
  });

  it("calls onConnectionStateChange with state changes", async () => {
    const onStateChange = vi.fn();
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

    expect(onStateChange).toHaveBeenCalledWith("connecting", false);
    expect(onStateChange).toHaveBeenCalledWith("connected", true);
  });

  it("calls fitAddon.fit() via requestAnimationFrame when isActive becomes true", () => {
    const { rerender } = render(
      <TerminalInstance sessionName="test-session" isActive={false} />,
    );

    rerender(<TerminalInstance sessionName="test-session" isActive={true} />);

    act(() => {
      vi.runAllTimers();
    });

    expect(screen.getByTestId("terminal-instance")).toBeInTheDocument();
  });

  it("exposes search() via imperative handle", async () => {
    const ref = createRef<TerminalInstanceHandle>();
    render(
      <TerminalInstance ref={ref} sessionName="test-session" isActive={true} />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    expect(ref.current).not.toBeNull();
    ref.current!.search("hello", { caseSensitive: true });

    expect(shared.findNext).toHaveBeenCalledWith(
      "hello",
      expect.objectContaining({ caseSensitive: true }),
    );
  });

  it("exposes findNext() and findPrevious() via imperative handle", async () => {
    const ref = createRef<TerminalInstanceHandle>();
    render(
      <TerminalInstance ref={ref} sessionName="test-session" isActive={true} />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    // Search with a term first so findNext/findPrevious have something to search for
    ref.current!.search("test");
    shared.findNext.mockClear();
    shared.findPrevious.mockClear();

    ref.current!.findNext();
    expect(shared.findNext).toHaveBeenCalledWith(
      "test",
      expect.objectContaining({}),
    );

    ref.current!.findPrevious();
    expect(shared.findPrevious).toHaveBeenCalledWith(
      "test",
      expect.objectContaining({}),
    );
  });

  it("exposes clearSearch() via imperative handle", async () => {
    const ref = createRef<TerminalInstanceHandle>();
    render(
      <TerminalInstance ref={ref} sessionName="test-session" isActive={true} />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    ref.current!.clearSearch();
    expect(shared.clearDecorations).toHaveBeenCalled();
  });

  it("cleans up terminal, WebSocket, and observer on unmount", async () => {
    const { unmount } = render(
      <TerminalInstance sessionName="test-session" isActive={true} />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    const ws = lastMockWs;
    const terminal = shared.terminalInstance;

    unmount();

    expect(terminal.dispose).toHaveBeenCalled();
    expect(ws?.close).toHaveBeenCalled();
  });

  it("updates font options dynamically without recreating terminal", async () => {
    const { rerender } = render(
      <TerminalInstance
        sessionName="test-session"
        isActive={true}
        fontSize={14}
      />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    const termBefore = shared.terminalInstance;

    rerender(
      <TerminalInstance
        sessionName="test-session"
        isActive={true}
        fontSize={18}
      />,
    );

    expect(shared.terminalInstance).toBe(termBefore);
    expect(shared.terminalInstance.options.fontSize).toBe(18);
  });

  describe("auth-sign-out cleanup", () => {
    it("auth-sign-out closes WebSocket", async () => {
      render(<TerminalInstance sessionName="test-session" isActive={true} />);

      await act(async () => {
        await vi.runAllTimersAsync();
      });

      const ws = lastMockWs;
      expect(ws).not.toBeNull();

      // Dispatch auth-sign-out event
      act(() => {
        window.dispatchEvent(new Event("auth-sign-out"));
      });

      // WebSocket should be closed
      expect(ws!.close).toHaveBeenCalled();
    });

    it("auth-sign-out listener is cleaned up on unmount", async () => {
      const { unmount } = render(
        <TerminalInstance sessionName="test-session" isActive={true} />,
      );

      await act(async () => {
        await vi.runAllTimersAsync();
      });

      const firstWs = lastMockWs;
      expect(firstWs).not.toBeNull();

      // Unmount to trigger cleanup (removes auth-sign-out listener)
      unmount();

      // Reset close mock call count from unmount cleanup
      firstWs!.close.mockClear();

      // Dispatch auth-sign-out after unmount — the listener should have been removed
      act(() => {
        window.dispatchEvent(new Event("auth-sign-out"));
      });

      // The old WebSocket's close should NOT be called again since the listener was removed
      expect(firstWs!.close).not.toHaveBeenCalled();
    });
  });
});
