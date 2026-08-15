/**
 * @vitest-environment jsdom
 */

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const connectionState = vi.hoisted(() => ({
  writeCallbacks: [] as Array<(data: string | Uint8Array) => void>,
  cleanupCount: 0,
  fitCountsAtConnect: [] as number[],
  terminalSizesAtConnect: [] as Array<{ cols: number; rows: number }>,
  // Opt-in fake socket: tests that need the "input actually reached the PTY"
  // path set this before rendering. Left null, the connect mock behaves
  // exactly as it did before, so no other test sees a live socket.
  socket: null as { readyState: number; send: (data: unknown) => void } | null,
}));

const xtermState = vi.hoisted(() => {
  const state = {
    fitCount: 0,
    onReady: null as null | ((handle: unknown) => void),
    onResize: null as null | ((cols: number, rows: number) => void),
    handle: null as unknown as {
      write: ReturnType<typeof vi.fn>;
      focus: ReturnType<typeof vi.fn>;
      fit: () => { cols: number; rows: number };
      scrollToBottom: ReturnType<typeof vi.fn>;
      probeActivity: () => { cursorAtLineStart: boolean; altScreen: boolean };
    },
    onData: null as null | ((data: string) => void),
  };
  state.handle = {
    write: vi.fn(),
    focus: vi.fn(),
    fit: () => {
      state.fitCount += 1;
      return { cols: 132, rows: 43 };
    },
    scrollToBottom: vi.fn(),
    probeActivity: () => ({ cursorAtLineStart: false, altScreen: false }),
  };
  return state;
});

vi.mock("../XTermRenderer", async () => {
  const React = await import("react");

  function XTermRenderer(props: {
    onReady: (handle: unknown) => void;
    onDispose: (handle: unknown) => void;
    onResize: (cols: number, rows: number) => void;
    onData: (data: string) => void;
  }) {
    xtermState.onReady = props.onReady;
    xtermState.onResize = props.onResize;
    xtermState.onData = props.onData;
    React.useEffect(
      () => () => {
        props.onDispose(xtermState.handle);
      },
      [props.onDispose],
    );
    return React.createElement("div", { "data-testid": "mock-xterm" });
  }

  return { XTermRenderer };
});

vi.mock("../terminalConnection", () => ({
  connectWebSocket: vi.fn(
    (
      _workspaceId: string,
      _sessionName: string,
      write: (data: string | Uint8Array) => void,
      wsRef: { current: WebSocket | null },
      setConnState: (state: string) => void,
      _onConnected: () => void,
      _onDisconnected: () => void,
      _onOutput: () => void,
      _onBackendCrash: (reason: string) => void,
      _onSessionKilled: () => void,
      terminalSize: { cols: number; rows: number },
    ): (() => void) => {
      if (connectionState.socket) {
        wsRef.current = connectionState.socket as unknown as WebSocket;
      }
      connectionState.writeCallbacks.push(write);
      connectionState.fitCountsAtConnect.push(xtermState.fitCount);
      connectionState.terminalSizesAtConnect.push(terminalSize);
      setConnState("connecting");
      return () => {
        connectionState.cleanupCount += 1;
      };
    },
  ),
  encodeResize: (cols: number, rows: number) => `\x1b[RESIZE:${cols};${rows}]`,
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws" }),
}));

vi.mock("@/hooks/api", () => ({
  getTerminalConfig: vi.fn().mockResolvedValue({ gracePeriodMs: 0 }),
}));

import { TerminalInstance } from "../TerminalInstance";

