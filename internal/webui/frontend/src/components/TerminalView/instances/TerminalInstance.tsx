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

import type { CursorProbe } from "../tabs/waitingState";
import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import { connectWebSocket, encodeResize } from "./terminalConnection";
import type { TerminalAttachFrame } from "./terminalControlFrame";
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

/**
 * A connection that opened and then closed again inside this window did not
 * recover anything: the attach worked, the shell behind it exited. The
 * backoff cannot see that on its own — `startAutoReconnect` treats every
 * successful open as recovery and resets, so a session whose launch command
 * fails on startup reconnects forever at roughly one respawn per second.
 *
 * Sized above a realistic launch: a failing `loom lead` writes its error and
 * exits around three seconds in, so the window must clear that with room to
 * spare. Legitimate short sessions do not land here — a shell the user exits
 * closes with 1000 and takes the `session_ended` path before this runs.
 */
const SHORT_LIVED_CONNECTION_MS = 10_000;

/**
 * Consecutive short-lived connections tolerated before we stop reconnecting
 * and surface `spawn_failed`. Three keeps a genuinely flaky network from
 * tripping it while still stopping a broken launch command within seconds.
 */
const MAX_SHORT_LIVED_CONNECTIONS = 3;

/**
 * Render the replacement boundary the user sees at the seam between the dead
 * session's buffer and the new shell's first output.
 *
 * Deliberately plain: dim, one line, no cursor addressing and no padding to
 * the terminal width, so a later reflow cannot corrupt it. It is a client-side
 * render, never PTY input.
 */
export function formatRestartBoundary(replacedAt?: string): string {
  const when = formatBoundaryTime(replacedAt);
  const label = when ? `server restarted ${when}` : "server restarted";
  return `\r\n\x1b[2m── ${label} · the previous process did not survive · this is a new shell ──\x1b[0m\r\n`;
}

