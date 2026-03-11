/**
 * TerminalPanel component.
 * Slide-out panel with an embedded xterm.js terminal connected via WebSocket.
 */

import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal } from "@xterm/xterm";
import { useEffect, useRef, useState, useCallback } from "react";

import { get, post } from "@/api/client";
import {
  startAutoReconnect,
  DEFAULT_RECONNECT_CONFIG,
  type ReconnectState,
} from "@/utils/reconnectBackoff";

import "@xterm/xterm/css/xterm.css";
import styles from "./TerminalPanel.module.css";

export interface TerminalPanelProps {
  isOpen: boolean;
  onClose: () => void;
}

type ConnectionState = "disconnected" | "connecting" | "connected";

const TERMINAL_SESSION = "talk-to-lead";

/**
 * Fetch a one-time terminal auth token from the server.
 * Returns null on failure — the WebSocket connection will be rejected by the server.
 */
async function fetchTerminalToken(): Promise<string | null> {
  try {
    const resp = await get<{ token: string }>(
      `/api/terminal/token?session=${TERMINAL_SESSION}`, // allow-url
    );
    return resp.token;
  } catch {
    return null;
  }
}

/**
 * Build the WebSocket URL for the terminal relay endpoint.
 */
function buildWsUrl(token: string | null): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  let url = `${proto}//${window.location.host}/api/terminal/ws?session=${TERMINAL_SESSION}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return url;
}

/**
 * Encode a resize message per the binary frame protocol.
 * Byte 0 = 0x01, then cols as uint16 BE, then rows as uint16 BE.
 */
function encodeResize(cols: number, rows: number): ArrayBuffer {
  const buf = new ArrayBuffer(5);
  const view = new DataView(buf);
  view.setUint8(0, 0x01);
  view.setUint16(1, cols, false);
  view.setUint16(3, rows, false);
  return buf;
}

/**
 * Connect a Terminal instance to a WebSocket, returning a cleanup function.
 * Fetches a one-time terminal token before establishing the connection.
 * Used for both initial connection and reconnection.
 */
function connectWebSocket(
  terminal: Terminal,
  fitAddon: FitAddon,
  wsRef: React.MutableRefObject<WebSocket | null>,
  setConnectionState: (s: ConnectionState) => void,
  setWasConnected: (v: boolean) => void,
  onConnected?: () => void,
  onDisconnected?: () => void,
): () => void {
  setConnectionState("connecting");

  let cancelled = false;

  // Fetch one-time token, then open WebSocket
  fetchTerminalToken()
    .then((token) => {
      if (cancelled) return;

      const ws = new WebSocket(buildWsUrl(token));
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";

      ws.onopen = () => {
        setConnectionState("connected");
        setWasConnected(true);
        fitAddon.fit();
        ws.send(encodeResize(terminal.cols, terminal.rows));
        onConnected?.();
      };

      ws.onmessage = (ev: MessageEvent) => {
        if (typeof ev.data === "string") {
          terminal.write(ev.data);
        } else if (ev.data instanceof ArrayBuffer) {
          terminal.write(new Uint8Array(ev.data));
        }
      };

      ws.onclose = () => {
        setConnectionState("disconnected");
        onDisconnected?.();
      };

      ws.onerror = () => {
        setConnectionState("disconnected");
      };

      const onDataDisposable = terminal.onData((data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(data);
        }
      });

      // Store cleanup for the data listener so the outer cleanup can call it
      wsCleanupInner = () => {
        onDataDisposable.dispose();
        if (
          ws.readyState === WebSocket.OPEN ||
          ws.readyState === WebSocket.CONNECTING
        ) {
          ws.close(1000);
        }
        wsRef.current = null;
      };
    })
    .catch(() => {
      if (!cancelled) {
        setConnectionState("disconnected");
        onDisconnected?.();
      }
    });

  // Inner cleanup set by the async token fetch
  let wsCleanupInner: (() => void) | null = null;

  return () => {
    cancelled = true;
    if (wsCleanupInner) {
      wsCleanupInner();
    } else {
      wsRef.current = null;
    }
  };
}

/**
 * Format seconds remaining for the reconnect countdown.
 */
function formatCountdown(nextRetryAt: number): string {
  const remaining = Math.max(0, Math.ceil((nextRetryAt - Date.now()) / 1000));
  return `${remaining}s`;
}

export function TerminalPanel({
  isOpen,
  onClose,
}: TerminalPanelProps): JSX.Element {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsCleanupRef = useRef<(() => void) | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);
  const panelRef = useRef<HTMLElement>(null);

  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const [wasConnected, setWasConnected] = useState(false);
  const [reconnectState, setReconnectState] = useState<ReconnectState>({
    attempt: 0,
    nextRetryAt: null,
    gaveUp: false,
  });
  const [countdown, setCountdown] = useState<string | null>(null);

  // Countdown timer: update every second while waiting for next retry
  useEffect(() => {
    if (!reconnectState.nextRetryAt || reconnectState.gaveUp) {
      setCountdown(null);
      return;
    }

    setCountdown(formatCountdown(reconnectState.nextRetryAt));

    const intervalId = setInterval(() => {
      const remaining = reconnectState.nextRetryAt! - Date.now();
      if (remaining <= 0) {
        setCountdown(null);
        clearInterval(intervalId);
      } else {
        setCountdown(formatCountdown(reconnectState.nextRetryAt!));
      }
    }, 1000);

    return () => clearInterval(intervalId);
  }, [reconnectState.nextRetryAt, reconnectState.gaveUp]);

  // Terminal lifecycle: create on open, destroy on close
  useEffect(() => {
    if (!isOpen) return;

    const container = termRef.current;
    if (!container) return;

    // Guard against async callbacks (ws.onclose) firing after cleanup
    let mounted = true;

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      scrollback: 5000,
      smoothScrollDuration: 120,
      theme: {
        background: "#1e1e1e",
        foreground: "#d4d4d4",
        cursor: "#d4d4d4",
      },
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();

    terminal.loadAddon(fitAddon);
    terminal.loadAddon(webLinksAddon);

    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;

    terminal.open(container);

    // Delay initial fit until after slide animation completes
    const fitTimer = setTimeout(() => {
      fitAddon.fit();
    }, 350);

    /**
     * Initiate a WebSocket connection with auto-reconnect callbacks wired in.
     */
    function doConnect(): void {
      wsCleanupRef.current?.();
      const cleanup = connectWebSocket(
        terminal,
        fitAddon,
        wsRef,
        setConnectionState,
        setWasConnected,
        () => {
          // onConnected: cancel auto-reconnect, reset state
          reconnectCancelRef.current?.();
          reconnectCancelRef.current = null;
          setReconnectState({ attempt: 0, nextRetryAt: null, gaveUp: false });
        },
        () => {
          // onDisconnected: start auto-reconnect if not already running
          if (!mounted || reconnectCancelRef.current) return;
          const cancel = startAutoReconnect(() => {
            if (!mounted) return true; // stop loop if unmounted
            doConnect();
            return false; // async — success handled by onConnected
          }, setReconnectState);
          reconnectCancelRef.current = cancel;
        },
      );
      wsCleanupRef.current = cleanup;
    }

    // Connect WebSocket
    setWasConnected(false);
    doConnect();

    // ResizeObserver with debounce for ongoing resize
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        if (fitAddonRef.current && terminalRef.current) {
          fitAddonRef.current.fit();
          const currentWs = wsRef.current;
          if (currentWs && currentWs.readyState === WebSocket.OPEN) {
            currentWs.send(
              encodeResize(terminalRef.current.cols, terminalRef.current.rows),
            );
          }
        }
      }, 100);
    });
    observer.observe(container);

    return () => {
      mounted = false;

      clearTimeout(fitTimer);
      clearTimeout(resizeTimer);
      observer.disconnect();

      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;

      wsCleanupRef.current?.();
      wsCleanupRef.current = null;

      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;

      setConnectionState("disconnected");
      setReconnectState({ attempt: 0, nextRetryAt: null, gaveUp: false });
    };
  }, [isOpen]);

  // Reconnect handler: cancel auto-reconnect, reset state, connect fresh
  const handleReconnect = useCallback(() => {
    const terminal = terminalRef.current;
    const fitAddon = fitAddonRef.current;
    if (!terminal || !fitAddon) return;

    // Cancel any in-progress auto-reconnect
    reconnectCancelRef.current?.();
    reconnectCancelRef.current = null;
    setReconnectState({ attempt: 0, nextRetryAt: null, gaveUp: false });

    // Clean up previous connection and create new one
    wsCleanupRef.current?.();
    const cleanupWs = connectWebSocket(
      terminal,
      fitAddon,
      wsRef,
      setConnectionState,
      setWasConnected,
      () => {
        // onConnected: cancel auto-reconnect (if re-armed by a prior close)
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        setReconnectState({ attempt: 0, nextRetryAt: null, gaveUp: false });
      },
      () => {
        // onDisconnected: start auto-reconnect
        if (reconnectCancelRef.current) return;
        const cancel = startAutoReconnect(() => {
          handleReconnect();
          return false;
        }, setReconnectState);
        reconnectCancelRef.current = cancel;
      },
    );
    wsCleanupRef.current = cleanupWs;
  }, []);

  // Listen for backend changes from SettingsView and restart terminal
  useEffect(() => {
    if (!isOpen) return;

    const handler = async () => {
      setConnectionState("connecting");
      try {
        const tokenResp = await get<{ token: string }>(
          `/api/terminal/token?session=${TERMINAL_SESSION}`, // allow-url
        );
        const token = tokenResp?.token ?? "";
        // prettier-ignore
        await post(`/api/terminal/restart?session=${TERMINAL_SESSION}&token=${encodeURIComponent(token)}`, {}); // allow-url
      } catch {
        // Restart may fail if daemon unavailable; proceed with reconnect anyway
      }
      handleReconnect();
    };

    window.addEventListener("terminal-backend-changed", handler);
    return () =>
      window.removeEventListener("terminal-backend-changed", handler);
  }, [isOpen, handleReconnect]);

  // Body scroll lock
  useEffect(() => {
    if (!isOpen) return;

    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [isOpen]);

  // Focus management: focus panel when opened
  useEffect(() => {
    if (isOpen && panelRef.current) {
      panelRef.current.focus();
    }
  }, [isOpen]);

  // Backdrop click handler
  const handleOverlayClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) {
        onClose();
      }
    },
    [onClose],
  );

  const overlayClass = `${styles.overlay}${isOpen ? ` ${styles.open}` : ""}`;

  // Determine reconnect overlay content
  const showReconnectOverlay =
    connectionState === "disconnected" && wasConnected;
  const isAutoReconnecting =
    reconnectState.nextRetryAt !== null && !reconnectState.gaveUp;

  return (
    <div
      className={overlayClass}
      onClick={handleOverlayClick}
      aria-hidden={!isOpen}
      data-testid="terminal-panel-overlay"
    >
      <aside
        className={styles.panel}
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-label="Terminal"
      >
        <header className={styles.header}>
          <h2>Terminal</h2>
          <span
            className={styles.statusDot}
            data-status={connectionState}
            data-testid="terminal-status-dot"
            aria-label={`Connection: ${connectionState}`}
          />
          <button
            type="button"
            className={styles.closeButton}
            onClick={onClose}
            aria-label="Close terminal"
            data-testid="terminal-close-button"
          >
            &#x2715;
          </button>
        </header>
        <div
          className={styles.terminalContainer}
          ref={termRef}
          data-testid="terminal-container"
        >
          {showReconnectOverlay && (
            <div
              className={styles.reconnectOverlay}
              data-testid="terminal-reconnect-overlay"
            >
              {isAutoReconnecting ? (
                <p
                  className={styles.reconnectStatus}
                  data-testid="terminal-reconnect-status"
                >
                  Reconnecting in {countdown ?? "..."} (attempt{" "}
                  {reconnectState.attempt + 1}/
                  {DEFAULT_RECONNECT_CONFIG.maxAttempts})
                </p>
              ) : reconnectState.gaveUp ? (
                <p>Could not reconnect</p>
              ) : (
                <p>Connection lost</p>
              )}
              <button
                type="button"
                className={styles.reconnectButton}
                onClick={handleReconnect}
                data-testid="terminal-reconnect-button"
              >
                Reconnect
              </button>
            </div>
          )}
        </div>
      </aside>
    </div>
  );
}
