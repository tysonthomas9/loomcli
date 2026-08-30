import { useEffect, useState } from "react";

import { getApiOrigin, wsUrl } from "@/api/common/client";
import {
  fetchSseToken,
  type ConnectionState,
  type SseTokenResult,
} from "@/api/common/sse";
import type { components } from "@/types/generated/openapi";

export const DEFAULT_LOG_TAIL_BYTES = 256 * 1024;
const LOG_STREAM_RETRY_MS = 5000;
const LOG_STREAM_MAX_RETRY_MS = 30000;

type LogChunkPayload = components["schemas"]["LogChunkPayload"];

export interface UseLogStreamOptions {
  workspaceId: string;
  /** Workspace-relative API path, for example /agents/ember/logs/stream. */
  streamPath: string;
  enabled: boolean;
  tailBytes?: number;
  /** Test seam; production callers use the workspace SSE token exchange. */
  fetchToken?: () => Promise<SseTokenResult>;
}

export interface UseLogStreamResult {
  content: string;
  state: ConnectionState;
  error: string | null;
}

export function getLogStreamUrl(
  workspaceId: string,
  streamPath: string,
  start: { tailBytes: number } | { offset: number },
  token?: string,
): string {
  const params = new URLSearchParams();
  if ("offset" in start) {
    params.set("offset", String(start.offset));
  } else {
    params.set("tail_bytes", String(start.tailBytes));
  }
  if (token) {
    params.set("token", token);
  }
  return `${getApiOrigin()}${wsUrl(workspaceId, streamPath)}?${params.toString()}`;
}

/**
 * Streams raw log bytes from a workspace SSE route. The hook owns reconnects
 * because stream tokens are single-use and browser-native retry would replay a
 * burned token.
 */
export function useLogStream({
  workspaceId,
  streamPath,
  enabled,
  tailBytes = DEFAULT_LOG_TAIL_BYTES,
  fetchToken,
}: UseLogStreamOptions): UseLogStreamResult {
  const [content, setContent] = useState("");
  const [state, setState] = useState<ConnectionState>("disconnected");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    let source: EventSource | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let retryAttempts = 0;
    let byteOffset = 0;
    let receivedChunk = false;
    let isReconnect = false;
    let decoder = new TextDecoder();

    setContent("");
    setError(null);

    if (!enabled) {
      setState("disconnected");
      return;
    }

    const tokenProvider = fetchToken ?? (() => fetchSseToken(workspaceId));

    const closeSource = () => {
      source?.close();
      source = null;
    };

    const scheduleReconnect = (message: string) => {
      if (!active) return;
      closeSource();
      retryAttempts += 1;
      isReconnect = true;
      setError(message);
      setState("reconnecting");
      const delay = Math.min(
        LOG_STREAM_RETRY_MS * Math.pow(2, retryAttempts - 1),
        LOG_STREAM_MAX_RETRY_MS,
      );
      retryTimer = setTimeout(() => {
        retryTimer = null;
        void connect();
      }, delay);
    };

    const handleLogChunk = (event: MessageEvent) => {
      if (!active) return;
      try {
        const payload = JSON.parse(event.data as string) as LogChunkPayload;
        if (
          typeof payload.chunk_b64 !== "string" ||
          !Number.isSafeInteger(payload.byte_offset) ||
          payload.byte_offset < 0
        ) {
          throw new Error("invalid log chunk payload");
        }
        const binary = atob(payload.chunk_b64);
        const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
        const text = decoder.decode(bytes, { stream: true });
        byteOffset = payload.byte_offset;
        receivedChunk = true;
        if (text) {
          setContent((current) => current + text);
        }
      } catch {
        console.warn("[LogStream] Received malformed log-chunk event");
      }
    };

    const handleTruncated = () => {
      if (!active) return;
      byteOffset = 0;
      decoder = new TextDecoder();
      setContent("");
    };

    const connect = async () => {
      if (!active) return;
      setState(isReconnect ? "reconnecting" : "connecting");

      let tokenResult: SseTokenResult;
      try {
        tokenResult = await tokenProvider();
      } catch (tokenError) {
        if (!active) return;
        const message =
          tokenError instanceof Error ? tokenError.message : "Unknown error";
        scheduleReconnect(`Log stream auth failed: ${message}`);
        return;
      }
      if (!active) return;
      if (tokenResult.kind === "error") {
        scheduleReconnect(`Log stream auth failed: ${tokenResult.message}`);
        return;
      }

      const token =
        tokenResult.kind === "token" ? tokenResult.token : undefined;
      // Resume by offset only once a chunk has established the cursor; a
      // reconnect before any chunk keeps the tail window rather than
      // replaying the whole file from byte zero.
      const start = receivedChunk
        ? ({ offset: byteOffset } as const)
        : ({ tailBytes } as const);

      try {
        const nextSource = new EventSource(
          getLogStreamUrl(workspaceId, streamPath, start, token),
        );
        source = nextSource;
        nextSource.onopen = () => {
          if (!active || source !== nextSource) return;
          retryAttempts = 0;
          setError(null);
          setState("connected");
        };
        nextSource.onerror = () => {
          if (!active || source !== nextSource) return;
          scheduleReconnect("Log stream connection lost");
        };
        nextSource.addEventListener("log-chunk", (event) =>
          handleLogChunk(event as MessageEvent),
        );
        nextSource.addEventListener("truncated", handleTruncated);
      } catch (sourceError) {
        const message =
          sourceError instanceof Error ? sourceError.message : "Unknown error";
        scheduleReconnect(`Failed to open log stream: ${message}`);
      }
    };

    void connect();

    return () => {
      active = false;
      if (retryTimer !== null) {
        clearTimeout(retryTimer);
      }
      closeSource();
    };
  }, [enabled, fetchToken, streamPath, tailBytes, workspaceId]);

  return { content, state, error };
}
