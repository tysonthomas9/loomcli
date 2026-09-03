/**
 * @vitest-environment jsdom
 */

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const connectionState = vi.hoisted(() => ({
  writeCallbacks: [] as Array<(data: string | Uint8Array) => void>,
  attachCallbacks: [] as Array<(frame: unknown) => void>,
  cleanupCount: 0,
  fitCountsAtConnect: [] as number[],
  terminalSizesAtConnect: [] as Array<{ cols: number; rows: number }>,
}));

const xtermState = vi.hoisted(() => {
  const state = {
    fitCount: 0,
    /**
     * What the pane would actually be showing. Writes accumulate; a write
     * containing the erase-display sequence discards everything written
     * before it, exactly as xterm.js would. Anything drawn ahead of the
     * scrollback replay therefore disappears from this buffer — which is the
     * bug the boundary tests below exist to catch.
     */
    screen: "",
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
    write: vi.fn((data: string | Uint8Array) => {
      const text =
        typeof data === "string" ? data : new TextDecoder().decode(data);
      const clearAt = text.lastIndexOf("\x1b[2J");
      state.screen =
        clearAt === -1
          ? state.screen + text
          : text.slice(clearAt + "\x1b[2J".length);
    }),
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
      _onConnected: () => void,
      _onDisconnected: () => void,
      _onOutput: () => void,
      _onBackendCrash: (reason: string) => void,
      _onSessionKilled: () => void,
      terminalSize: { cols: number; rows: number },
      onAttach?: (frame: unknown) => void,
    ): (() => void) => {
      connectionState.writeCallbacks.push(write);
      if (onAttach) connectionState.attachCallbacks.push(onAttach);
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

function latestAttachCallback(): (frame: unknown) => void {
  const callback = connectionState.attachCallbacks.at(-1);
  if (!callback) throw new Error("no attach callback captured");
  return callback;
}

const BOUNDARY_MARKER = "this is a new shell";

function boundaryOccurrences(): number {
  return xtermState.screen.split(BOUNDARY_MARKER).length - 1;
}

function attachFrame(overrides: Record<string, unknown> = {}): unknown {
  return {
    type: "attach",
    reattached: false,
    replaced: true,
    replaced_at: "2026-08-14T16:52:03Z",
    replaced_reason: "server_restart",
    ...overrides,
  };
}

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

describe("TerminalInstance", () => {
  beforeEach(() => {
    connectionState.writeCallbacks.length = 0;
    connectionState.attachCallbacks.length = 0;
    connectionState.cleanupCount = 0;
    xtermState.screen = "";
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

  describe("session replacement boundary", () => {
    it("leaves the boundary visible after the scrollback replay's screen clear", async () => {
      await mountConnected();
      const write = latestWriteCallback();

      // The server's order on the wire: replay (binary, opening with the
      // erase-display sequence), then the attach control frame, then live
      // output from the fresh shell.
      act(() =>
        write(new TextEncoder().encode("\x1b[2J\x1b[Hold scrollback\r\n")),
      );
      act(() => latestAttachCallback()(attachFrame()));
      act(() => write("fresh-shell$ "));

      expect(boundaryOccurrences()).toBe(1);
      expect(xtermState.screen).toContain("server restarted");
      // And it sits at the seam: replay above it, new output below.
      expect(xtermState.screen.indexOf("old scrollback")).toBeLessThan(
        xtermState.screen.indexOf(BOUNDARY_MARKER),
      );
      expect(xtermState.screen.indexOf(BOUNDARY_MARKER)).toBeLessThan(
        xtermState.screen.indexOf("fresh-shell$ "),
      );
    });

    it("draws the boundary exactly once when there is no replay frame at all", async () => {
      await mountConnected();
      const write = latestWriteCallback();

      // Empty scrollback: the server skips the replay write entirely, so no
      // erase-display sequence ever arrives.
      act(() => latestAttachCallback()(attachFrame()));
      act(() => write("fresh-shell$ "));

      expect(boundaryOccurrences()).toBe(1);
      expect(xtermState.screen.indexOf(BOUNDARY_MARKER)).toBeLessThan(
        xtermState.screen.indexOf("fresh-shell$ "),
      );
    });

    it("draws no boundary on a reattach", async () => {
      await mountConnected();

      act(() =>
        latestAttachCallback()(
          attachFrame({ reattached: true, replaced: false }),
        ),
      );

      expect(boundaryOccurrences()).toBe(0);
    });

    it("does not draw a second boundary when the socket reconnects", async () => {
      const onSessionReplaced = vi.fn();
      await mountConnected({ onSessionReplaced });

      act(() => latestAttachCallback()(attachFrame()));
      // Reconnect: the server re-announces the same replacement, now as a
      // reattach. The pane is never unmounted, so its buffer still holds the
      // first boundary.
      act(() =>
        latestAttachCallback()(
          attachFrame({ reattached: true, replaced: false }),
        ),
      );
      // Even a repeated "this attach is the replacement" frame for the same
      // timestamp must not stack a second line.
      act(() => latestAttachCallback()(attachFrame()));

      expect(boundaryOccurrences()).toBe(1);
      expect(onSessionReplaced).toHaveBeenCalledTimes(1);
    });

    it("surfaces the replacement timestamp to the metadata owner", async () => {
      const onSessionReplaced = vi.fn();
      await mountConnected({ onSessionReplaced });

      act(() => latestAttachCallback()(attachFrame()));

      expect(onSessionReplaced).toHaveBeenCalledWith("2026-08-14T16:52:03Z");
    });

    it("renders the replacement time in the boundary line", async () => {
      await mountConnected();
      const replacedAt = "2026-08-14T16:52:03Z";
      const local = new Date(replacedAt);
      const expected = `${String(local.getHours()).padStart(2, "0")}:${String(
        local.getMinutes(),
      ).padStart(2, "0")}`;

      act(() =>
        latestAttachCallback()(attachFrame({ replaced_at: replacedAt })),
      );

      expect(xtermState.screen).toContain(`server restarted ${expected}`);
    });

    it("still draws the boundary for a read-only tab", async () => {
      // The boundary is a client-side render, not PTY input, so the
      // `writable` gate on handleData must not reach it.
      await mountConnected({ writable: false });

      act(() => latestAttachCallback()(attachFrame()));

      expect(boundaryOccurrences()).toBe(1);
    });
  });
});