async function flushPendingWork(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function readyRenderer(): void {
  act(() => {
    xtermState.onReady?.(xtermState.handle);
  });
}

function latestWriteCallback(): (data: string | Uint8Array) => void {
  const callback = connectionState.writeCallbacks.at(-1);
  if (!callback) throw new Error("no write callback captured");
  return callback;
}

describe("TerminalInstance", () => {
  beforeEach(() => {
    connectionState.writeCallbacks.length = 0;
    connectionState.cleanupCount = 0;
    connectionState.fitCountsAtConnect.length = 0;
    connectionState.terminalSizesAtConnect.length = 0;
    connectionState.socket = null;
    xtermState.fitCount = 0;
    xtermState.onReady = null;
    xtermState.onResize = null;
    xtermState.onData = null;
    xtermState.handle.write.mockClear();
    xtermState.handle.focus.mockClear();
    xtermState.handle.scrollToBottom.mockClear();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it.each(["claude", "codex", "gemini", "cursor", "shell"])(
    "uses xterm for %s terminal metadata",
    async (backendName) => {
      const view = render(
        <TerminalInstance
          sessionName={`${backendName}-alpha`}
          backendName={backendName}
          isActive
        />,
      );
      await waitFor(() => expect(xtermState.onReady).not.toBeNull());

      expect(
        view
          .getByTestId("terminal-wrapper")
          .getAttribute("data-terminal-renderer"),
      ).toBe("xterm");
      expect(view.queryByTestId("mock-xterm")).not.toBeNull();
    },
  );

  it("fits the visible pane before opening its first WebSocket", async () => {
    render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );
    await flushPendingWork();
    expect(connectionState.writeCallbacks).toHaveLength(0);

    readyRenderer();

    await waitFor(() => {
      expect(connectionState.writeCallbacks).toHaveLength(1);
    });
    expect(connectionState.fitCountsAtConnect).toEqual([1]);
    expect(connectionState.terminalSizesAtConnect).toEqual([
      { cols: 132, rows: 43 },
    ]);
  });

  it("delivers WebSocket output to xterm", async () => {
    render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();
    await waitFor(() => {
      expect(connectionState.writeCallbacks).toHaveLength(1);
    });

    act(() => latestWriteCallback()("terminal-output"));

    expect(xtermState.handle.write).toHaveBeenCalledWith("terminal-output");
  });

  it("does not persist an inactive renderer's sentinel resize", async () => {
    const { rerender } = render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive={false}
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();

    act(() => xtermState.onResize?.(1, 1));
    rerender(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );

    await waitFor(() => {
      expect(connectionState.writeCallbacks).toHaveLength(1);
    });
    expect(connectionState.terminalSizesAtConnect).toEqual([
      { cols: 132, rows: 43 },
    ]);
  });

  it("does not force a scrolled-up viewport to the bottom on reactivation", async () => {
    const { rerender } = render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();
    await waitFor(() => expect(xtermState.fitCount).toBeGreaterThan(1));
    xtermState.handle.scrollToBottom.mockClear();

    rerender(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive={false}
      />,
    );
    await flushPendingWork();
    const beforeReactivation = xtermState.fitCount;
    rerender(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );

    await waitFor(() => {
      expect(xtermState.fitCount).toBeGreaterThan(beforeReactivation);
    });
    expect(xtermState.handle.scrollToBottom).not.toHaveBeenCalled();
  });

  it("reconnects after a ptyAlive lifecycle transition", async () => {
    const { rerender } = render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();
    await waitFor(() => {
      expect(connectionState.writeCallbacks).toHaveLength(1);
    });

    rerender(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
        ptyAlive
      />,
    );
    await flushPendingWork();

    expect(connectionState.cleanupCount).toBeGreaterThanOrEqual(1);
    expect(connectionState.writeCallbacks.length).toBeGreaterThanOrEqual(2);
  });

  describe("onInput", () => {
    async function renderConnected(props: { writable?: boolean } = {}) {
      const onInput = vi.fn();
      render(
        <TerminalInstance
          sessionName="codex-alpha"
          backendName="codex"
          isActive
          onInput={onInput}
          {...props}
        />,
      );
      await waitFor(() => expect(xtermState.onReady).not.toBeNull());
      readyRenderer();
      await waitFor(() => {
        expect(connectionState.writeCallbacks).toHaveLength(1);
      });
      return onInput;
    }

    it("fires when a keystroke reaches an open socket", async () => {
      connectionState.socket = { readyState: WebSocket.OPEN, send: vi.fn() };
      const onInput = await renderConnected();

      act(() => xtermState.onData?.("y"));

      expect(onInput).toHaveBeenCalledTimes(1);
      expect(connectionState.socket.send).toHaveBeenCalledWith("y");
    });

    it("does not fire for a read-only tab whose input is dropped", async () => {
      connectionState.socket = { readyState: WebSocket.OPEN, send: vi.fn() };
      const onInput = await renderConnected({ writable: false });

      act(() => xtermState.onData?.("y"));

      expect(onInput).not.toHaveBeenCalled();
      expect(connectionState.socket.send).not.toHaveBeenCalled();
    });

    it("does not fire when the socket is not open", async () => {
      connectionState.socket = { readyState: WebSocket.CLOSED, send: vi.fn() };
      const onInput = await renderConnected();

      act(() => xtermState.onData?.("y"));

      expect(onInput).not.toHaveBeenCalled();
    });
  });

  describe("probeActivity", () => {
    it("returns null before the renderer is ready and the probe after", async () => {
      const ref = { current: null } as {
        current: { probeActivity: () => unknown } | null;
      };
      render(
        <TerminalInstance
          ref={ref as never}
          sessionName="codex-alpha"
          backendName="codex"
          isActive
        />,
      );
      await waitFor(() => expect(ref.current).not.toBeNull());

      expect(ref.current?.probeActivity()).toBeNull();

      readyRenderer();

      expect(ref.current?.probeActivity()).toEqual({
        cursorAtLineStart: false,
        altScreen: false,
      });
    });
  });
});
