/**
 * TerminalInstance component.
 * Single xterm.js terminal pane with WebSocket connection, WebGL rendering,
 * SearchAddon, FitAddon, and auto-reconnect. Designed to be mounted inside
 * a tabbed TerminalView container.
 */

import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal } from "@xterm/xterm";
import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  useCallback,
} from "react";

import { get } from "@/api/client";
import {
  startAutoReconnect,
  type ReconnectState,
} from "@/utils/reconnectBackoff";

import "@xterm/xterm/css/xterm.css";
import styles from "./TerminalInstance.module.css";

export type ConnectionState = "disconnected" | "connecting" | "connected";

export interface TerminalInstanceProps {
  sessionName: string;
  isActive: boolean;
  fontFamily?: string;
  fontSize?: number;
  onConnectionStateChange?: (state: ConnectionState) => void;
}

export interface TerminalInstanceHandle {
  search: (
    term: string,
    options?: {
      caseSensitive?: boolean;
      wholeWord?: boolean;
      regex?: boolean;
    },
  ) => boolean;
  findNext: () => boolean;
  findPrevious: () => boolean;
  clearSearch: () => void;
}

/**
 * Fetch a one-time terminal auth token from the server.
 */
async function fetchTerminalToken(sessionName: string): Promise<string | null> {
  try {
    const resp = await get<{ token: string }>(
      `/api/terminal/token?session=${encodeURIComponent(sessionName)}`, // allow-url
    );
    return resp.token;
  } catch {
    return null;
  }
}

/**
 * Build the WebSocket URL for the terminal relay endpoint.
 */
function buildWsUrl(sessionName: string, token: string | null): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  let url = `${proto}//${window.location.host}/api/terminal/ws?session=${encodeURIComponent(sessionName)}`; // allow-url
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
 */
