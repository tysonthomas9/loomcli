/**
 * Terminal WebSocket connection utilities.
 * Handles token fetching, URL building, resize encoding, and WebSocket lifecycle.
 *
 * Renderer-agnostic: the caller provides a `write` function (for output) and
 * wires input directly (e.g. wterm's `onData` → ws.send). No xterm Terminal
 * / FitAddon / interceptor references live here after the wterm migration.
 */

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
 * lives in the URL path (not a query parameter): WorkspaceMiddleware reads it
 * from the path and injects it into the handler context.
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
 */
export function encodeResize(cols: number, rows: number): string {
  return `\x1b[RESIZE:${cols};${rows}]`;
}

/** WebSocket close code sent by the backend when the process exits. */
const WS_CLOSE_BACKEND_EXITED = 4001;
/** WebSocket close code sent by the backend when user kills the session. */
const WS_CLOSE_SESSION_KILLED = 4002;

/**
 * Connect a WebSocket, wiring received bytes into `write` and announcing
 * lifecycle transitions. The caller owns input (wterm's `onData` → ws.send)
 * and resize (wterm's `onResize` → encodeResize → ws.send).
 *
 * Returns a cleanup function that closes the WS and marks the connect
 * attempt cancelled (idempotent; safe to call before or after onopen).
 */
export function connectWebSocket(
  workspaceId: string,
  sessionName: string,
  write: (data: string | Uint8Array) => void,
  wsRef: React.MutableRefObject<WebSocket | null>,
  setConnectionState: (s: ConnectionState) => void,
  onConnected?: () => void,
  onDisconnected?: () => void,
  onOutput?: () => void,
  onBackendCrash?: (reason: string) => void,
  agentName?: string,
  onSessionKilled?: () => void,
): () => void {
  setConnectionState("connecting");

  let cancelled = false;
  let wsCleanupInner: (() => void) | null = null;

  const tokenPromise = agentName
    ? getAgentTerminalToken(workspaceId, agentName).catch(() => null)
    : fetchTerminalToken(workspaceId, sessionName);

  tokenPromise
    .then((token) => {
      if (cancelled) return;

      const url =
        agentName && token
          ? getAgentTerminalWsUrl(workspaceId, agentName, token)
          : buildWsUrl(workspaceId, sessionName, token);
      const ws = new WebSocket(url);
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";

      // Defense-in-depth: if a future refactor adds an async step between
      // the check above and here, don't leak the fresh WebSocket.
      if (cancelled) {
        ws.close(1000);
        wsRef.current = null;
        return;
      }

      ws.onopen = () => {
        if (cancelled) return;
        setConnectionState("connected");
        onConnected?.();
      };

      ws.onmessage = (ev: MessageEvent) => {
        if (cancelled) return;
        if (typeof ev.data === "string") {
          write(ev.data);
        } else if (ev.data instanceof ArrayBuffer) {
          write(new Uint8Array(ev.data));
        }
        onOutput?.();
      };

      ws.onclose = (event: CloseEvent) => {
        if (cancelled) return;
        if (event.code === WS_CLOSE_BACKEND_EXITED) {
          setConnectionState("crashed");
          onBackendCrash?.(event.reason || "backend process exited");
          return;
        }
        if (event.code === WS_CLOSE_SESSION_KILLED) {
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

      wsCleanupInner = () => {
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

  return () => {
    cancelled = true;
    if (wsCleanupInner) {
      const fn = wsCleanupInner;
      wsCleanupInner = null;
      fn();
    } else {
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
