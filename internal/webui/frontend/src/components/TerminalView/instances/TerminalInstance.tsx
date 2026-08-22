/**
 * TerminalInstance component.
 *
 * A single xterm.js pane bound to one PTY-backed WebSocket. This component
 * owns the shared WebSocket lifecycle and reconnect state machine; the
 * renderer owns buffer, scrolling, reflow, font, and theme behavior.
 *
 * The imperative handle is deliberately narrow (disconnect, reconnect,
 * focus, pasteText) so parent components stay decoupled from renderer
 * internals.
 */

import {
  forwardRef,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import { getTerminalConfig } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import {
  startAutoReconnect,
  type ReconnectConfig,
  type ReconnectState,
} from "@/utils/reconnectBackoff";

import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import {
  connectWebSocket,
  type ReconnectPolicy,
  type TerminalConnectionHandle,
  type TerminalNotice,
} from "./terminalConnection";
import type { XTermRendererHandle } from "./XTermRenderer";
import styles from "./TerminalInstance.module.css";

const LazyXTermRenderer = lazy(async () => {
  const module = await import("./XTermRenderer");
  return { default: module.XTermRenderer };
});

/** Stricter backoff for initial connection failures (e.g. session not found). */
const INITIAL_CONNECT_CONFIG: ReconnectConfig = {
  maxAttempts: 3,
  baseDelay: 3000,
  maxDelay: 15000,
  jitterFactor: 0.5,
};

const IMMEDIATE_RECONNECT_MAX_DELAY_MS = 250;

/**
 * Wall-clock ceiling: if a reconnect doesn't succeed within this window,
 * give up and surface the `expired` overlay. The ceiling is clamped to the
 * server's advertised grace period (`/api/config/terminal`) so the client
 * never keeps retrying past the point where the server has already killed
 * the shell. A server value of 0 means "no timeout" (local `loom serve`),
 * in which case we fall back to a long but finite ceiling so the retry
 * loop doesn't run forever unnoticed.
 */
const UNBOUNDED_RECONNECT_TIMEOUT_MS = 60 * 60 * 1000; // 1 h when server disables its own timeout

function isSocketOpenOrConnecting(ws: WebSocket | null): boolean {
  return (
    ws?.readyState === WebSocket.OPEN || ws?.readyState === WebSocket.CONNECTING
  );
}

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error"
  | "crashed"
  // The backend tab metadata survived a server restart but the PTY did
  // not. We deliberately did not auto-respawn — the overlay prompts the
  // user before opening a fresh shell so scrollback loss is explicit.
  | "session_ended";

export interface TerminalInstanceProps {
  sessionName: string;
  isActive: boolean;
  /** Canonical backend name, retained as terminal session metadata. */
  backendName?: string | undefined;
  onConnectionStateChange?: (
    state: ConnectionState,
    hasConnected: boolean,
  ) => void;
  onReconnectStateChange?: (state: ReconnectOverlayState) => void;
  onOutput?: () => void;
  onBackendCrash?: (reason: string) => void;
  onNotice?: ((notice: TerminalNotice) => void) | undefined;
  onTerminalFocus?: (() => void) | undefined;
  /** Whether user input should be forwarded to the backing PTY. */
  writable?: boolean | undefined;
  /**
   * Liveness hint from the backend's tab metadata. When explicitly
   * `false`, the auto-connect on mount is skipped and the overlay goes
   * directly to "session_ended" so the user opts into spawning a fresh
   * shell (losing prior scrollback). `undefined` or `true` preserves
   * the pre-liveness behavior of connecting immediately.
   */
  ptyAlive?: boolean | undefined;
  /** When true, stale PTYs are automatically replaced with a fresh shell. */
  autoStartStaleSession?: boolean | undefined;
  /** When false, unexpected disconnects stop at the reconnect affordance. */
  autoReconnect?: boolean | undefined;
}

export interface TerminalInstanceHandle {
  /** Cancel in-flight reconnects and close the WebSocket. Resolves when socket is closed (or after 2s). */
  disconnect: () => Promise<void>;
  /** Drop the current connection and open a fresh one. */
  reconnect: () => void;
  /** Focus the terminal grid. */
  focus: () => void;
  /** Send arbitrary text over the WebSocket (e.g. for programmatic paste). */
  pasteText: (text: string) => void;
}

export const TerminalInstance = forwardRef<
  TerminalInstanceHandle,
  TerminalInstanceProps
>(function TerminalInstance(
  {
    sessionName,
    isActive,
    onConnectionStateChange,
    onReconnectStateChange,
    onOutput,
    onBackendCrash,
    onNotice,
    onTerminalFocus,
    writable = true,
    ptyAlive,
    autoStartStaleSession,
    autoReconnect = true,
  },
  ref,
) {
  const { workspaceId } = useWorkspaceContext();
  const xtermInstanceRef = useRef<XTermRendererHandle | null>(null);
  const pendingRendererWritesRef = useRef<Array<string | Uint8Array>>([]);
  const terminalSizeRef = useRef({ cols: 80, rows: 24 });

  const syncViewportToBottom = useCallback(() => {
    xtermInstanceRef.current?.scrollToBottom();
  }, []);

  const write = useCallback((data: string | Uint8Array) => {
    const xterm = xtermInstanceRef.current;
    if (xterm) {
      xterm.write(data);
    } else {
      pendingRendererWritesRef.current.push(data);
    }
  }, []);
  const reset = useCallback(() => {
    pendingRendererWritesRef.current = [];
    xtermInstanceRef.current?.reset();
  }, []);
  const focus = useCallback(() => {
    xtermInstanceRef.current?.focus();
  }, []);

  // Start pessimistic: until the server's config arrives, prefer the long
  // ceiling over the old 30-second default. This matters for loom-agentd
  // deployments where the real grace is minutes — a client falling back to
  // 30 s would give up while the server still held the shell open. The
  // config promise is memoised so the fetch runs at most once per app load.
  const reconnectCeilingMsRef = useRef<number>(UNBOUNDED_RECONNECT_TIMEOUT_MS);
  useEffect(() => {
    let cancelled = false;
    void getTerminalConfig().then((cfg) => {
      if (cancelled) return;
      // grace_period_ms == 0 on the server means "disabled". Translate into
      // a long-but-finite client ceiling. Otherwise honour the server value.
      reconnectCeilingMsRef.current =
        cfg.gracePeriodMs > 0
          ? cfg.gracePeriodMs
          : UNBOUNDED_RECONNECT_TIMEOUT_MS;
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const wsRef = useRef<WebSocket | null>(null);
  const connectionRef = useRef<TerminalConnectionHandle | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const beingKilledRef = useRef(false);
  const hasConnectedRef = useRef(false);
  const initialViewportSyncDoneRef = useRef(false);
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const [readyVersion, setReadyVersion] = useState(0);
  const isActiveRef = useRef(isActive);
  isActiveRef.current = isActive;

  // Stable refs for parent callbacks so the lifecycle effect's dep array
  // stays minimal.
  const onConnectionStateChangeRef = useRef(onConnectionStateChange);
  onConnectionStateChangeRef.current = onConnectionStateChange;
  const onReconnectStateChangeRef = useRef(onReconnectStateChange);
  onReconnectStateChangeRef.current = onReconnectStateChange;
  const onOutputRef = useRef(onOutput);
  onOutputRef.current = onOutput;
  const onBackendCrashRef = useRef(onBackendCrash);
  onBackendCrashRef.current = onBackendCrash;
  const onNoticeRef = useRef(onNotice);
  onNoticeRef.current = onNotice;
  const onTerminalFocusRef = useRef(onTerminalFocus);
  onTerminalFocusRef.current = onTerminalFocus;

  // Surface connection state changes.
  useEffect(() => {
    if (connectionState === "connected") hasConnectedRef.current = true;
    onConnectionStateChangeRef.current?.(
      connectionState,
      hasConnectedRef.current,
    );
  }, [connectionState]);

  const clearReconnectTimers = useCallback(() => {
    reconnectCancelRef.current?.();
    reconnectCancelRef.current = null;
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
  }, []);

  // doConnect is defined below but `startReconnectLoop` needs a stable
  // reference to it. A ref avoids the circular-dep dance and keeps the
  // reconnect loop calling the latest doConnect even if its closure over
  // workspaceId / sessionName re-memoises.
  const doConnectRef = useRef<
    ((opts?: { onOutcome?: (ok: boolean) => void }) => void) | null
  >(null);

  const startReconnectLoop = useCallback(
    (config?: ReconnectConfig) => {
      clearReconnectTimers();
      onReconnectStateChangeRef.current?.("reconnecting");

      reconnectTimeoutRef.current = setTimeout(() => {
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        onReconnectStateChangeRef.current?.("expired");
      }, reconnectCeilingMsRef.current);

      const cancel = startAutoReconnect(
        () =>
          new Promise<boolean>((resolve) => {
            doConnectRef.current?.({ onOutcome: resolve });
          }),
        (state: ReconnectState) => {
          if (state.gaveUp) {
            clearReconnectTimers();
            onReconnectStateChangeRef.current?.("expired");
          }
        },
        config,
      );
      reconnectCancelRef.current = cancel;
    },
    [clearReconnectTimers],
  );

  const scheduleImmediateReconnect = useCallback(() => {
    clearReconnectTimers();
    onReconnectStateChangeRef.current?.("reconnecting");
    reconnectTimeoutRef.current = setTimeout(() => {
      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;
      onReconnectStateChangeRef.current?.("expired");
    }, reconnectCeilingMsRef.current);

    const timer = setTimeout(() => {
      reconnectCancelRef.current = null;
      doConnectRef.current?.();
    }, Math.random() * IMMEDIATE_RECONNECT_MAX_DELAY_MS);
    reconnectCancelRef.current = () => clearTimeout(timer);
  }, [clearReconnectTimers]);

  const doConnect = useCallback(
    (opts?: { onOutcome?: (ok: boolean) => void }) => {
      if (beingKilledRef.current) {
        opts?.onOutcome?.(false);
        return;
      }

      // Tear down any prior connection before opening a new one.
      connectionRef.current?.dispose();
      connectionRef.current = null;

      const connection = connectWebSocket(
        workspaceId,
        sessionName,
        wsRef,
        {
          write,
          reset,
          setConnectionState,
          onConnected: () => {
            clearReconnectTimers();
            onReconnectStateChangeRef.current?.(null);
            if (!initialViewportSyncDoneRef.current) {
              initialViewportSyncDoneRef.current = true;
              syncViewportToBottom();
            }
            opts?.onOutcome?.(true);
          },
          onDisconnected: (policy: ReconnectPolicy) => {
            opts?.onOutcome?.(false);
            if (beingKilledRef.current) return;
            if (autoReconnect && policy === "immediate") {
              scheduleImmediateReconnect();
            } else if (autoReconnect && !reconnectCancelRef.current) {
              const config = hasConnectedRef.current
                ? undefined
                : INITIAL_CONNECT_CONFIG;
              startReconnectLoop(config);
            } else if (!autoReconnect) {
              clearReconnectTimers();
              onReconnectStateChangeRef.current?.(null);
              if (hasConnectedRef.current) {
                setConnectionState("session_ended");
              }
            }
          },
          onOutput: () => onOutputRef.current?.(),
          onBackendCrash: (reason) => onBackendCrashRef.current?.(reason),
          onSessionKilled: () => {
            beingKilledRef.current = true;
            clearReconnectTimers();
            onReconnectStateChangeRef.current?.(null);
          },
          onInitialState: ({ cols, rows }) => {
            xtermInstanceRef.current?.setSize(cols, rows);
          },
          onCanonicalResize: (cols, rows) => {
            xtermInstanceRef.current?.setSize(cols, rows);
          },
          onNotice: (notice) => onNoticeRef.current?.(notice),
        },
        terminalSizeRef.current,
      );
      connectionRef.current = connection;
    },
    [
      workspaceId,
      sessionName,
      write,
      reset,
      clearReconnectTimers,
      syncViewportToBottom,
      startReconnectLoop,
      scheduleImmediateReconnect,
      autoReconnect,
    ],
  );

  // Keep the reconnect loop pointing at the latest doConnect closure.
  doConnectRef.current = doConnect;

  // Mount / teardown per session.
  useEffect(() => {
    beingKilledRef.current = false;
    hasConnectedRef.current = false;
    initialViewportSyncDoneRef.current = false;
    // The renderer remains mounted across metadata-driven effect re-runs, so
    // onReady does not fire again. Re-kick the WebSocket from its live handle.
    const canReconnect =
      isActiveRef.current && (ptyAlive !== false || autoStartStaleSession);
    if (canReconnect && xtermInstanceRef.current != null) {
      doConnectRef.current?.();
    }
    return () => {
      beingKilledRef.current = true;
      // Do NOT null xtermInstanceRef here: the XTermRenderer child owns its
      // handle and nulls it via handleXTermDispose on real disposal. Nulling it
      // on a mere effect re-run (e.g. a ptyAlive transition) strands the
      // tab with no path back — no onReady to repopulate the ref, no reconnect.
      pendingRendererWritesRef.current = [];
      clearReconnectTimers();
      connectionRef.current?.dispose();
      connectionRef.current = null;
    };
  }, [sessionName, clearReconnectTimers, ptyAlive, autoStartStaleSession]);

  useEffect(() => {
    if (ptyAlive === false && !autoStartStaleSession) {
      setConnectionState("session_ended");
    }
  }, [ptyAlive, autoStartStaleSession]);

  const handleXTermReady = useCallback(
    (xterm: XTermRendererHandle) => {
      xtermInstanceRef.current = xterm;
      const pendingWrites = pendingRendererWritesRef.current.splice(0);
      for (const data of pendingWrites) {
        xterm.write(data);
      }
      const measured = xterm.fit();
      if (measured) terminalSizeRef.current = measured;
      setReadyVersion((value) => value + 1);
      if (ptyAlive === false && !autoStartStaleSession) {
        setConnectionState("session_ended");
        return;
      }
      if (
        isActiveRef.current &&
        !connectionRef.current &&
        !isSocketOpenOrConnecting(wsRef.current)
      ) {
        doConnectRef.current?.();
      }
    },
    [ptyAlive, autoStartStaleSession],
  );

  const handleXTermDispose = useCallback((xterm: XTermRendererHandle) => {
    if (xtermInstanceRef.current === xterm) {
      xtermInstanceRef.current = null;
    }
  }, []);

  // In the desktop shell, a terminal can be mounted before the renderer's
  // ready event fires. Kick one connection attempt as soon as the pane is
  // active; output is still written once the renderer instance is available.
  useEffect(() => {
    if (!isActive) return;
    if (ptyAlive === false && !autoStartStaleSession) return;
    // Controlled harnesses size their inner PTY from the first WebSocket
    // attachment. Wait for the lazy xterm renderer to fit the visible pane so
    // that first attachment does not permanently seed an 80x24 inner grid.
    if (!xtermInstanceRef.current) return;
    if (connectionState !== "disconnected") return;
    if (reconnectCancelRef.current) return;
    if (connectionRef.current || isSocketOpenOrConnecting(wsRef.current)) {
      return;
    }

    const timeout = setTimeout(() => {
      if (!xtermInstanceRef.current) return;
      if (connectionState !== "disconnected") return;
      if (reconnectCancelRef.current) return;
      if (connectionRef.current || isSocketOpenOrConnecting(wsRef.current)) {
        return;
      }

      doConnectRef.current?.();
    }, 0);

    return () => {
      clearTimeout(timeout);
    };
  }, [
    isActive,
    ptyAlive,
    autoStartStaleSession,
    connectionState,
    readyVersion,
  ]);

  const handleData = useCallback(
    (data: string) => {
      if (!writable) return;
      connectionRef.current?.sendInput(data);
    },
    [writable],
  );

  const handleBinary = useCallback(
    (data: Uint8Array) => {
      if (!writable) return;
      connectionRef.current?.sendInput(data);
    },
    [writable],
  );

  const lastSentResizeRef = useRef<{ cols: number; rows: number } | null>(null);
  const handleResize = useCallback((cols: number, rows: number) => {
    // A hidden host can be measured as a tiny sentinel grid. Keep inactive
    // measurements out of canonical reconnect state and the backing PTY;
    // activation performs a fresh visible fit.
    if (!isActiveRef.current) return;
    terminalSizeRef.current = { cols, rows };
    // Collapse redundant frames from layout observation, activation recovery,
    // and font changes. Re-sending an unchanged size churns the PTY
    // (SIGWINCH -> prompt redraw) for no reason.
    const last = lastSentResizeRef.current;
    if (last && last.cols === cols && last.rows === rows) return;
    if (connectionRef.current) {
      connectionRef.current.sendResizeRequest(cols, rows);
      // Record only what actually went out. Marking a resize "sent" while the
      // socket is still connecting would let a later identical frame be
      // deduped, stranding the PTY at the connect-time size.
      lastSentResizeRef.current = { cols, rows };
    }
  }, []);

  // Tauri can reveal an already-mounted terminal after it measured while
  // hidden. Resync once visible so the PTY has the real grid size and focus.
  // XTermRenderer.fit() preserves either the bottom-follow state or the
  // scrolled-up buffer-line anchor across reflow.
  useEffect(() => {
    if (!isActive) return;

    let cancelled = false;
    let firstFrame = 0;
    let secondFrame = 0;
    const focusTimers: Array<ReturnType<typeof setTimeout>> = [];

    const syncActiveLayout = () => {
      if (cancelled) return;
      const xterm = xtermInstanceRef.current;
      if (!xterm) return;
      const measured = xterm.fit();
      if (measured) {
        terminalSizeRef.current = measured;
        handleResize(measured.cols, measured.rows);
      }
      focus();
    };

    firstFrame = requestAnimationFrame(() => {
      syncActiveLayout();
      secondFrame = requestAnimationFrame(syncActiveLayout);
    });
    for (const delay of [50, 150, 300, 600]) {
      focusTimers.push(setTimeout(syncActiveLayout, delay));
    }

    return () => {
      cancelled = true;
      cancelAnimationFrame(firstFrame);
      cancelAnimationFrame(secondFrame);
      for (const timer of focusTimers) {
        clearTimeout(timer);
      }
    };
  }, [isActive, readyVersion, focus, handleResize]);

  useImperativeHandle(
    ref,
    () => ({
      disconnect: () =>
        new Promise<void>((resolve) => {
          beingKilledRef.current = true;
          clearReconnectTimers();
          const ws = wsRef.current;
          if (
            ws &&
            (ws.readyState === WebSocket.OPEN ||
              ws.readyState === WebSocket.CONNECTING)
          ) {
            const timeout = setTimeout(resolve, 2000);
            ws.addEventListener(
              "close",
              () => {
                clearTimeout(timeout);
                resolve();
              },
              { once: true },
            );
            connectionRef.current?.dispose();
            connectionRef.current = null;
          } else {
            connectionRef.current?.dispose();
            connectionRef.current = null;
            resolve();
          }
        }),
      reconnect: () => {
        beingKilledRef.current = false;
        clearReconnectTimers();
        onReconnectStateChangeRef.current?.(null);
        doConnectRef.current?.();
      },
      focus: () => {
        focus();
        onTerminalFocusRef.current?.();
      },
      pasteText: (text: string) => {
        if (!writable) return;
        connectionRef.current?.sendInput(text);
      },
    }),
    [focus, clearReconnectTimers, writable],
  );

  return (
    <div
      className={styles.wrapper}
      data-testid="terminal-wrapper"
      data-terminal-input
      data-terminal-renderer="xterm"
    >
      <Suspense
        fallback={
          <div className={styles.xtermContainer} data-testid="xterm-loading" />
        }
      >
        <LazyXTermRenderer
          className={styles.xtermContainer}
          onReady={handleXTermReady}
          onDispose={handleXTermDispose}
          onData={handleData}
          onBinary={handleBinary}
          onResize={handleResize}
          onFocus={() => {
            connectionRef.current?.sendFocus();
            onTerminalFocusRef.current?.();
          }}
        />
      </Suspense>
    </div>
  );
});