/** Local HH:MM for an RFC3339 stamp; empty string when absent or unparseable. */
function formatBoundaryTime(replacedAt?: string): string {
  if (!replacedAt) return "";
  const parsed = new Date(replacedAt);
  if (Number.isNaN(parsed.getTime())) return "";
  const hours = String(parsed.getHours()).padStart(2, "0");
  const minutes = String(parsed.getMinutes()).padStart(2, "0");
  return `${hours}:${minutes}`;
}

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
  | "session_ended"
  // Every attach succeeds and the shell dies seconds later: the session's
  // launch command is failing on startup, so retrying only respawns it.
  // We stop and hand the user the affordance instead of looping.
  | "spawn_failed";

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
  /** User input was actually delivered to the PTY (not merely typed). */
  onInput?: (() => void) | undefined;
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
  attachable?: boolean | undefined;
  /** When true, stale PTYs are automatically replaced with a fresh shell. */
  autoStartStaleSession?: boolean | undefined;
  /** When false, unexpected disconnects stop at the reconnect affordance. */
  autoReconnect?: boolean | undefined;
  /**
   * Called with the RFC3339 replacement timestamp when the server tells us,
   * on attach, that this tab's shell was replaced because the server
   * restarted. The owner feeds it back into the tab metadata state, which is
   * the same source a reloaded page reads — so live-detected and reloaded
   * state converge on one code path.
   */
  onSessionReplaced?: ((replacedAt: string) => void) | undefined;
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
  /**
   * Cursor facts from the emulator, or null before the renderer is mounted.
   * Callers must treat null as "unknown" rather than guessing.
   */
  probeActivity: () => CursorProbe | null;
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
    onInput,
    onBackendCrash,
    onTerminalFocus,
    writable = true,
    attachable,
    autoStartStaleSession,
    autoReconnect = true,
    onSessionReplaced,
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
  // When the live socket opened, and how many consecutive sockets have opened
  // only to die inside SHORT_LIVED_CONNECTION_MS. Together they distinguish
  // "the shell keeps failing to start" from "the network is flaky", which the
  // reconnect backoff alone cannot tell apart.
  const connectedAtRef = useRef<number | null>(null);
  const shortLivedStreakRef = useRef(0);
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
  const onInputRef = useRef(onInput);
  onInputRef.current = onInput;
  const onBackendCrashRef = useRef(onBackendCrash);
  onBackendCrashRef.current = onBackendCrash;
  const onTerminalFocusRef = useRef(onTerminalFocus);
  onTerminalFocusRef.current = onTerminalFocus;
  const onSessionReplacedRef = useRef(onSessionReplaced);
  onSessionReplacedRef.current = onSessionReplaced;

  // The replacement already drawn in this pane's buffer. A reconnect attaches
  // again and the server re-announces the same replacement (with
  // `reattached: true`); redrawing on that would stack boundaries in a buffer
  // that is never unmounted.
  const drawnBoundaryRef = useRef<string | null>(null);

  const handleAttachFrame = useCallback(
    (frame: TerminalAttachFrame) => {
      const replacedAt = frame.replaced_at ?? "";
      // `replaced` is true only when this attach IS the replacement. A reattach
      // carries the stored timestamp so a late client learns the fact, but the
      // durable marker (PUPPET-28) — not a boundary line — is what shows it.
      if (frame.reattached || !frame.replaced) return;
      if (drawnBoundaryRef.current === replacedAt) return;
      drawnBoundaryRef.current = replacedAt;
      // Same `write` the server's output goes through, so the boundary lands at
      // the seam and inherits the pre-renderer pending-write drain.
      write(formatRestartBoundary(frame.replaced_at));
      if (replacedAt !== "") {
        onSessionReplacedRef.current?.(replacedAt);
      }
    },
    [write],
  );

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
          connectedAtRef.current = Date.now();
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
          // A socket that opened and died again immediately spent its life
          // on a shell that could not start. Count the streak before the
          // reconnect decision below; a connection that actually lived
          // clears it, and one that never opened is a transport failure the
          // backoff's own attempt counter already bounds.
          const openedAt = connectedAtRef.current;
          connectedAtRef.current = null;
          if (openedAt !== null) {
            shortLivedStreakRef.current =
              Date.now() - openedAt < SHORT_LIVED_CONNECTION_MS
                ? shortLivedStreakRef.current + 1
                : 0;
          }
          if (beingKilledRef.current) return;
          if (shortLivedStreakRef.current >= MAX_SHORT_LIVED_CONNECTIONS) {
            // Respawning again would only repeat the failure and bury the
            // shell's own error message under another boundary banner.
            clearReconnectTimers();
            onReconnectStateChangeRef.current?.(null);
            setConnectionState("spawn_failed");
            return;
          }
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
        handleAttachFrame,
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
      handleAttachFrame,
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
      isActiveRef.current && (attachable !== false || autoStartStaleSession);
    if (canReconnect && xtermInstanceRef.current != null) {
      doConnectRef.current?.();
    }
    return () => {
      beingKilledRef.current = true;
      // Do NOT null xtermInstanceRef here: the XTermRenderer child owns its
      // handle and nulls it via handleXTermDispose on real disposal. Nulling it
      // on a mere effect re-run (e.g. an attachable transition) strands the
      // tab with no path back — no onReady to repopulate the ref, no reconnect.
      pendingRendererWritesRef.current = [];
      clearReconnectTimers();
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
    };
  }, [sessionName, clearReconnectTimers, attachable, autoStartStaleSession]);

  useEffect(() => {
    if (attachable === false && !autoStartStaleSession) {
      setConnectionState("session_ended");
    }
  }, [attachable, autoStartStaleSession]);

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
      if (attachable === false && !autoStartStaleSession) {
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
    [attachable, autoStartStaleSession],
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
    if (attachable === false && !autoStartStaleSession) return;
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
    attachable,
    autoStartStaleSession,
    connectionState,
    readyVersion,
  ]);

  // onInput fires only when the keystroke actually reached the PTY: input a
  // read-only tab dropped, or that died on a closed socket, must not retire a
  // waiting-for-input badge the user has not in fact answered.
  const handleData = useCallback(
    (data: string) => {
      if (!writable) return;
      const ws = wsRef.current;
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(data);
        onInputRef.current?.();
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
        onInputRef.current?.();
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
        // An explicit user retry starts the flap budget over: the operator
        // may have just repaired whatever the launch command was failing on.
        shortLivedStreakRef.current = 0;
        connectedAtRef.current = null;
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
      probeActivity: () => xtermInstanceRef.current?.probeActivity() ?? null,
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
          onFocus={() => onTerminalFocusRef.current?.()}
        />
      </Suspense>
    </div>
  );
});
