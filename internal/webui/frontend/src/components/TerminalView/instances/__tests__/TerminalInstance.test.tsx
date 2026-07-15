/**
 * @vitest-environment jsdom
 */

/**
 * Integration tests for TerminalInstance focused on the WebSocket-write
 * path through wterm. Built so the wterm renderer is the only piece
 * stubbed in detail — everything else is wired up the real way.
 *
 * Regression target: PR #54 (fix/terminal-replay-blank-after-remount).
 * After a lifecycle remount (sessionName change, StrictMode rekick, etc.),
 * the cleanup nulls wtermInstanceRef. wterm's onReady only fires once per
 * WASM lifetime, so without restoring the ref from the still-alive
 * TerminalHandle, every replayed byte is silently dropped by the
 * optional-chain in `write()`.
 */

import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  TERMINAL_FONT_CHANGE_EVENT,
  type TerminalFontChangeDetail,
} from "@/hooks/terminal/useTerminalFont";

// ── Hoisted shared state ─────────────────────────────────────────────────────
//
// Lives at module scope so vi.mock factories can reach it. Tests reset it in
// beforeEach.

const wtermState = vi.hoisted(() => {
  const stub = {
    write: ((..._args: unknown[]) => {}) as (data: string | Uint8Array) => void,
    focus: () => {},
    resize: (_cols: number, _rows: number) => {},
    cols: 80,
    rows: 24,
    // measureTerminalSize reaches into wt.element.querySelector(".term-grid").
    // We deliberately leave it empty so measure returns null and skips the
    // resize path — keeps the stub tiny.
    element: null as HTMLElement | null,
  };
  return {
    stub,
    onReadyFiredCount: 0,
    resizeBeforeReady: null as { cols: number; rows: number } | null,
  };
});

const connectionState = vi.hoisted(() => ({
  writeCallbacks: [] as Array<(data: string | Uint8Array) => void>,
  initialSizes: [] as Array<{ cols: number; rows: number } | undefined>,
  cleanupCount: 0,
}));

// ── Mock @wterm/react ────────────────────────────────────────────────────────
//
// The real <Terminal> wraps a WASM-backed renderer. We stub it with a
// component that:
//   - exposes a stable handle whose `.instance` always points to the same
//     WTerm-shaped stub (mirrors how the real Terminal keeps its WASM
//     instance alive across React re-renders), and
//   - fires `onReady(stub)` exactly once per process lifetime, regardless of
//     how many times the component mounts (mirrors wterm's WASM caching —
//     this is the precondition that makes the original bug possible).

vi.mock("@wterm/react", async () => {
  const React = await import("react");

  type TerminalProps = {
    onReady?: (wt: unknown) => void;
    onData?: (data: string) => void;
    onResize?: (cols: number, rows: number) => void;
    cols?: number;
    rows?: number;
    autoResize?: boolean;
    className?: string;
  };

  const Terminal = React.forwardRef<unknown, TerminalProps>(
    function Terminal(props, ref) {
      const { onReady, onResize } = props;

      React.useImperativeHandle(
        ref,
        () => ({
          instance: wtermState.stub,
          write: (data: string | Uint8Array) => wtermState.stub.write(data),
          focus: () => wtermState.stub.focus(),
        }),
        [],
      );

      React.useEffect(() => {
        if (wtermState.onReadyFiredCount === 0) {
          wtermState.onReadyFiredCount += 1;
          if (wtermState.resizeBeforeReady) {
            onResize?.(
              wtermState.resizeBeforeReady.cols,
              wtermState.resizeBeforeReady.rows,
            );
          }
          onReady?.(wtermState.stub);
        }
      }, [onReady, onResize]);

      return React.createElement("div", { "data-testid": "mock-wterm" });
    },
  );

  return { Terminal };
});

vi.mock("@wterm/react/css", () => ({}));

// ── Mock terminalConnection ──────────────────────────────────────────────────
//
// Captures every `write` callback the component hands us. Tests invoke the
// most recent one and assert whether bytes reach the wterm stub.

vi.mock("../terminalConnection", () => ({
  connectWebSocket: vi.fn(
    (
      _workspaceId: string,
      _sessionName: string,
      write: (data: string | Uint8Array) => void,
      _wsRef: { current: WebSocket | null },
      setConnState: (s: string) => void,
      _onConnected?: () => void,
      _onDisconnected?: () => void,
      _onOutput?: () => void,
      _onBackendCrash?: (reason: string) => void,
      _onSessionKilled?: () => void,
      initialSize?: { cols: number; rows: number },
    ): (() => void) => {
      connectionState.writeCallbacks.push(write);
      connectionState.initialSizes.push(initialSize);
      setConnState("connecting");
      const cleanup = () => {
        connectionState.cleanupCount += 1;
      };
      return cleanup;
    },
  ),
  encodeResize: (cols: number, rows: number) => `\x1b[RESIZE:${cols};${rows}]`,
}));

// ── Mock workspace + terminal-config hooks ───────────────────────────────────

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws" }),
}));

vi.mock("@/hooks/api", () => ({
  getTerminalConfig: vi.fn().mockResolvedValue({ gracePeriodMs: 0 }),
}));

// Imports must come after vi.mock declarations so the mocks are in place.
import { TerminalInstance } from "../TerminalInstance";

// ── Helpers ──────────────────────────────────────────────────────────────────

