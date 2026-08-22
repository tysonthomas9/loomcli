/** Direct-PTY WebSocket lifecycle and loom-terminal.v1 frame handling. */

import { get, wsUrl, getWsBaseUrl } from "@/hooks/api";

import type { ConnectionState } from "./TerminalInstance";
import {
  decodeServerFrame,
  encodeFocus,
  encodeInput,
  encodeResizeRequest,
  ProtocolError,
  TERMINAL_SUBPROTOCOL,
} from "./terminalProtocol";

const WS_CLOSE_NORMAL = 1000;
const WS_CLOSE_GOING_AWAY = 1001;
const WS_CLOSE_PROTOCOL_ERROR = 1002;
const WS_CLOSE_BACKEND_EXITED = 4001;
const WS_CLOSE_SESSION_KILLED = 4002;
const WS_CLOSE_SLOW_CONSUMER = 4003;
const WS_CLOSE_STATE_REBUILDING = 4004;
const WORKSPACE_UNAVAILABLE_REASON = "workspace unavailable";

export type ReconnectPolicy = "backoff" | "immediate";

export interface TerminalInitialStateMetadata {
  cols: number;
  rows: number;
  retainedLines: number;
}

export interface TerminalNotice {
  code: string;
  message: string;
}

export interface TerminalConnectionCallbacks {
  write: (data: string | Uint8Array) => void;
  reset: () => void;
  setConnectionState: (state: ConnectionState) => void;
  onConnected?: (() => void) | undefined;
  onDisconnected?: ((policy: ReconnectPolicy) => void) | undefined;
  onOutput?: (() => void) | undefined;
  onBackendCrash?: ((reason: string) => void) | undefined;
  onSessionKilled?: (() => void) | undefined;
  onInitialState?: ((state: TerminalInitialStateMetadata) => void) | undefined;
  onCanonicalResize?: ((cols: number, rows: number) => void) | undefined;
  onNotice?: ((notice: TerminalNotice) => void) | undefined;
}

export interface TerminalConnectionHandle {
  dispose: () => void;
  sendInput: (data: string | Uint8Array) => void;
  sendResizeRequest: (cols: number, rows: number) => void;
  sendFocus: () => void;
}

async function fetchTerminalToken(
  workspaceId: string,
  sessionName: string,
): Promise<string | null> {
  try {
    const resp = await get<{ token: string }>(
      wsUrl(
        workspaceId,
        "/terminal/token?session=" + encodeURIComponent(sessionName),
      ),
    );
    return resp.token;
  } catch {
    return null;
  }
}

function buildWsUrl(
  workspaceId: string,
  sessionName: string,
  token: string | null,
  initialSize?: { cols: number; rows: number },
): string {
  const path = wsUrl(
    workspaceId,
    "/terminal/ws?session=" + encodeURIComponent(sessionName),
  );
  let url = getWsBaseUrl() + path; // allow-url
  if (initialSize) {
    url += "&cols=" + encodeURIComponent(String(initialSize.cols));
    url += "&rows=" + encodeURIComponent(String(initialSize.rows));
  }
  if (token) url += "&token=" + encodeURIComponent(token);
  return url;
}

function generationsEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.byteLength !== right.byteLength) return false;
  return left.every((byte, index) => byte === right[index]);
}

