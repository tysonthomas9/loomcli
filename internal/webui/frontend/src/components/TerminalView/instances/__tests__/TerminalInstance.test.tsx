/**
 * @vitest-environment jsdom
 */

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const connectionState = vi.hoisted(() => ({
  writeCallbacks: [] as Array<(data: string | Uint8Array) => void>,
  // Lifecycle callbacks of every connect attempt, so a test can drive the
  // open/close cycles that the flap detector counts.
  connectedCallbacks: [] as Array<() => void>,
  disconnectedCallbacks: [] as Array<() => void>,
  cleanupCount: 0,
  fitCountsAtConnect: [] as number[],
  terminalSizesAtConnect: [] as Array<{ cols: number; rows: number }>,
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
    },
  };
  state.handle = {
    write: vi.fn(),
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
    onResize: (cols: number, rows: number) => void;
  }) {
    xtermState.onReady = props.onReady;
    xtermState.onResize = props.onResize;
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
      _wsRef: { current: WebSocket | null },
      setConnState: (state: string) => void,
      onConnected: () => void,
      onDisconnected: () => void,
      _onOutput: () => void,
      _onBackendCrash: (reason: string) => void,
      _onSessionKilled: () => void,
      terminalSize: { cols: number; rows: number },
    ): (() => void) => {
      connectionState.writeCallbacks.push(write);
      connectionState.connectedCallbacks.push(onConnected);
      connectionState.disconnectedCallbacks.push(onDisconnected);
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
    connectionState.connectedCallbacks.length = 0;
    connectionState.disconnectedCallbacks.length = 0;
    connectionState.cleanupCount = 0;
    connectionState.fitCountsAtConnect.length = 0;
    connectionState.terminalSizesAtConnect.length = 0;
    xtermState.fitCount = 0;
    xtermState.onReady = null;
    xtermState.onResize = null;
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

  it("reconnects after a attachable lifecycle transition", async () => {
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
        attachable
      />,
    );
    await flushPendingWork();

    expect(connectionState.cleanupCount).toBeGreaterThanOrEqual(1);
    expect(connectionState.writeCallbacks.length).toBeGreaterThanOrEqual(2);
  });

  describe("shell that dies on spawn", () => {
    // The flap detector measures wall-clock life, and the reconnect loop
    // waits on backoff timers, so both need to be under the test's control.
    beforeEach(() => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    /** Mount an active instance with its renderer ready and one live connection. */
    async function mountConnected(
      props: Record<string, unknown> = {},
    ): Promise<void> {
      render(
        <TerminalInstance
          sessionName="codex-alpha"
          backendName="codex"
          isActive
          {...props}
        />,
      );
      await waitFor(() => expect(xtermState.onReady).not.toBeNull());
      readyRenderer();
      await waitFor(() => {
        expect(connectionState.writeCallbacks).toHaveLength(1);
      });
    }

    /**
     * Open the newest connection and immediately close it again, which is
     * what a session whose launch command exits on startup looks like from
     * the client: the attach succeeds, the shell behind it is already gone.
     */
    function flapNewestConnection(): void {
      const connected = connectionState.connectedCallbacks.at(-1);
      const disconnected = connectionState.disconnectedCallbacks.at(-1);
      if (!connected || !disconnected)
        throw new Error("no connection captured");
      act(() => {
        connected();
        disconnected();
      });
    }

    it("stops reconnecting and reports spawn_failed after repeated instant deaths", async () => {
      const onConnectionStateChange = vi.fn();
      await mountConnected({ onConnectionStateChange });

      // Each cycle needs the backoff timer to fire before the next attempt.
      for (let cycle = 0; cycle < 3; cycle++) {
        flapNewestConnection();
        await act(async () => {
          await vi.advanceTimersByTimeAsync(60_000);
        });
      }

      const states = onConnectionStateChange.mock.calls.map(([state]) => state);
      expect(states).toContain("spawn_failed");

      // The loop is stopped, not merely slowed: no further attempts land no
      // matter how long the client waits.
      const attemptsAtGiveUp = connectionState.writeCallbacks.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5 * 60_000);
      });
      expect(connectionState.writeCallbacks).toHaveLength(attemptsAtGiveUp);
    });

    it("keeps reconnecting when a connection actually lived a while", async () => {
      const onConnectionStateChange = vi.fn();
      await mountConnected({ onConnectionStateChange });

      for (let cycle = 0; cycle < 4; cycle++) {
        const connected = connectionState.connectedCallbacks.at(-1);
        const disconnected = connectionState.disconnectedCallbacks.at(-1);
        act(() => connected?.());
        // Well past SHORT_LIVED_CONNECTION_MS, so this is an ordinary drop.
        await act(async () => {
          await vi.advanceTimersByTimeAsync(30_000);
        });
        act(() => disconnected?.());
        await act(async () => {
          await vi.advanceTimersByTimeAsync(60_000);
        });
      }

      const states = onConnectionStateChange.mock.calls.map(([state]) => state);
      expect(states).not.toContain("spawn_failed");
    });
  });
});
