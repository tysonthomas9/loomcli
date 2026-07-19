/**
 * TerminalInstance component.
 *
 * A single terminal pane bound to one PTY-backed WebSocket. Claude-backed
 * panes use xterm.js; every other backend stays on wterm. This component owns
 * the shared WebSocket lifecycle and reconnect state machine.
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
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import { getTerminalConfig } from "@/hooks/api";
import {
  TERMINAL_FONT_CHANGE_EVENT,
  type TerminalFontChangeDetail,
} from "@/hooks/terminal/useTerminalFont";
import { useWorkspaceContext } from "@/hooks/workspace";
import {
  startAutoReconnect,
  type ReconnectConfig,
  type ReconnectState,
} from "@/utils/reconnectBackoff";

import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import { connectWebSocket, encodeResize } from "./terminalConnection";
import { terminalRendererForBackend } from "./terminalRenderer";
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
  /** Canonical backend name. Only exact `claude` values select xterm.js. */
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
    backendName,
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
  const useXTerm = terminalRendererForBackend(backendName) === "xterm";
  const wtermRef = useRef<TerminalHandle | null>(null);
  const wtermInstanceRef = useRef<WTerm | null>(null);
  const xtermInstanceRef = useRef<XTermRendererHandle | null>(null);
  const pendingRendererWritesRef = useRef<Array<string | Uint8Array>>([]);
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const terminalSizeRef = useRef({ cols: 80, rows: 24 });
  const shouldFollowBottomRef = useRef(true);

  const forceRendererPaint = useCallback((wt: WTerm | null) => {
    const render = (wt as unknown as WTermRenderAdapter | null)?._doRender;
    if (typeof render === "function") {
      render.call(wt);
    }
  }, []);

  const getViewportElement = useCallback((): HTMLElement | null => {
    if (useXTerm) return null;
    return wrapperRef.current?.querySelector<HTMLElement>(".wterm") ?? null;
  }, [useXTerm]);

  const distanceFromBottom = useCallback((el: HTMLElement): number => {
    return Math.max(0, el.scrollHeight - el.scrollTop - el.clientHeight);
  }, []);

  const isViewportNearBottom = useCallback(
    (el: HTMLElement): boolean =>
      distanceFromBottom(el) <= SCROLL_BOTTOM_THRESHOLD_PX,
    [distanceFromBottom],
  );

  const scrollWTermToBottom = useCallback(
    (wt: WTerm) => {
      const el = wt.element;
      shouldFollowBottomRef.current = true;
      const sync = () => {
        forceRendererPaint(wt);
        el.scrollTop = el.scrollHeight;
      };
      sync();
      requestAnimationFrame(() => {
        sync();
        requestAnimationFrame(sync);
      });
    },
    [forceRendererPaint],
  );

  const syncViewportToBottom = useCallback(() => {
    if (useXTerm) {
      xtermInstanceRef.current?.scrollToBottom();
      return;
    }
    const wt = wtermInstanceRef.current;
    if (wt) {
      scrollWTermToBottom(wt);
      return;
    }
    const el = getViewportElement();
    if (!el) return;
    shouldFollowBottomRef.current = true;
    requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
  }, [getViewportElement, scrollWTermToBottom, useXTerm]);

  const write = useCallback(
    (data: string | Uint8Array) => {
      if (useXTerm) {
        const xterm = xtermInstanceRef.current;
        if (xterm) {
          xterm.write(data);
          return;
        }
        pendingRendererWritesRef.current.push(data);
        return;
      }
      const wt = wtermInstanceRef.current;
      if (wt) {
        // Keep wterm's own write-time follow decision aligned with Loom's
        // user-intent tracking. A near-bottom viewport can be a few pixels off
        // because rows are integral-height; normalize it before wterm latches
        // its private follow flag instead of reaching into renderer internals.
        if (shouldFollowBottomRef.current && isActiveRef.current) {
          wt.element.scrollTop = wt.element.scrollHeight;
        }
        wt.write(data);
        forceRendererPaint(wt);
        return;
      }
      pendingRendererWritesRef.current.push(data);
    },
    [forceRendererPaint, useXTerm],
  );
  const focus = useCallback(() => {
    if (useXTerm) {
      xtermInstanceRef.current?.focus();
    } else {
      wtermInstanceRef.current?.focus();
    }
  }, [useXTerm]);

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
  const [wtermSize, setWTermSize] = useState({ cols: 80, rows: 24 });
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
      shouldFollowBottomRef.current = isViewportNearBottom(el);
    };

    el.addEventListener("wheel", handleWheel, { passive: false });
    return () => {
      el.removeEventListener("wheel", handleWheel);
    };
  }, [getViewportElement, isViewportNearBottom, sessionName]);

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
    const canReconnect =
      isActiveRef.current && (ptyAlive !== false || autoStartStaleSession);
    if (canReconnect && !useXTerm && wtermReadyRef.current) {
      if (wtermInstanceRef.current == null) {
        const instance = wtermRef.current?.instance as WTerm | undefined;
        if (instance) {
          wtermInstanceRef.current = instance;
        }
      }
      doConnectRef.current?.();
    } else if (canReconnect && useXTerm && xtermInstanceRef.current != null) {
      // The lazy xterm renderer is not remounted on a ptyAlive-driven re-run,
      // so its onReady won't fire again to repopulate the ref or reconnect.
      // Its handle is still alive (handleXTermDispose owns disposal), so
      // re-kick the connection the cleanup below tore down — the same recovery
      // the wterm branch already had.
      doConnectRef.current?.();
    }
    return () => {
      beingKilledRef.current = true;
      wtermInstanceRef.current = null;
      // Do NOT null xtermInstanceRef here: the XTermRenderer child owns its
      // handle and nulls it via handleXTermDispose on real disposal. Nulling it
      // on a mere effect re-run (e.g. a ptyAlive transition) stranded a Claude
      // tab with no path back — no onReady to repopulate the ref, no reconnect.
      pendingRendererWritesRef.current = [];
      clearReconnectTimers();
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
    };
  }, [
    sessionName,
    clearReconnectTimers,
    ptyAlive,
    autoStartStaleSession,
    useXTerm,
  ]);

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
      if (el.clientWidth <= 0 || el.clientHeight <= 0) return null;
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
      // @wterm/react locks a fixed pixel height when autoResize is disabled.
      // Loom owns the pane geometry, so restore the flex-driven height before
      // measuring and before replaying any buffered terminal output.
      wt.element.style.height = "100%";
      const measured = measureTerminalSize(wt);
      if (measured) {
        terminalSizeRef.current = measured;
        setWTermSize(measured);
        if (wt.cols !== measured.cols || wt.rows !== measured.rows) {
          wt.resize(measured.cols, measured.rows);
        }
      }
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
      if (
        isActiveRef.current &&
        !wsCleanupRef.current &&
        !isSocketOpenOrConnecting(wsRef.current)
      ) {
        doConnectRef.current?.();
      }
    },
    [forceRendererPaint, measureTerminalSize, ptyAlive, autoStartStaleSession],
  );

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
    // Claude's controlled harness sizes its inner PTY from the first WebSocket
    // attachment. Wait for the lazy xterm renderer to fit the visible pane so
    // that first attachment does not permanently seed an 80x24 inner grid.
    if (useXTerm && !xtermInstanceRef.current) return;
    if (connectionState !== "disconnected") return;
    if (reconnectCancelRef.current) return;
    if (wsCleanupRef.current || isSocketOpenOrConnecting(wsRef.current)) {
      return;
    }

    const timeout = setTimeout(() => {
      if (useXTerm && !xtermInstanceRef.current) return;
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
    useXTerm,
  ]);

  const handleData = useCallback(
    (data: string) => {
      if (!writable) return;
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
    // Renderers can observe display:none as a tiny sentinel grid. Keep those
    // inactive measurements out of both canonical reconnect state and the PTY;
    // activation performs a fresh visible fit for either renderer.
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

  // Loom is the single owner of wterm layout. wterm's built-in observer sees
  // display:none route transitions as a real 0x0 resize, clamps that to 1x1,
  // and reflows both the local buffer and backing PTY before the route is
  // visible again. Measuring only active, non-zero panes prevents inactive
  // routes from corrupting terminal state and gives window, split-pane, font,
  // and activation resize paths one consistent policy.
  const syncWTermLayout = useCallback(
    (wt: WTerm): boolean => {
      if (!isActiveRef.current) return false;
      const measured = measureTerminalSize(wt);
      if (!measured) return false;

      const el = wt.element;
      const shouldFollow =
        shouldFollowBottomRef.current || isViewportNearBottom(el);
      terminalSizeRef.current = measured;
      setWTermSize((current) =>
        current.cols === measured.cols && current.rows === measured.rows
          ? current
          : measured,
      );

      if (wt.cols !== measured.cols || wt.rows !== measured.rows) {
        if (shouldFollow) el.scrollTop = el.scrollHeight;
        // WTerm synchronously invokes onResize, which updates the PTY exactly
        // once through handleResize. Do not call handleResize a second time.
        wt.resize(measured.cols, measured.rows);
      }

      if (shouldFollow) {
        scrollWTermToBottom(wt);
      } else {
        forceRendererPaint(wt);
      }
      return true;
    },
    [
      forceRendererPaint,
      isViewportNearBottom,
      measureTerminalSize,
      scrollWTermToBottom,
    ],
  );

  useEffect(() => {
    if (useXTerm || !isActive || typeof ResizeObserver === "undefined") return;
    const wrapper = wrapperRef.current;
    if (!wrapper) return;

    let frame = 0;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1];
      if (
        !entry ||
        entry.contentRect.width <= 0 ||
        entry.contentRect.height <= 0
      ) {
        return;
      }
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const wt = wtermInstanceRef.current;
        if (wt) syncWTermLayout(wt);
      });
    });
    observer.observe(wrapper);
    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
    };
  }, [isActive, readyVersion, syncWTermLayout, useXTerm]);

  // Tauri can reveal an already-mounted terminal after it measured while
  // hidden. Resync once visible so the PTY has the real grid size and focus.
  useEffect(() => {
    if (!isActive) return;

    let cancelled = false;
    let firstFrame = 0;
    let secondFrame = 0;
    const focusTimers: Array<ReturnType<typeof setTimeout>> = [];

    const syncActiveLayout = () => {
      if (cancelled) return;
      if (useXTerm) {
        const xterm = xtermInstanceRef.current;
        if (!xterm) return;
        const measured = xterm.fit();
        if (measured) {
          terminalSizeRef.current = measured;
          handleResize(measured.cols, measured.rows);
        }
        focus();
        syncViewportToBottom();
        return;
      }
      const wt = wtermInstanceRef.current;
      if (!wt) return;
      if (!syncWTermLayout(wt)) return;
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
  }, [
    isActive,
    readyVersion,
    focus,
    handleResize,
    syncWTermLayout,
    syncViewportToBottom,
    useXTerm,
  ]);

  // Re-measure the grid when font prefs change so cols/rows stay accurate.
  useEffect(() => {
    const onFontChange = () => {
      if (useXTerm) return;
      const wt = wtermInstanceRef.current;
      if (!wt) return;
      syncWTermLayout(wt);
    };

    const handler = (event: Event) => {
      const detail = (event as CustomEvent<TerminalFontChangeDetail>).detail;
      if (!detail) return;
      onFontChange();
    };

    window.addEventListener(TERMINAL_FONT_CHANGE_EVENT, handler);
    return () =>
      window.removeEventListener(TERMINAL_FONT_CHANGE_EVENT, handler);
  }, [syncWTermLayout, useXTerm]);

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
          ws.send(text);
        }
      },
    }),
    [focus, clearReconnectTimers, writable],
  );

  return (
    <div
      ref={wrapperRef}
      className={styles.wrapper}
      data-testid="terminal-wrapper"
      data-terminal-input
      data-terminal-renderer={useXTerm ? "xterm" : "wterm"}
    >
      {useXTerm ? (
        <Suspense
          fallback={
            <div
              className={styles.xtermContainer}
              data-testid="xterm-loading"
            />
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
          />
        </Suspense>
      ) : (
        <Terminal
          ref={wtermRef}
          cols={wtermSize.cols}
          rows={wtermSize.rows}
          autoResize={false}
          onReady={handleReady}
          onData={handleData}
          onResize={handleResize}
          className={styles.container}
          style={{ height: "100%" }}
        />
      )}
    </div>
  );
});
