/**
 * Terminal WebSocket connection utilities.
 * Handles token fetching, URL building, resize encoding, and WebSocket lifecycle.
 *
 * Renderer-agnostic: the caller provides a `write` function (for output) and
 * wires renderer input directly (`onData` → ws.send).
 */

import { get, wsUrl, getWsBaseUrl } from "@/hooks/api";

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
  initialSize?: { cols: number; rows: number },
): string {
  const path = wsUrl(
    workspaceId,
    `/terminal/ws?session=${encodeURIComponent(sessionName)}`,
  );
  let url = `${getWsBaseUrl()}${path}`; // allow-url
  if (initialSize) {
    url += `&cols=${encodeURIComponent(String(initialSize.cols))}`;
    url += `&rows=${encodeURIComponent(String(initialSize.rows))}`;
  }
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return url;
}

/**
 * Encode a resize message using Loom's in-band terminal wire format: an escape
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
/** Standard WebSocket close code used for a clean server-side close. */
const WS_CLOSE_NORMAL = 1000;
/** Standard WebSocket close code used when the workspace runtime is unavailable. */
const WS_CLOSE_GOING_AWAY = 1001;
const WORKSPACE_UNAVAILABLE_REASON = "workspace unavailable";

/**
 * Connect a WebSocket, wiring received bytes into `write` and announcing
 * lifecycle transitions. The caller owns input (`onData` → ws.send) and
 * resize (`onResize` → encodeResize → ws.send).
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
  onSessionKilled?: () => void,
  initialSize?: { cols: number; rows: number },
  onHistoryCoordinate?: (firstScreenLine: number) => void,
): () => void {
  setConnectionState("connecting");

  let cancelled = false;
  let wsCleanupInner: (() => void) | null = null;
  let flushTimer: ReturnType<typeof setTimeout> | null = null;
  const pendingWrites: Array<string | Uint8Array> = [];

  const cancelPendingFlush = () => {
    if (flushTimer != null) {
      clearTimeout(flushTimer);
      flushTimer = null;
    }
  };

  const flushBinaryGroup = (group: Uint8Array[]) => {
    if (group.length === 0) return;
    if (group.length === 1) {
      const first = group[0];
      if (first) {
        write(first);
      }
      return;
    }
    const total = group.reduce((sum, chunk) => sum + chunk.byteLength, 0);
    const merged = new Uint8Array(total);
    let offset = 0;
    for (const chunk of group) {
      merged.set(chunk, offset);
      offset += chunk.byteLength;
    }
    write(merged);
  };

  const flushPendingWrites = () => {
    flushTimer = null;
    if (cancelled || pendingWrites.length === 0) return;

    let textGroup = "";
    let binaryGroup: Uint8Array[] = [];

    const flushTextGroup = () => {
      if (textGroup.length === 0) return;
      write(textGroup);
      textGroup = "";
    };

    const flushCurrentBinaryGroup = () => {
      flushBinaryGroup(binaryGroup);
      binaryGroup = [];
    };

    for (const chunk of pendingWrites.splice(0, pendingWrites.length)) {
      if (typeof chunk === "string") {
        flushCurrentBinaryGroup();
        textGroup += chunk;
      } else {
        flushTextGroup();
        binaryGroup.push(chunk);
      }
    }

    flushCurrentBinaryGroup();
    flushTextGroup();
    onOutput?.();
  };

  const scheduleFlush = () => {
    if (flushTimer != null) return;
    flushTimer = setTimeout(flushPendingWrites, 16);
  };

  const tokenPromise = fetchTerminalToken(workspaceId, sessionName);

  tokenPromise
    .then((token) => {
      if (cancelled) return;

      const url = buildWsUrl(workspaceId, sessionName, token, initialSize);
      const ws = new WebSocket(url);
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";
      let disconnectedNotified = false;

      const clearCurrentSocket = () => {
        if (wsRef.current === ws) {
          wsRef.current = null;
        }
      };

      const notifyDisconnected = () => {
        if (cancelled || disconnectedNotified) return;
        disconnectedNotified = true;
        setConnectionState("disconnected");
        onDisconnected?.();
      };

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
          try {
            const control = JSON.parse(ev.data) as {
              type?: string;
              firstScreenLine?: number;
            };
            if (
              control.type === "terminal-history" &&
              typeof control.firstScreenLine === "number"
            ) {
              onHistoryCoordinate?.(control.firstScreenLine);
              return;
            }
          } catch {
            // Non-JSON text frames remain valid terminal output.
          }
          pendingWrites.push(ev.data);
        } else if (ev.data instanceof ArrayBuffer) {
          pendingWrites.push(new Uint8Array(ev.data));
        } else if (ev.data instanceof Blob) {
          void ev.data.arrayBuffer().then((buf) => {
            if (cancelled) return;
            pendingWrites.push(new Uint8Array(buf));
            scheduleFlush();
          });
          return;
        }
        scheduleFlush();
      };

      ws.onclose = (event: CloseEvent) => {
        clearCurrentSocket();
        if (cancelled) return;
        if (disconnectedNotified) return;
        if (event.code === WS_CLOSE_BACKEND_EXITED) {
          setConnectionState("crashed");
          onBackendCrash?.(event.reason || "backend process exited");
          return;
        }
        if (event.code === WS_CLOSE_SESSION_KILLED) {
          setConnectionState("session_ended");
          onSessionKilled?.();
          return;
        }
        if (event.code === WS_CLOSE_NORMAL) {
          setConnectionState("session_ended");
          onSessionKilled?.();
          return;
        }
        if (
          event.code === WS_CLOSE_GOING_AWAY &&
          event.reason === WORKSPACE_UNAVAILABLE_REASON
        ) {
          setConnectionState("error");
          onSessionKilled?.();
          return;
        }
        notifyDisconnected();
      };

      ws.onerror = () => {
        if (cancelled) return;
        if (
          ws.readyState === WebSocket.OPEN ||
          ws.readyState === WebSocket.CONNECTING
        ) {
          ws.close();
        }
        clearCurrentSocket();
        notifyDisconnected();
      };

      wsCleanupInner = () => {
        cancelPendingFlush();
        pendingWrites.length = 0;
        if (
          ws.readyState === WebSocket.OPEN ||
          ws.readyState === WebSocket.CONNECTING
        ) {
          ws.close(1000);
        }
        clearCurrentSocket();
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
    cancelPendingFlush();
    pendingWrites.length = 0;
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
