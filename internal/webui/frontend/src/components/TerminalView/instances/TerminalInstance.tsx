/**
 * TerminalInstance component.
 *
 * A single wterm-backed terminal pane bound to one PTY-backed WebSocket.
 * The wterm renderer handles grid layout, scrolling, DOM selection, native
 * copy/paste, and resize observation; this component owns the WebSocket
 * lifecycle and reconnect state machine.
 *
 * The imperative handle is deliberately narrow (disconnect, reconnect,
 * focus, pasteText) so parent components stay decoupled from renderer
 * internals.
 */

import type { WTerm } from "@wterm/dom";
import { Terminal, type TerminalHandle } from "@wterm/react";
import "@wterm/react/css";
import {
  forwardRef,
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
import styles from "./TerminalInstance.module.css";

/** Stricter backoff for initial connection failures (e.g. session not found). */
const INITIAL_CONNECT_CONFIG: ReconnectConfig = {
  maxAttempts: 3,
  baseDelay: 3000,
  maxDelay: 15000,
  jitterFactor: 0.5,
};

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
const SCROLL_BOTTOM_THRESHOLD_PX = 24;

function isSocketOpenOrConnecting(ws: WebSocket | null): boolean {
  return (
    ws?.readyState === WebSocket.OPEN || ws?.readyState === WebSocket.CONNECTING
  );
}

type WTermRenderAdapter = {
  _doRender?: () => void;
};

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
  onConnectionStateChange?: (
    state: ConnectionState,
    hasConnected: boolean,
  ) => void;
  onReconnectStateChange?: (state: ReconnectOverlayState) => void;
  onOutput?: () => void;
  onBackendCrash?: (reason: string) => void;
  onTerminalFocus?: (() => void) | undefined;
  /** When set, connects to the agent terminal WebSocket instead of the regular terminal. */
  agentName?: string | undefined;
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
    agentName,
    ptyAlive,
    autoStartStaleSession,
  },
  ref,
) {
  const { workspaceId } = useWorkspaceContext();
  const wtermRef = useRef<TerminalHandle | null>(null);
  const wtermInstanceRef = useRef<WTerm | null>(null);
  const pendingRendererWritesRef = useRef<Array<string | Uint8Array>>([]);
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const terminalSizeRef = useRef({ cols: 80, rows: 24 });

  const forceRendererPaint = useCallback((wt: WTerm | null) => {
    const render = (wt as unknown as WTermRenderAdapter | null)?._doRender;
    if (typeof render === "function") {
      render.call(wt);
    }
  }, []);

  const getViewportElement = useCallback((): HTMLElement | null => {
    return wrapperRef.current?.querySelector<HTMLElement>(".wterm") ?? null;
  }, []);

  const distanceFromBottom = useCallback((el: HTMLElement): number => {
    return Math.max(0, el.scrollHeight - el.scrollTop - el.clientHeight);
  }, []);

  const isViewportNearBottom = useCallback(
    (el: HTMLElement): boolean =>
      distanceFromBottom(el) <= SCROLL_BOTTOM_THRESHOLD_PX,
    [distanceFromBottom],
  );

  const disableRendererBottomFollow = useCallback(() => {
    const instance = wtermRef.current?.instance as unknown as {
      _shouldScrollToBottom?: boolean;
    } | null;
    if (instance) {
      instance._shouldScrollToBottom = false;
    }
  }, []);

  const syncViewportToBottom = useCallback(() => {
    const el = getViewportElement();
    if (!el) return;
    requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
  }, [getViewportElement]);

  const write = useCallback(
    (data: string | Uint8Array) => {
      const wt = wtermInstanceRef.current;
      if (wt) {
        wt.write(data);
        forceRendererPaint(wt);
        return;
      }
      pendingRendererWritesRef.current.push(data);
    },
    [forceRendererPaint],
  );
  const focus = useCallback(() => {
    wtermInstanceRef.current?.focus();
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
  // workspaceId / sessionName / agentName re-memoises.
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
        // onConnected — wterm's autoResize will fire onResize with the true
        // container size, which sends the first encodeResize. Don't preempt
        // with a stale 80×24 seed.
        () => {
          clearReconnectTimers();
          onReconnectStateChangeRef.current?.(null);
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
          if (!reconnectCancelRef.current) {
            const config = hasConnectedRef.current
              ? undefined
              : INITIAL_CONNECT_CONFIG;
            startReconnectLoop(config);
          }
        },
        () => onOutputRef.current?.(),
        (reason) => onBackendCrashRef.current?.(reason),
        agentName,
        // onSessionKilled — server told us the session is gone; do not reconnect.
        () => {
          beingKilledRef.current = true;
          clearReconnectTimers();
          onReconnectStateChangeRef.current?.(null);
        },
        terminalSizeRef.current,
      );
      wsCleanupRef.current = cleanup;
    },
    [
      workspaceId,
      sessionName,
      agentName,
      write,
      clearReconnectTimers,
      syncViewportToBottom,
      startReconnectLoop,
    ],
  );

  // Keep the reconnect loop pointing at the latest doConnect closure.
  doConnectRef.current = doConnect;

  // Mount / teardown per session.
  useEffect(() => {
    const el = getViewportElement();
    if (!el) return;

    const handleWheel = (event: WheelEvent) => {
      const maxScrollTop = Math.max(0, el.scrollHeight - el.clientHeight);
      if (maxScrollTop <= 0) return;

      const deltaY =
        event.deltaMode === WheelEvent.DOM_DELTA_LINE
          ? event.deltaY * 16
          : event.deltaMode === WheelEvent.DOM_DELTA_PAGE
            ? event.deltaY * el.clientHeight
            : event.deltaY;
      const nextScrollTop = Math.min(
        maxScrollTop,
        Math.max(0, el.scrollTop + deltaY),
      );
      if (nextScrollTop === el.scrollTop) return;

      event.preventDefault();
      el.scrollTop = nextScrollTop;
      if (!isViewportNearBottom(el)) {
        disableRendererBottomFollow();
      }
    };

    el.addEventListener("wheel", handleWheel, { passive: false });
    return () => {
      el.removeEventListener("wheel", handleWheel);
    };
  }, [
    disableRendererBottomFollow,
    getViewportElement,
    isViewportNearBottom,
    sessionName,
  ]);

  useEffect(() => {
    beingKilledRef.current = false;
    hasConnectedRef.current = false;
    initialViewportSyncDoneRef.current = false;
    // Connection normally begins in the onReady handler. If we're in a
    // StrictMode remount (wterm already fired onReady, and its cached
    // WASM means it won't fire again), re-kick the connection here —
    // otherwise the tab would stay stuck at "connecting" because the
    // prior cleanup cancelled its in-flight WebSocket. The same hazard
    // applies to wtermInstanceRef: cleanup nulled it, but onReady won't
    // fire to re-set it, so write() would silently drop every replayed
    // byte. Restore from the still-alive TerminalHandle.
    if (
      wtermReadyRef.current &&
      (ptyAlive !== false || autoStartStaleSession)
    ) {
      if (wtermInstanceRef.current == null) {
        const instance = wtermRef.current?.instance as WTerm | undefined;
        if (instance) {
          wtermInstanceRef.current = instance;
        }
      }
      doConnectRef.current?.();
    }
    return () => {
      wtermInstanceRef.current = null;
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

  // handleReady and the reconnect imperative method both read the latest
  // doConnect via doConnectRef so neither hands the wterm <Terminal> a new
  // onReady identity on every render.
  // Track wterm readiness so the mount effect can re-kick the connection
  // when React StrictMode double-invokes mount → unmount → remount. Without
  // this, the unmount cancels the in-flight connect but wterm's onReady
  // never fires again on remount (same component instance), leaving the tab
  // stuck in "connecting".
  const wtermReadyRef = useRef(false);
  const measureTerminalSize = useCallback(
    (wt: WTerm): { cols: number; rows: number } | null => {
      const el = wt.element;
      const grid = el.querySelector<HTMLElement>(".term-grid");
      if (!grid) return null;

      const probe = document.createElement("span");
      probe.className = "term-cell";
      probe.textContent = "W";
      probe.style.position = "absolute";
      probe.style.visibility = "hidden";
      grid.appendChild(probe);

      const rect = probe.getBoundingClientRect();
      probe.remove();

      if (rect.width <= 0 || rect.height <= 0) return null;

      const cols = Math.max(1, Math.floor(el.clientWidth / rect.width));
      const rows = Math.max(1, Math.floor(el.clientHeight / rect.height));
      return { cols, rows };
    },
    [],
  );

  const handleReady = useCallback(
    (wt: WTerm) => {
      wtermReadyRef.current = true;
      wtermInstanceRef.current = wt;
      const pendingWrites = pendingRendererWritesRef.current.splice(0);
      for (const data of pendingWrites) {
        wt.write(data);
      }
      forceRendererPaint(wt);
      setReadyVersion((value) => value + 1);
      if (ptyAlive === false && !autoStartStaleSession) {
        setConnectionState("session_ended");
        return;
      }
      const measured = measureTerminalSize(wt);
      if (measured) {
        terminalSizeRef.current = measured;
        if (wt.cols !== measured.cols || wt.rows !== measured.rows) {
          wt.resize(measured.cols, measured.rows);
        }
      }
      if (!wsCleanupRef.current && !isSocketOpenOrConnecting(wsRef.current)) {
        doConnectRef.current?.();
      }
    },
    [measureTerminalSize, ptyAlive, autoStartStaleSession],
  );

  // In the desktop shell, a terminal can be mounted before the renderer's
  // ready event fires. Kick one connection attempt as soon as the pane is
  // active; output is still written once the renderer instance is available.
  useEffect(() => {
    if (!isActive) return;
    if (ptyAlive === false && !autoStartStaleSession) return;
    if (connectionState !== "disconnected") return;
    if (reconnectCancelRef.current) return;
    if (wsCleanupRef.current || isSocketOpenOrConnecting(wsRef.current)) {
      return;
    }

    const timeout = setTimeout(() => {
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
  }, [isActive, ptyAlive, connectionState, readyVersion]);

  const handleData = useCallback((data: string) => {
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(data);
    }
  }, []);

  const handleResize = useCallback((cols: number, rows: number) => {
    terminalSizeRef.current = { cols, rows };
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(encodeResize(cols, rows));
    }
  }, []);

  // Tauri can reveal an already-mounted terminal after it measured while
  // hidden. Resync once visible so the PTY has the real grid size and focus.
  useEffect(() => {
    if (!isActive) return;

    let cancelled = false;
    let firstFrame = 0;
    let secondFrame = 0;

    const syncActiveLayout = () => {
      if (cancelled) return;
      const wt = wtermInstanceRef.current;
      if (!wt) return;
      const measured = measureTerminalSize(wt);
      if (measured) {
        terminalSizeRef.current = measured;
        if (wt.cols !== measured.cols || wt.rows !== measured.rows) {
          wt.resize(measured.cols, measured.rows);
        }
        handleResize(measured.cols, measured.rows);
      }
      forceRendererPaint(wt);
      focus();
      syncViewportToBottom();
    };

    firstFrame = requestAnimationFrame(() => {
      syncActiveLayout();
      secondFrame = requestAnimationFrame(syncActiveLayout);
    });

    return () => {
      cancelled = true;
      cancelAnimationFrame(firstFrame);
      cancelAnimationFrame(secondFrame);
    };
  }, [
    isActive,
    readyVersion,
    focus,
    forceRendererPaint,
    handleResize,
    measureTerminalSize,
    syncViewportToBottom,
  ]);

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
        const ws = wsRef.current;
        if (ws?.readyState === WebSocket.OPEN) {
          ws.send(text);
        }
      },
    }),
    [focus, clearReconnectTimers],
  );

  return (
    <div
      ref={wrapperRef}
      className={styles.wrapper}
      data-testid="terminal-wrapper"
    >
      <Terminal
        ref={wtermRef}
        cols={80}
        rows={24}
        autoResize
        onReady={handleReady}
        onData={handleData}
        onResize={handleResize}
        className={styles.container}
      />
    </div>
  );
});
