/**
 * TerminalPanel component.
 * Slide-out panel with an embedded xterm.js terminal connected via WebSocket.
 */

import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal } from '@xterm/xterm';
import { useEffect, useRef, useState, useCallback } from 'react';

import '@xterm/xterm/css/xterm.css';
import styles from './TerminalPanel.module.css';

export interface TerminalPanelProps {
  isOpen: boolean;
  onClose: () => void;
}

type ConnectionState = 'disconnected' | 'connecting' | 'connected';

/**
 * Build the WebSocket URL for the terminal relay endpoint.
 * Includes a required session parameter for tmux session identification.
 */
function buildWsUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  // Use a fixed session name for the Talk to Lead terminal
  const session = 'talk-to-lead';
  return `${proto}//${window.location.host}/api/terminal/ws?session=${session}`;
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
 * Used for both initial connection and reconnection.
 */
function connectWebSocket(
  terminal: Terminal,
  fitAddon: FitAddon,
  wsRef: React.MutableRefObject<WebSocket | null>,
  setConnectionState: (s: ConnectionState) => void,
  setWasConnected: (v: boolean) => void
): () => void {
  setConnectionState('connecting');
  const ws = new WebSocket(buildWsUrl());
  wsRef.current = ws;
  ws.binaryType = 'arraybuffer';

  ws.onopen = () => {
    setConnectionState('connected');
    setWasConnected(true);
    fitAddon.fit();
    ws.send(encodeResize(terminal.cols, terminal.rows));
  };

  ws.onmessage = (ev: MessageEvent) => {
    if (typeof ev.data === 'string') {
      terminal.write(ev.data);
    } else if (ev.data instanceof ArrayBuffer) {
      terminal.write(new Uint8Array(ev.data));
    }
  };

  ws.onclose = () => {
    setConnectionState('disconnected');
  };

  ws.onerror = () => {
    setConnectionState('disconnected');
  };

  const onDataDisposable = terminal.onData((data: string) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(data);
    }
  });

  return () => {
    onDataDisposable.dispose();
    if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
      ws.close(1000);
    }
    wsRef.current = null;
  };
}

export function TerminalPanel({ isOpen, onClose }: TerminalPanelProps): JSX.Element {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsCleanupRef = useRef<(() => void) | null>(null);
  const panelRef = useRef<HTMLElement>(null);

  const [connectionState, setConnectionState] = useState<ConnectionState>('disconnected');
  const [wasConnected, setWasConnected] = useState(false);

  // Terminal lifecycle: create on open, destroy on close
  useEffect(() => {
    if (!isOpen) return;

    const container = termRef.current;
    if (!container) return;

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      scrollback: 5000,
      smoothScrollDuration: 120,
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#d4d4d4',
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

    // Connect WebSocket
    setWasConnected(false);
    const cleanupWs = connectWebSocket(
      terminal,
      fitAddon,
      wsRef,
      setConnectionState,
      setWasConnected
    );
    wsCleanupRef.current = cleanupWs;

    // ResizeObserver with debounce for ongoing resize
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        if (fitAddonRef.current && terminalRef.current) {
          fitAddonRef.current.fit();
          const currentWs = wsRef.current;
          if (currentWs && currentWs.readyState === WebSocket.OPEN) {
            currentWs.send(encodeResize(terminalRef.current.cols, terminalRef.current.rows));
          }
        }
      }, 100);
    });
    observer.observe(container);

    return () => {
      clearTimeout(fitTimer);
      clearTimeout(resizeTimer);
      observer.disconnect();

      wsCleanupRef.current?.();
      wsCleanupRef.current = null;

      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;

      setConnectionState('disconnected');
    };
  }, [isOpen]);

  // Reconnect handler: clean up old WS, create new one reusing existing terminal
  const handleReconnect = useCallback(() => {
    const terminal = terminalRef.current;
    const fitAddon = fitAddonRef.current;
    if (!terminal || !fitAddon) return;

    // Clean up previous connection
    wsCleanupRef.current?.();

    const cleanupWs = connectWebSocket(
      terminal,
      fitAddon,
      wsRef,
      setConnectionState,
      setWasConnected
    );
    wsCleanupRef.current = cleanupWs;
  }, []);

  // Body scroll lock
  useEffect(() => {
    if (!isOpen) return;

    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
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
    [onClose]
  );

  const overlayClass = `${styles.overlay}${isOpen ? ` ${styles.open}` : ''}`;

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
        <div className={styles.terminalContainer} ref={termRef} data-testid="terminal-container">
          {connectionState === 'disconnected' && wasConnected && (
            <div className={styles.reconnectOverlay} data-testid="terminal-reconnect-overlay">
              <p>Connection lost</p>
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
