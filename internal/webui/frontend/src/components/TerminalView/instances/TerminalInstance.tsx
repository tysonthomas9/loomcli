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
import { connectWebSocket, encodeResize } from "./terminalConnection";
import type { XTermRendererHandle } from "./XTermRenderer";
import { getTerminalHistoryMode } from "./terminalHistoryMode";
import {
  VirtualTerminalHistory,
  type VirtualTerminalHistoryHandle,
} from "./VirtualTerminalHistory";
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

/**
 * A connection must stay open this long before it resets the reconnect
 * backoff. A crash-looping session "connects" successfully and dies moments
 * later; resetting on bare ws.onopen made every doomed cycle restart the
 * backoff at attempt 0, hammering the server at the base cadence forever.
 */
const HEALTHY_CONNECTION_RESET_MS = 10_000;

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
  const virtualHistoryRef = useRef<VirtualTerminalHistoryHandle | null>(null);
  const pendingRendererWritesRef = useRef<Array<string | Uint8Array>>([]);
  const terminalSizeRef = useRef({ cols: 80, rows: 24 });
  const configuredHistoryModeRef = useRef(getTerminalHistoryMode());
  const [historyMode, setHistoryMode] = useState(
    configuredHistoryModeRef.current,
  );
  const [firstScreenLine, setFirstScreenLine] = useState<number | undefined>();
  const [recordingEpoch, setRecordingEpoch] = useState(0);

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
  const wsCleanupRef = useRef<(() => void) | null>(null);
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

  const reconnectAttemptCarryRef = useRef(0);
  const healthyResetTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );

  const clearReconnectTimers = useCallback(() => {
    reconnectCancelRef.current?.();
    reconnectCancelRef.current = null;
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (healthyResetTimerRef.current) {
      clearTimeout(healthyResetTimerRef.current);
      healthyResetTimerRef.current = null;
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
          reconnectAttemptCarryRef.current = state.attempt;
          if (state.gaveUp) {
            clearReconnectTimers();
            onReconnectStateChangeRef.current?.("expired");
          }
        },
        config,
        reconnectAttemptCarryRef.current,
      );
      reconnectCancelRef.current = cancel;
    },
    [clearReconnectTimers],
  );

  const doConnect = useCallback(
    (opts?: { onOutcome?: (ok: boolean) => void }) => {
      if (beingKilledRef.current) {
        opts?.onOutcome?.(false);
        return;
      }

      // Tear down any prior connection before opening a new one.
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;

      const cleanup = connectWebSocket(
        workspaceId,
        sessionName,
        (data) => write(data),
        wsRef,
        setConnectionState,
        // onConnected — the renderer receives the server's durable PTY replay
        // immediately after this callback.
        () => {
          clearReconnectTimers();
          // Only a connection that stays up resets the backoff carry; a
          // crash-looping session that opens and dies keeps escalating.
          healthyResetTimerRef.current = setTimeout(() => {
            reconnectAttemptCarryRef.current = 0;
            healthyResetTimerRef.current = null;
          }, HEALTHY_CONNECTION_RESET_MS);
          onReconnectStateChangeRef.current?.(null);
          setFirstScreenLine(undefined);
          setRecordingEpoch((epoch) => epoch + 1);
          if (!initialViewportSyncDoneRef.current) {
            initialViewportSyncDoneRef.current = true;
            syncViewportToBottom();
          }
          opts?.onOutcome?.(true);
        },
        // onDisconnected — schedule a reconnect unless we're being killed.
        () => {
          opts?.onOutcome?.(false);
          if (beingKilledRef.current) return;
          // Only start a fresh reconnect loop if none is running. If one is
          // already running, startAutoReconnect's own backoff handles the
          // retry — we just wait for the next attempt.
          if (autoReconnect && !reconnectCancelRef.current) {
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
        () => onOutputRef.current?.(),
        (reason) => onBackendCrashRef.current?.(reason),
        // onSessionKilled — server told us the session is gone; do not reconnect.
        () => {
          beingKilledRef.current = true;
          clearReconnectTimers();
          onReconnectStateChangeRef.current?.(null);
        },
        terminalSizeRef.current,
        (line, available) => {
          setHistoryMode(
            available ? configuredHistoryModeRef.current : "classic",
          );
          setFirstScreenLine(available ? line : undefined);
        },
      );
      wsCleanupRef.current = cleanup;
    },
    [
      workspaceId,
      sessionName,
      write,
      clearReconnectTimers,
      syncViewportToBottom,
      startReconnectLoop,
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
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
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
        !wsCleanupRef.current &&
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
    if (wsCleanupRef.current || isSocketOpenOrConnecting(wsRef.current)) {
      return;
    }

    const timeout = setTimeout(() => {
      if (!xtermInstanceRef.current) return;
      if (connectionState !== "disconnected") return;
      if (reconnectCancelRef.current) return;
      if (wsCleanupRef.current || isSocketOpenOrConnecting(wsRef.current)) {
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
      virtualHistoryRef.current?.scrollToBottom();
      const ws = wsRef.current;
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(data);
      }
    },
    [writable],
  );

  const handleBinary = useCallback(
    (data: Uint8Array) => {
      if (!writable) return;
      const ws = wsRef.current;
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(data);
      }
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
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(encodeResize(cols, rows));
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
            wsCleanupRef.current?.();
            wsCleanupRef.current = null;
          } else {
            wsCleanupRef.current?.();
            wsCleanupRef.current = null;
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
        const ws = wsRef.current;
        if (ws?.readyState === WebSocket.OPEN) {
          virtualHistoryRef.current?.scrollToBottom();
          ws.send(text);
        }
      },
    }),
    [focus, clearReconnectTimers, writable],
  );

  const renderer = (
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
        onFocus={() => onTerminalFocusRef.current?.()}
        scrollbackLines={historyMode === "virtual" ? 0 : undefined}
        allowParentWheelScroll={historyMode === "virtual"}
      />
    </Suspense>
  );

  return (
    <div
      className={styles.wrapper}
      data-testid="terminal-wrapper"
      data-terminal-input
      data-terminal-renderer="xterm"
      data-history-mode={historyMode}
    >
      {historyMode === "virtual" ? (
        <VirtualTerminalHistory
          ref={virtualHistoryRef}
          sessionName={sessionName}
          isActive={isActive}
          recordingEpoch={recordingEpoch}
          firstScreenLine={firstScreenLine}
        >
          {renderer}
        </VirtualTerminalHistory>
      ) : (
        renderer
      )}
    </div>
  );
});