export function connectWebSocket(
  workspaceId: string,
  sessionName: string,
  wsRef: React.MutableRefObject<WebSocket | null>,
  callbacks: TerminalConnectionCallbacks,
  initialSize?: { cols: number; rows: number },
): TerminalConnectionHandle {
  callbacks.setConnectionState("connecting");

  let cancelled = false;
  let socket: WebSocket | null = null;
  let flushTimer: ReturnType<typeof setTimeout> | null = null;
  let pinnedGeneration: Uint8Array | null = null;
  let receivedInitialState = false;
  let disconnectedNotified = false;
  let terminalCloseReason = "";
  let pendingFocus = false;
  let pendingResize: { cols: number; rows: number } | null = null;
  const pendingWrites: Uint8Array[] = [];

  const cancelPendingFlush = () => {
    if (flushTimer != null) {
      clearTimeout(flushTimer);
      flushTimer = null;
    }
  };

  const flushPendingWrites = () => {
    flushTimer = null;
    if (cancelled || pendingWrites.length === 0) return;
    const chunks = pendingWrites.splice(0);
    if (chunks.length === 1) {
      callbacks.write(chunks[0] ?? new Uint8Array());
    } else {
      const total = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
      const merged = new Uint8Array(total);
      let offset = 0;
      for (const chunk of chunks) {
        merged.set(chunk, offset);
        offset += chunk.byteLength;
      }
      callbacks.write(merged);
    }
    callbacks.onOutput?.();
  };

  const scheduleFlush = () => {
    if (flushTimer == null) {
      flushTimer = setTimeout(flushPendingWrites, 16);
    }
  };

  const notifyDisconnected = (policy: ReconnectPolicy) => {
    if (cancelled || disconnectedNotified) return;
    disconnectedNotified = true;
    callbacks.setConnectionState("disconnected");
    callbacks.onDisconnected?.(policy);
  };

  const clearCurrentSocket = (ws: WebSocket) => {
    if (wsRef.current === ws) wsRef.current = null;
    if (socket === ws) socket = null;
  };

  const sendWithGeneration = (
    encode: (generation: Uint8Array) => ArrayBuffer,
  ): boolean => {
    const ws = socket;
    if (
      !pinnedGeneration ||
      !ws ||
      ws.readyState !== WebSocket.OPEN ||
      cancelled
    ) {
      return false;
    }
    ws.send(encode(pinnedGeneration));
    return true;
  };

  const failProtocol = (ws: WebSocket, message: string) => {
    if (cancelled || disconnectedNotified) return;
    cancelPendingFlush();
    pendingWrites.length = 0;
    disconnectedNotified = true;
    callbacks.setConnectionState("error");
    ws.close(WS_CLOSE_PROTOCOL_ERROR, message);
  };

  const reconnectForGenerationChange = (ws: WebSocket) => {
    cancelPendingFlush();
    pendingWrites.length = 0;
    notifyDisconnected("immediate");
    ws.close(WS_CLOSE_PROTOCOL_ERROR, "terminal generation changed");
  };

  const processFrame = (ws: WebSocket, buffer: ArrayBuffer) => {
    if (cancelled || disconnectedNotified) return;
    let frame;
    try {
      frame = decodeServerFrame(buffer);
    } catch (error) {
      const message =
        error instanceof ProtocolError
          ? error.message
          : "malformed terminal frame";
      failProtocol(ws, message);
      return;
    }

    if (!receivedInitialState) {
      if (frame.kind !== "initial_state") {
        failProtocol(ws, "first terminal frame must be initial_state");
        return;
      }
      if (frame.encoding !== "xterm-vt/1") {
        failProtocol(ws, "unsupported terminal state encoding");
        return;
      }
      receivedInitialState = true;
      pinnedGeneration = frame.generation;
      callbacks.onInitialState?.({
        cols: frame.cols,
        rows: frame.rows,
        retainedLines: frame.retainedLines,
      });
      callbacks.reset();
      callbacks.write(frame.data);
      callbacks.onOutput?.();
      if (pendingResize) {
        const resize = pendingResize;
        pendingResize = null;
        sendWithGeneration((generation) =>
          encodeResizeRequest(generation, resize.cols, resize.rows),
        );
      }
      if (pendingFocus) {
        pendingFocus = false;
        sendWithGeneration(encodeFocus);
      }
      return;
    }

    if (frame.kind === "initial_state") {
      failProtocol(ws, "initial_state may only be the first frame");
      return;
    }
    if (
      !pinnedGeneration ||
      !generationsEqual(frame.generation, pinnedGeneration)
    ) {
      reconnectForGenerationChange(ws);
      return;
    }

    switch (frame.kind) {
      case "output":
        pendingWrites.push(frame.data);
        scheduleFlush();
        break;
      case "resize":
        cancelPendingFlush();
        flushPendingWrites();
        callbacks.onCanonicalResize?.(frame.cols, frame.rows);
        break;
      case "notice":
        cancelPendingFlush();
        flushPendingWrites();
        callbacks.onNotice?.({ code: frame.code, message: frame.message });
        break;
      case "close":
        cancelPendingFlush();
        flushPendingWrites();
        terminalCloseReason = frame.reason;
        break;
    }
  };

  void fetchTerminalToken(workspaceId, sessionName).then((token) => {
    if (cancelled) return;

    const url = buildWsUrl(workspaceId, sessionName, token, initialSize);
    const ws = new WebSocket(url, [TERMINAL_SUBPROTOCOL]);
    socket = ws;
    wsRef.current = ws;
    ws.binaryType = "arraybuffer";
    let messageChain = Promise.resolve();

    if (cancelled) {
      ws.close(WS_CLOSE_NORMAL);
      clearCurrentSocket(ws);
      return;
    }

    ws.onopen = () => {
      if (cancelled) return;
      if (ws.protocol !== TERMINAL_SUBPROTOCOL) {
        failProtocol(ws, "terminal subprotocol was not negotiated");
        return;
      }
      callbacks.setConnectionState("connected");
      callbacks.onConnected?.();
    };

    ws.onmessage = (event: MessageEvent) => {
      if (cancelled || disconnectedNotified) return;
      messageChain = messageChain
        .then(async () => {
          if (cancelled || disconnectedNotified) return;
          if (event.data instanceof ArrayBuffer) {
            processFrame(ws, event.data);
          } else if (event.data instanceof Blob) {
            processFrame(ws, await event.data.arrayBuffer());
          } else {
            failProtocol(ws, "terminal frames must be binary");
          }
        })
        .catch(() => failProtocol(ws, "could not read terminal frame"));
    };

    ws.onclose = (event: CloseEvent) => {
      clearCurrentSocket(ws);
      if (cancelled || disconnectedNotified) return;
      cancelPendingFlush();
      flushPendingWrites();
      const reason = event.reason || terminalCloseReason;
      switch (event.code) {
        case WS_CLOSE_BACKEND_EXITED:
          callbacks.setConnectionState("crashed");
          callbacks.onBackendCrash?.(reason || "backend process exited");
          return;
        case WS_CLOSE_SESSION_KILLED:
        case WS_CLOSE_NORMAL:
          callbacks.setConnectionState("session_ended");
          callbacks.onSessionKilled?.();
          return;
        case WS_CLOSE_SLOW_CONSUMER:
          notifyDisconnected("immediate");
          return;
        case WS_CLOSE_STATE_REBUILDING:
          notifyDisconnected("backoff");
          return;
        case WS_CLOSE_GOING_AWAY:
          if (reason === WORKSPACE_UNAVAILABLE_REASON) {
            callbacks.setConnectionState("error");
            callbacks.onSessionKilled?.();
            return;
          }
          break;
      }
      notifyDisconnected("backoff");
    };

    ws.onerror = () => {
      if (cancelled || disconnectedNotified) return;
      if (
        ws.readyState === WebSocket.OPEN ||
        ws.readyState === WebSocket.CONNECTING
      ) {
        ws.close();
      }
      clearCurrentSocket(ws);
      notifyDisconnected("backoff");
    };
  });

  const dispose = () => {
    if (cancelled) return;
    cancelled = true;
    cancelPendingFlush();
    pendingWrites.length = 0;
    const ws = socket;
    if (
      ws &&
      (ws.readyState === WebSocket.OPEN ||
        ws.readyState === WebSocket.CONNECTING)
    ) {
      ws.close(WS_CLOSE_NORMAL);
    }
    if (ws) clearCurrentSocket(ws);
  };

  return {
    dispose,
    sendInput: (data) => {
      sendWithGeneration((generation) => encodeInput(generation, data));
    },
    sendResizeRequest: (cols, rows) => {
      pendingResize = { cols, rows };
      if (
        sendWithGeneration((generation) =>
          encodeResizeRequest(generation, cols, rows),
        )
      ) {
        pendingResize = null;
      }
    },
    sendFocus: () => {
      pendingFocus = true;
      if (sendWithGeneration(encodeFocus)) pendingFocus = false;
    },
  };
}
