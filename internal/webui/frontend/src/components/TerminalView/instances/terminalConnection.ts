/**
 * Terminal WebSocket connection utilities.
 * Handles token fetching, URL building, resize encoding, and WebSocket lifecycle.
 */

import type { FitAddon } from "@xterm/addon-fit";
import type { Terminal } from "@xterm/xterm";

import {
  get,
  wsUrl,
  getWsBaseUrl,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
} from "@/hooks/api";

import type { ConnectionState } from "./TerminalInstance";

/**
 * Fetch a one-time terminal auth token from the server. The token endpoint
 * is workspace-scoped — the server's WorkspaceMiddleware injects the wsID
 * from the URL path into the request context, and the handler refuses
 * requests without one.
 */
async function fetchTerminalToken(
  workspaceId: string,
  sessionName: string,
): Promise<string | null> {
  try {
    const resp = await get<{ token: string }>(
      wsUrl(
        workspaceId,
        `/terminal/token?session=${encodeURIComponent(sessionName)}`,
      ),
    );
    return resp.token;
  } catch {
    return null;
  }
}

/**
 * Build the WebSocket URL for the terminal relay endpoint. The workspace ID
 * lives in the URL path (not a query parameter) since the workspace-scoped
 * routing migration: WorkspaceMiddleware reads it from the path and injects
 * it into the handler context.
 */
function buildWsUrl(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): string {
  const path = wsUrl(
    workspaceId,
    `/terminal/ws?session=${encodeURIComponent(sessionName)}`,
  );
  let url = `${getWsBaseUrl()}${path}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return url;
}

/**
 * Encode a resize message per the wterm wire format: an in-band escape
 * sequence "\x1b[RESIZE:<cols>;<rows>]" sent as a WebSocket string message.
 * Matches the server-side regex in internal/webui/server/realtime/terminal_relay.go.
 *
 * Returned as a string (not ArrayBuffer) because WebSocket.send accepts both;
 * using a string keeps this function symmetric with the server's string-prefix
 * check and removes the need for the caller to distinguish message types.
 */
export function encodeResize(cols: number, rows: number): string {
  return `\x1b[RESIZE:${cols};${rows}]`;
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