async function flushPendingWork(): Promise<void> {
  // Two ticks: first flushes microtasks (effects), second flushes the
  // setTimeout(0) used by the activate-on-mount effect.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function latestWriteCallback(): (data: string | Uint8Array) => void {
  const cb =
    connectionState.writeCallbacks[connectionState.writeCallbacks.length - 1];
  if (!cb) throw new Error("no write callback captured yet");
  return cb;
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe("TerminalInstance", () => {
  beforeEach(() => {
    wtermState.stub.write = vi.fn();
    wtermState.stub.element = document.createElement("div");
    wtermState.onReadyFiredCount = 0;
    wtermState.resizeBeforeReady = null;
    connectionState.writeCallbacks.length = 0;
    connectionState.initialSizes.length = 0;
    connectionState.cleanupCount = 0;
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  describe("write path after remount (regression: PR #54)", () => {
    it("delivers bytes to wterm after a sessionName change forces the lifecycle effect to re-run", async () => {
      // First mount: a fresh wterm fires onReady once and sets
      // wtermInstanceRef. Output written through the captured callback should
      // reach the stub.
      const { rerender, unmount } = render(
        <TerminalInstance sessionName="alpha" isActive />,
      );
      await flushPendingWork();

      expect(wtermState.onReadyFiredCount).toBe(1);
      expect(connectionState.writeCallbacks.length).toBeGreaterThanOrEqual(1);

      const writeMock = wtermState.stub.write as ReturnType<typeof vi.fn>;

      act(() => {
        latestWriteCallback()("first-mount-bytes");
      });
      expect(writeMock).toHaveBeenCalledWith("first-mount-bytes");

      writeMock.mockClear();

      // sessionName change re-runs the lifecycle effect: cleanup nulls
      // wtermInstanceRef, then the rekick path re-opens the WS without
      // wterm firing onReady again. With the fix, that path restores
      // wtermInstanceRef from the still-alive TerminalHandle.
      rerender(<TerminalInstance sessionName="beta" isActive />);
      await flushPendingWork();

      // onReady must NOT have re-fired — the WASM cache is the precondition
      // for the bug.
      expect(wtermState.onReadyFiredCount).toBe(1);

      // The remount opens a new WebSocket connection, giving us a fresh
      // write callback. Without the fix, this callback would silently push
      // bytes into the pending buffer (wtermInstanceRef.current === null);
      // with the fix, the ref is restored and the bytes reach the stub.
      expect(connectionState.writeCallbacks.length).toBeGreaterThanOrEqual(2);

      act(() => {
        latestWriteCallback()("post-remount-replay");
      });

      expect(writeMock).toHaveBeenCalledWith("post-remount-replay");

      unmount();
    });

    it("delivers bytes to wterm after StrictMode double-mounts the component", async () => {
      // Same scenario, exercised through React.StrictMode rather than a prop
      // change. StrictMode double-invokes mount → cleanup → mount in dev
      // mode, which is exactly the path that surfaced the original bug in
      // production after a page refresh.
      const { StrictMode } = await import("react");

      const { unmount } = render(
        <StrictMode>
          <TerminalInstance sessionName="alpha" isActive />
        </StrictMode>,
      );
      await flushPendingWork();

      expect(wtermState.onReadyFiredCount).toBe(1);
      expect(connectionState.writeCallbacks.length).toBeGreaterThanOrEqual(1);

      const writeMock = wtermState.stub.write as ReturnType<typeof vi.fn>;

      act(() => {
        latestWriteCallback()("strict-mode-bytes");
      });
      expect(writeMock).toHaveBeenCalledWith("strict-mode-bytes");

      unmount();
    });
  });

  describe("terminal sizing", () => {
    it("does not seed the WebSocket with a transient one-column resize", async () => {
      wtermState.resizeBeforeReady = { cols: 1, rows: 24 };

      const { unmount } = render(
        <TerminalInstance sessionName="collapsed-first" isActive />,
      );
      await flushPendingWork();

      expect(connectionState.initialSizes.length).toBeGreaterThanOrEqual(1);
      expect(connectionState.initialSizes[0]).toEqual({ cols: 80, rows: 24 });

      unmount();
    });

    it("uses a non-collapsed resize as the initial WebSocket size", async () => {
      wtermState.resizeBeforeReady = { cols: 120, rows: 30 };

      const { unmount } = render(
        <TerminalInstance sessionName="measured-first" isActive />,
      );
      await flushPendingWork();

      expect(connectionState.initialSizes.length).toBeGreaterThanOrEqual(1);
      expect(connectionState.initialSizes[0]).toEqual({
        cols: 120,
        rows: 30,
      });

      unmount();
    });
  });

  describe("terminal font changes", () => {
    it("re-measures and resizes when terminal font prefs change", async () => {
      const element = document.createElement("div");
      Object.defineProperty(element, "clientWidth", { value: 800 });
      Object.defineProperty(element, "clientHeight", { value: 400 });
      const grid = document.createElement("div");
      grid.className = "term-grid";
      element.appendChild(grid);
      wtermState.stub.element = element;

      const rectSpy = vi
        .spyOn(HTMLElement.prototype, "getBoundingClientRect")
        .mockReturnValue({
          width: 10,
          height: 20,
          top: 0,
          left: 0,
          right: 10,
          bottom: 20,
          x: 0,
          y: 0,
          toJSON: () => ({}),
        } as DOMRect);

      const resizeMock = vi.fn();
      wtermState.stub.resize = resizeMock;

      render(<TerminalInstance sessionName="alpha" isActive />);
      await flushPendingWork();

      act(() => {
        window.dispatchEvent(
          new CustomEvent<TerminalFontChangeDetail>(
            TERMINAL_FONT_CHANGE_EVENT,
            {
              detail: {
                fontFamily: "Monaco, monospace",
                fontSize: 20,
              },
            },
          ),
        );
      });

      expect(resizeMock).toHaveBeenCalled();
      rectSpy.mockRestore();
    });
  });
});
