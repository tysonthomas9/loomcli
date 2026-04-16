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

/** Wall-clock ceiling: if a reconnect doesn't succeed within this window, give up. */
const RECONNECT_TIMEOUT_MS = 30_000;

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error"
  | "crashed";

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

  const wsRef = useRef<WebSocket | null>(null);
  const wsCleanupRef = useRef<(() => void) | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const beingKilledRef = useRef(false);
  const hasConnectedRef = useRef(false);
  const sizeRef = useRef<{ cols: number; rows: number }>({ cols: 80, rows: 24 });

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

  const startReconnectLoop = useCallback(
    (config?: ReconnectConfig) => {
      clearReconnectTimers();
      onReconnectStateChangeRef.current?.("reconnecting");

      reconnectTimeoutRef.current = setTimeout(() => {
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        onReconnectStateChangeRef.current?.("expired");
      }, RECONNECT_TIMEOUT_MS);

      const cancel = startAutoReconnect(
        () =>
          new Promise<boolean>((resolve) => {
            doConnect({ onOutcome: resolve });
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
    // doConnect is declared below; captured via closure. We intentionally
    // don't include it in deps — its identity doesn't matter here since we
    // always read the latest via ref-free closure capture inside setTimeout.
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
        // onConnected
        () => {
          clearReconnectTimers();
          onReconnectStateChangeRef.current?.(null);
          // Send initial size — subsequent resizes flow through onResize below.
          const ws = wsRef.current;
          if (ws?.readyState === WebSocket.OPEN) {
            const { cols, rows } = sizeRef.current;
            ws.send(encodeResize(cols, rows));
          }
          opts?.onOutcome?.(true);
        },
        // onDisconnected — schedule a reconnect unless we're being killed.
        () => {
          if (beingKilledRef.current) {
            opts?.onOutcome?.(false);
            return;
          }
          opts?.onOutcome?.(false);
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
        onOutputRef.current,
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

  // Mount / teardown per session.
  useEffect(() => {
    beingKilledRef.current = false;
    hasConnectedRef.current = false;
    // Connection begins in the onReady handler (once wterm's WASM is loaded).
    return () => {
      clearReconnectTimers();
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
    };
  }, [sessionName, clearReconnectTimers]);

  const handleReady = useCallback(() => {
    doConnect();
  }, [doConnect]);

  const handleData = useCallback((data: string) => {
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(data);
    }
  }, []);

  const handleResize = useCallback((cols: number, rows: number) => {
    sizeRef.current = { cols, rows };
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
        doConnect();
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
    [doConnect, focus, clearReconnectTimers],
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
