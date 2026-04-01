/**
 * Terminal WebSocket connection utilities.
 * Handles token fetching, URL building, resize encoding, and WebSocket lifecycle.
 */

import type { FitAddon } from "@xterm/addon-fit";
import type { Terminal } from "@xterm/xterm";

import { get } from "@/api/client";
import { getAgentTerminalToken, getAgentTerminalWsUrl } from "@/api/logs";

import type { ConnectionState } from "./TerminalInstance";

/**
 * Fetch a one-time terminal auth token from the server.
 */
async function fetchTerminalToken(
  _workspaceId: string,
  sessionName: string,
): Promise<string | null> {
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
function buildWsUrl(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  let url = `${proto}//${window.location.host}/api/terminal/ws?session=${encodeURIComponent(sessionName)}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  if (workspaceId) {
    url += `&workspace=${encodeURIComponent(workspaceId)}`;
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

/** WebSocket close code sent by the backend when the process exits. */
const WS_CLOSE_BACKEND_EXITED = 4001;
/** WebSocket close code sent by the backend when user kills the session. */
const WS_CLOSE_SESSION_KILLED = 4002;

/**
 * Connect a Terminal instance to a WebSocket, returning a cleanup function.
 */
export function connectWebSocket(
  workspaceId: string,
  sessionName: string,
  terminal: Terminal,
  fitAddon: FitAddon,
  wsRef: React.MutableRefObject<WebSocket | null>,
  setConnectionState: (s: ConnectionState) => void,
  onConnected?: () => void,
  onDisconnected?: () => void,
  onOutput?: () => void,
  onBackendCrash?: (reason: string) => void,
  onInput?: (
    data: string,
    sendToWs: (data: string) => void,
    terminal: Terminal,
  ) => void,
  agentName?: string,
  onSessionKilled?: () => void,
): () => void {
  setConnectionState("connecting");

  let cancelled = false;

  // Use agent terminal endpoint when agentName is provided
  const tokenPromise = agentName
    ? getAgentTerminalToken(workspaceId, agentName).catch(() => null)
    : fetchTerminalToken(workspaceId, sessionName);

  tokenPromise
    .then((token) => {
      if (cancelled) return;

      const wsUrl =
        agentName && token
          ? getAgentTerminalWsUrl(workspaceId, agentName, token)
          : buildWsUrl(workspaceId, sessionName, token);
      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";

      // Defense-in-depth: if a future refactor introduces an async step
      // between the check at line 88 and here, this guard prevents leaking
      // the WebSocket.
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
        onOutput?.();
      };

      ws.onclose = (event: CloseEvent) => {
        if (cancelled) return;
        if (event.code === WS_CLOSE_BACKEND_EXITED) {
          // Backend process exited — show crash overlay, do NOT auto-reconnect
          setConnectionState("crashed");
          onBackendCrash?.(event.reason || "backend process exited");
          return;
        }
        if (event.code === WS_CLOSE_SESSION_KILLED) {
          // User killed the session — do NOT auto-reconnect
          setConnectionState("disconnected");
          onSessionKilled?.();
          return;
        }
        setConnectionState("disconnected");
        onDisconnected?.();
      };

      ws.onerror = () => {
        if (cancelled) return;
        setConnectionState("disconnected");
      };

      const sendToWs = (data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(data);
        }
      };
      const onDataDisposable = terminal.onData((data: string) => {
        if (onInput) {
          onInput(data, sendToWs, terminal);
        } else {
          sendToWs(data);
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
      const fn = wsCleanupInner;
      wsCleanupInner = null;
      fn();
    } else {
      // wsCleanupInner not yet assigned, but WebSocket may already exist
      // via wsRef.current (assigned before handler setup).
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
