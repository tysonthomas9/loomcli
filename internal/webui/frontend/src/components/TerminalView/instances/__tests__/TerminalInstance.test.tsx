/**
 * @vitest-environment jsdom
 */

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const connectionState = vi.hoisted(() => ({
  writeCallbacks: [] as Array<(data: string | Uint8Array) => void>,
  callbacks: [] as Array<{
    reset: () => void;
    setConnectionState: (state: string) => void;
    onDisconnected?: (policy: "backoff" | "immediate") => void;
    onInitialState?: (state: {
      cols: number;
      rows: number;
      retainedLines: number;
    }) => void;
    onCanonicalResize?: (cols: number, rows: number) => void;
  }>,
  handles: [] as Array<{
    dispose: ReturnType<typeof vi.fn>;
    sendInput: ReturnType<typeof vi.fn>;
    sendResizeRequest: ReturnType<typeof vi.fn>;
    sendFocus: ReturnType<typeof vi.fn>;
  }>,
  cleanupCount: 0,
  fitCountsAtConnect: [] as number[],
  terminalSizesAtConnect: [] as Array<{ cols: number; rows: number }>,
}));

const xtermState = vi.hoisted(() => {
  const state = {
    fitCount: 0,
    onReady: null as null | ((handle: unknown) => void),
    onResize: null as null | ((cols: number, rows: number) => void),
    onData: null as null | ((data: string) => void),
    onBinary: null as null | ((data: Uint8Array) => void),
    onFocus: null as null | (() => void),
    handle: null as unknown as {
      write: ReturnType<typeof vi.fn>;
      reset: ReturnType<typeof vi.fn>;
      setSize: ReturnType<typeof vi.fn>;
      focus: ReturnType<typeof vi.fn>;
      fit: () => { cols: number; rows: number };
      scrollToBottom: ReturnType<typeof vi.fn>;
    },
  };
  state.handle = {
    write: vi.fn(),
    reset: vi.fn(),
    setSize: vi.fn(),
    focus: vi.fn(),
    fit: () => {
      state.fitCount += 1;
      return { cols: 132, rows: 43 };
    },
    scrollToBottom: vi.fn(),
  };
  return state;
});

vi.mock("../XTermRenderer", async () => {
  const React = await import("react");

  function XTermRenderer(props: {
    onReady: (handle: unknown) => void;
    onDispose: (handle: unknown) => void;
    onData: (data: string) => void;
    onBinary: (data: Uint8Array) => void;
    onResize: (cols: number, rows: number) => void;
    onFocus: () => void;
  }) {
    const { onDispose } = props;
    xtermState.onReady = props.onReady;
    xtermState.onResize = props.onResize;
    xtermState.onData = props.onData;
    xtermState.onBinary = props.onBinary;
    xtermState.onFocus = props.onFocus;
    React.useEffect(
      () => () => {
        onDispose(xtermState.handle);
      },
      [onDispose],
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
      _wsRef: { current: WebSocket | null },
      callbacks: {
        write: (data: string | Uint8Array) => void;
        reset: () => void;
        setConnectionState: (state: string) => void;
        onDisconnected?: (policy: "backoff" | "immediate") => void;
        onInitialState?: (state: {
          cols: number;
          rows: number;
          retainedLines: number;
        }) => void;
        onCanonicalResize?: (cols: number, rows: number) => void;
      },
      terminalSize: { cols: number; rows: number },
    ) => {
      connectionState.writeCallbacks.push(callbacks.write);
      connectionState.callbacks.push(callbacks);
      connectionState.fitCountsAtConnect.push(xtermState.fitCount);
      connectionState.terminalSizesAtConnect.push(terminalSize);
      callbacks.setConnectionState("connecting");
      const handle = {
        dispose: vi.fn(() => {
          connectionState.cleanupCount += 1;
        }),
        sendInput: vi.fn(),
        sendResizeRequest: vi.fn(),
        sendFocus: vi.fn(),
      };
      connectionState.handles.push(handle);
      return handle;
    },
  ),
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

function latestConnectionHandle() {
  const handle = connectionState.handles.at(-1);
  if (!handle) throw new Error("no connection handle captured");
  return handle;
}

describe("TerminalInstance", () => {
  beforeEach(() => {
    connectionState.writeCallbacks.length = 0;
    connectionState.callbacks.length = 0;
    connectionState.handles.length = 0;
    connectionState.cleanupCount = 0;
    connectionState.fitCountsAtConnect.length = 0;
    connectionState.terminalSizesAtConnect.length = 0;
    xtermState.fitCount = 0;
    xtermState.onReady = null;
    xtermState.onResize = null;
    xtermState.onData = null;
    xtermState.onBinary = null;
    xtermState.onFocus = null;
    xtermState.handle.write.mockClear();
    xtermState.handle.reset.mockClear();
    xtermState.handle.setSize.mockClear();
    xtermState.handle.focus.mockClear();
    xtermState.handle.scrollToBottom.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
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

  it("wires initial reset and canonical geometry to xterm", async () => {
    render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();
    await waitFor(() => expect(connectionState.callbacks).toHaveLength(1));
    const callbacks = connectionState.callbacks[0];
    if (!callbacks) throw new Error("no callbacks captured");

    act(() => {
      callbacks.onInitialState?.({ cols: 100, rows: 30, retainedLines: 42 });
      callbacks.reset();
      callbacks.onCanonicalResize?.(120, 40);
    });

    expect(xtermState.handle.setSize.mock.calls).toEqual([
      [100, 30],
      [120, 40],
    ]);
    expect(xtermState.handle.reset).toHaveBeenCalledOnce();
  });

  it("sends framed input, resize requests, and focus through the connection handle", async () => {
    const onTerminalFocus = vi.fn();
    render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
        onTerminalFocus={onTerminalFocus}
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();
    await waitFor(() => expect(connectionState.handles).toHaveLength(1));
    const handle = latestConnectionHandle();

    act(() => {
      xtermState.onData?.("hello");
      xtermState.onBinary?.(new Uint8Array([0, 255]));
      xtermState.onResize?.(120, 40);
      xtermState.onFocus?.();
    });

    expect(handle.sendInput.mock.calls).toEqual([
      ["hello"],
      [new Uint8Array([0, 255])],
    ]);
    expect(handle.sendResizeRequest).toHaveBeenCalledWith(120, 40);
    expect(handle.sendFocus).toHaveBeenCalledOnce();
    expect(onTerminalFocus).toHaveBeenCalledOnce();
  });

  it("reconnects with at most 250ms jitter for a resnapshot request", async () => {
    const random = vi.spyOn(Math, "random").mockReturnValue(1);
    const view = render(
      <TerminalInstance
        sessionName="codex-alpha"
        backendName="codex"
        isActive
      />,
    );
    await waitFor(() => expect(xtermState.onReady).not.toBeNull());
    readyRenderer();
    await waitFor(() => expect(connectionState.handles).toHaveLength(1));
    const callbacks = connectionState.callbacks[0];
    if (!callbacks) throw new Error("no callbacks captured");

    vi.useFakeTimers();
    act(() => callbacks.onDisconnected?.("immediate"));
    act(() => vi.advanceTimersByTime(249));
    expect(connectionState.handles).toHaveLength(1);
    act(() => vi.advanceTimersByTime(1));
    expect(connectionState.handles).toHaveLength(2);

    view.unmount();
    vi.useRealTimers();
    random.mockRestore();
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
});
