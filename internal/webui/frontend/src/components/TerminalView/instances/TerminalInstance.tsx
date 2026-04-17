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
  },
  ref,
) {
  const { workspaceId } = useWorkspaceContext();
  const wtermRef = useRef<TerminalHandle | null>(null);

  const write = useCallback((data: string | Uint8Array) => {
    wtermRef.current?.write(data);
  }, []);
  const focus = useCallback(() => {
    wtermRef.current?.focus();
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
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");

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
      );
      wsCleanupRef.current = cleanup;
    },
    [
      workspaceId,
      sessionName,
      agentName,
      write,
      clearReconnectTimers,
      startReconnectLoop,
    ],
  );

  // Keep the reconnect loop pointing at the latest doConnect closure.
  doConnectRef.current = doConnect;

  // Mount / teardown per session.
  useEffect(() => {
    beingKilledRef.current = false;
    hasConnectedRef.current = false;
    // Connection normally begins in the onReady handler. If we're in a
    // StrictMode remount (wterm already fired onReady, and its cached
    // WASM means it won't fire again), re-kick the connection here —
    // otherwise the tab would stay stuck at "connecting" because the
    // prior cleanup cancelled its in-flight WebSocket.
    if (wtermReadyRef.current && ptyAlive !== false) {
      doConnectRef.current?.();
    }
    return () => {
      clearReconnectTimers();
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
    };
  }, [sessionName, clearReconnectTimers, ptyAlive]);

  // handleReady and the reconnect imperative method both read the latest
  // doConnect via doConnectRef so neither hands the wterm <Terminal> a new
  // onReady identity on every render.
  // Track wterm readiness so the mount effect can re-kick the connection
  // when React StrictMode double-invokes mount → unmount → remount. Without
  // this, the unmount cancels the in-flight connect but wterm's onReady
  // never fires again on remount (same component instance), leaving the tab
  // stuck in "connecting".
  const wtermReadyRef = useRef(false);
  const handleReady = useCallback(() => {
    wtermReadyRef.current = true;
    if (ptyAlive === false) {
      setConnectionState("session_ended");
      return;
    }
    doConnectRef.current?.();
  }, [ptyAlive]);

  const handleData = useCallback((data: string) => {
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(data);
    }
  }, []);

  const handleResize = useCallback((cols: number, rows: number) => {
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(encodeResize(cols, rows));
    }
  }, []);

  // Re-focus when this tab becomes active.
  useEffect(() => {
    if (isActive) {
      focus();
    }
  }, [isActive, focus]);

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
    <div className={styles.wrapper} data-testid="terminal-wrapper">
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
