/**
 * Terminal WebSocket connection utilities.
 * Handles token fetching, URL building, resize encoding, and WebSocket lifecycle.
 */

import type { FitAddon } from "@xterm/addon-fit";
import type { Terminal } from "@xterm/xterm";

import { get } from "@/api/client";

import type { ConnectionState } from "./TerminalInstance";

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
export function encodeResize(cols: number, rows: number): ArrayBuffer {
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
export function connectWebSocket(
  sessionName: string,
  terminal: Terminal,
  fitAddon: FitAddon,
  wsRef: React.MutableRefObject<WebSocket | null>,
  setConnectionState: (s: ConnectionState) => void,
  onConnected?: () => void,
  onDisconnected?: () => void,
  onOutput?: () => void,
): () => void {
  setConnectionState("connecting");

  let cancelled = false;

  fetchTerminalToken(sessionName)
    .then((token) => {
      if (cancelled) return;

      const ws = new WebSocket(buildWsUrl(sessionName, token));
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";

      ws.onopen = () => {
        setConnectionState("connected");
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
        onOutput?.();
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
      wsRef.current = null;
    }
  };
}