function connectWebSocket(
  sessionName: string,
  terminal: Terminal,
  fitAddon: FitAddon,
  wsRef: React.MutableRefObject<WebSocket | null>,
  setConnectionState: (s: ConnectionState) => void,
  onConnected?: () => void,
  onDisconnected?: () => void,
): () => void {
  setConnectionState("connecting");

  let cancelled = false;

  fetchTerminalToken(sessionName)
    .then((token) => {
      if (cancelled) return;

      const ws = new WebSocket(buildWsUrl(sessionName, token));
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";

      // If cleanup already fired while this microtask was queued,
      // close the socket immediately and bail out.
      if (cancelled) {
        ws.close(1000);
        wsRef.current = null;
        return;
      }

      ws.onopen = () => {
        if (cancelled) return;
        setConnectionState("connected");
        fitAddon.fit();
        ws.send(encodeResize(terminal.cols, terminal.rows));
        onConnected?.();
      };

      ws.onmessage = (ev: MessageEvent) => {
        if (cancelled) return;
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

  let wsCleanupInner: (() => void) | null = null;

  return () => {
    cancelled = true;
    if (wsCleanupInner) {
      wsCleanupInner();
    } else {
      // WebSocket may exist via wsRef.current (assigned before handler setup)
      // even though wsCleanupInner hasn't been assigned yet.
      const ws = wsRef.current;
      if (
        ws &&
        (ws.readyState === WebSocket.OPEN ||
          ws.readyState === WebSocket.CONNECTING)
      ) {
        ws.close(1000);
      }
      wsRef.current = null;
    }
  };
}

export const TerminalInstance = forwardRef<
  TerminalInstanceHandle,
  TerminalInstanceProps
>(function TerminalInstance(
  {
    sessionName,
    isActive,
    fontFamily = 'Menlo, Monaco, "Courier New", monospace',
    fontSize = 14,
    onConnectionStateChange,
  },
  ref,
) {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const searchAddonRef = useRef<SearchAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const wsCleanupRef = useRef<(() => void) | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);

  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");

  // Mirror connection state to parent via callback
  const connectionStateRef = useRef(connectionState);
  useEffect(() => {
    connectionStateRef.current = connectionState;
    onConnectionStateChange?.(connectionState);
  }, [connectionState, onConnectionStateChange]);

  // Expose search methods via imperative handle
  useImperativeHandle(
    ref,
    () => ({
      search(term, options) {
        const addon = searchAddonRef.current;
        if (!addon) return false;
        const searchOpts: Record<string, boolean> = {};
        if (options?.caseSensitive !== undefined)
          searchOpts.caseSensitive = options.caseSensitive;
        if (options?.wholeWord !== undefined)
          searchOpts.wholeWord = options.wholeWord;
        if (options?.regex !== undefined) searchOpts.regex = options.regex;
        return addon.findNext(term, searchOpts);
      },
      findNext() {
        const addon = searchAddonRef.current;
        if (!addon) return false;
        return addon.findNext("");
      },
      findPrevious() {
        const addon = searchAddonRef.current;
        if (!addon) return false;
        return addon.findPrevious("");
      },
      clearSearch() {
        searchAddonRef.current?.clearDecorations();
      },
    }),
    [],
  );

  // Reconnect handler
  const handleReconnect = useCallback(() => {
    const terminal = terminalRef.current;
    const fitAddon = fitAddonRef.current;
    if (!terminal || !fitAddon) return;

    reconnectCancelRef.current?.();
    reconnectCancelRef.current = null;

    wsCleanupRef.current?.();
    const cleanupWs = connectWebSocket(
      sessionName,
      terminal,
      fitAddon,
      wsRef,
      setConnectionState,
      () => {
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
      },
      () => {
        if (reconnectCancelRef.current) return;
        const cancel = startAutoReconnect(
          () => {
            handleReconnect();
            return false;
          },
          (_state: ReconnectState) => {
            // State tracked internally; parent can observe via onConnectionStateChange
          },
        );
        reconnectCancelRef.current = cancel;
      },
    );
    wsCleanupRef.current = cleanupWs;
  }, [sessionName]);

  // Terminal lifecycle: create terminal and connect WebSocket
  useEffect(() => {
    const container = termRef.current;
    if (!container) return;

    let mounted = true;

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize,
      fontFamily,
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
    const searchAddon = new SearchAddon();

    terminal.loadAddon(fitAddon);
    terminal.loadAddon(webLinksAddon);
    terminal.loadAddon(searchAddon);

    // Try loading WebGL addon — fall back to canvas on failure
    let webglAddon: WebglAddon | null = null;
    try {
      webglAddon = new WebglAddon();
      webglAddon.onContextLoss(() => {
        console.warn(
          "TerminalInstance: WebGL context lost, falling back to canvas renderer",
        );
        webglAddon?.dispose();
        webglAddon = null;
      });
      terminal.loadAddon(webglAddon);
    } catch {
      // WebGL not available — canvas renderer is the default
      webglAddon = null;
    }

    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;
    searchAddonRef.current = searchAddon;

    terminal.open(container);

    // Initial fit
    fitAddon.fit();

    // Connect WebSocket
    function doConnect(): void {
      wsCleanupRef.current?.();
      const cleanup = connectWebSocket(
        sessionName,
        terminal,
        fitAddon,
        wsRef,
        setConnectionState,
        () => {
          // onConnected: cancel auto-reconnect
          reconnectCancelRef.current?.();
          reconnectCancelRef.current = null;
        },
        () => {
          // onDisconnected: start auto-reconnect
          if (!mounted || reconnectCancelRef.current) return;
          const cancel = startAutoReconnect(
            () => {
              if (!mounted) return true;
              doConnect();
              return false;
            },
            (_state: ReconnectState) => {
              // Reconnect state tracked internally
            },
          );
          reconnectCancelRef.current = cancel;
        },
      );
      wsCleanupRef.current = cleanup;
    }

    doConnect();

    // ResizeObserver with debounce
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

      clearTimeout(resizeTimer);
      observer.disconnect();

      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;

      wsCleanupRef.current?.();
      wsCleanupRef.current = null;

      webglAddon?.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      searchAddonRef.current = null;

      setConnectionState("disconnected");
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fontSize/fontFamily handled by separate sync effect
  }, [sessionName]);

  // Re-fit when tab becomes active (xterm can't measure when hidden)
  useEffect(() => {
    if (!isActive) return;
    const frame = requestAnimationFrame(() => {
      if (fitAddonRef.current) {
        fitAddonRef.current.fit();
        const currentWs = wsRef.current;
        if (
          currentWs &&
          currentWs.readyState === WebSocket.OPEN &&
          terminalRef.current
        ) {
          currentWs.send(
            encodeResize(terminalRef.current.cols, terminalRef.current.rows),
          );
        }
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [isActive]);

  // Dynamic font changes without terminal recreation
  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) return;
    terminal.options.fontSize = fontSize;
    terminal.options.fontFamily = fontFamily;
    fitAddonRef.current?.fit();
  }, [fontSize, fontFamily]);

  return (
    <div
      className={styles.container}
      ref={termRef}
      data-testid="terminal-instance"
    />
  );
});
