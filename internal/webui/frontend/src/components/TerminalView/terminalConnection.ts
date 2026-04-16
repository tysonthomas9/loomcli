/**
 * Terminal WebSocket connection utilities.
 * Handles token fetching, URL building, resize encoding, and WebSocket lifecycle.
 */

import { get, wsUrl } from "@/api/client";
import { getAgentTerminalToken, getAgentTerminalWsUrl } from "@/api/logs";

import type { ConnectionState } from "./TerminalInstance";

/**
 * Renderer-agnostic sink. The caller owns a terminal (wterm, or whatever
 * renders today) and exposes just the two hooks this module needs.
 */
export interface TerminalSink {
  write: (data: string | Uint8Array) => void;
  getSize: () => { cols: number; rows: number };
}

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
 * lives in the URL path (not a query parameter) since Commit 1 of the
 * workspace-scoped routing migration: WorkspaceMiddleware reads it from the
 * path and injects it into the handler context.
 */
function buildWsUrl(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  // wsUrl returns "/api/workspaces/<id><path>"; we just need to splice the
  // proto + host on the front for the WebSocket scheme.
  const path = wsUrl(
    workspaceId,
    `/terminal/ws?session=${encodeURIComponent(sessionName)}`,
  );
  let url = `${proto}//${window.location.host}${path}`;
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

/** WebSocket close code sent by the backend when the process exits. */
const WS_CLOSE_BACKEND_EXITED = 4001;
/** WebSocket close code sent by the backend when user kills the session. */
const WS_CLOSE_SESSION_KILLED = 4002;

/**
 * Connect a terminal sink to the backend WebSocket, returning a cleanup function.
 * The caller is responsible for wiring user input (onData) directly — this
 * function owns the WS lifecycle, incoming bytes, and the initial resize frame.
 */
export function connectWebSocket(
  workspaceId: string,
  sessionName: string,
  sink: TerminalSink,
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
        const { cols, rows } = sink.getSize();
        ws.send(encodeResize(cols, rows));
        onConnected?.();
      };

      ws.onmessage = (ev: MessageEvent) => {
        if (cancelled) return;
        if (typeof ev.data === "string") {
          sink.write(ev.data);
        } else if (ev.data instanceof ArrayBuffer) {
          sink.write(new Uint8Array(ev.data));
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
        // No state change here — the spec guarantees onclose fires after
        // onerror for the same WebSocket, and that handler owns state
        // transitions (including 4001/4002 which must not degrade to
        // "disconnected" first).
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
