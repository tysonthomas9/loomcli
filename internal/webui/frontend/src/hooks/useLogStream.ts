/**
 * React hook for SSE raw log streaming.
 * Streams terminal bytes and keeps reconnect state with byte offsets.
 */

import { useState, useEffect, useRef, useCallback } from 'react';

/**
 * A single raw log chunk.
 */
export interface LogChunk {
  chunk: Uint8Array;
  byteOffset: number;
  timestamp: string;
}

/**
 * Connection states for log streaming.
 */
export type LogStreamState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting';

/**
 * Options for the useLogStream hook.
 */
export interface UseLogStreamOptions {
  /** Log endpoint URL (e.g., "/api/agents/spark/logs/stream") */
  url: string;
  /** Auto-connect on mount. Default: true */
  autoConnect?: boolean;
  /** Maximum chunks to keep in buffer. Default: 5000 */
  maxChunks?: number;
}

/**
 * Return type for the useLogStream hook.
 */
export interface UseLogStreamReturn {
  /** Raw log chunks */
  chunks: LogChunk[];
  /** Connection state */
  state: LogStreamState;
  /** Whether currently connected */
  isConnected: boolean;
  /** Current reconnection attempt count */
  reconnectAttempts: number;
  /** Last error message */
  lastError: string | null;
  /** Incremented each time stream is truncated/reset */
  resetVersion: number;
  /** Clear all chunks from buffer */
  clearChunks: () => void;
  /** Manually connect */
  connect: () => void;
  /** Manually disconnect */
  disconnect: () => void;
}

interface LogChunkEvent {
  chunk_b64?: string;
  chunkB64?: string;
  byte_offset?: number;
  byteOffset?: number;
  timestamp?: string;
}

function decodeBase64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export function useLogStream(options: UseLogStreamOptions): UseLogStreamReturn {
  const { url, autoConnect = true, maxChunks = 5000 } = options;

  const [chunks, setChunks] = useState<LogChunk[]>([]);
  const [state, setState] = useState<LogStreamState>('disconnected');
  const [lastError, setLastError] = useState<string | null>(null);
  const [reconnectAttempts, setReconnectAttempts] = useState(0);
  const [resetVersion, setResetVersion] = useState(0);

  const eventSourceRef = useRef<EventSource | null>(null);
  const mountedRef = useRef(true);
  const manualDisconnectRef = useRef(false);
  const lastByteOffsetRef = useRef(0);
  const urlRef = useRef(url);
  const maxChunksRef = useRef(maxChunks);

  useEffect(() => {
    urlRef.current = url;
  }, [url]);

  useEffect(() => {
    maxChunksRef.current = maxChunks;
  }, [maxChunks]);

  const handleLogChunk = useCallback((event: MessageEvent) => {
    if (!mountedRef.current) return;

    let data: LogChunkEvent = {};
    try {
      data = JSON.parse(event.data as string);
    } catch {
      return;
    }

    const base64 = data.chunk_b64 ?? data.chunkB64;
    if (!base64) return;

    let parsedBytes: Uint8Array;
    try {
      parsedBytes = decodeBase64ToBytes(base64);
    } catch {
      return;
    }

    const parsedOffset =
      typeof data.byte_offset === 'number'
        ? data.byte_offset
        : typeof data.byteOffset === 'number'
          ? data.byteOffset
          : undefined;

    let byteOffset = lastByteOffsetRef.current + parsedBytes.length;
    if (parsedOffset && parsedOffset > 0) {
      byteOffset = parsedOffset;
    }
    lastByteOffsetRef.current = byteOffset;

    const newChunk: LogChunk = {
      chunk: parsedBytes,
      byteOffset,
      timestamp: data.timestamp || new Date().toISOString(),
    };

    setChunks((prev) => {
      const updated = [...prev, newChunk];
      if (updated.length > maxChunksRef.current) {
        return updated.slice(updated.length - maxChunksRef.current);
      }
      return updated;
    });
  }, []);

  const handleTruncated = useCallback(() => {
    if (!mountedRef.current) return;
    lastByteOffsetRef.current = 0;
    setChunks([]);
    setResetVersion((prev) => prev + 1);
  }, []);

  const connect = useCallback(() => {
    if (eventSourceRef.current) {
      return;
    }

    manualDisconnectRef.current = false;
    setState('connecting');
    setLastError(null);

    let connectUrl = urlRef.current;
    if (lastByteOffsetRef.current > 0) {
      const separator = connectUrl.includes('?') ? '&' : '?';
      connectUrl = `${connectUrl}${separator}since_bytes=${lastByteOffsetRef.current}`;
    }

    try {
      const es = new EventSource(connectUrl);
      eventSourceRef.current = es;

      es.onopen = () => {
        if (!mountedRef.current) return;
        setState('connected');
        setLastError(null);
        setReconnectAttempts(0);
      };

      es.onerror = () => {
        if (!mountedRef.current || manualDisconnectRef.current) return;

        if (es.readyState === EventSource.CONNECTING) {
          setState('reconnecting');
          setReconnectAttempts((prev) => prev + 1);
        } else if (es.readyState === EventSource.CLOSED) {
          setState('reconnecting');
          setReconnectAttempts((prev) => prev + 1);
          setLastError('Connection closed');
        }
      };

      es.addEventListener('log-chunk', handleLogChunk);
      es.addEventListener('truncated', handleTruncated);
      es.onmessage = handleLogChunk;
    } catch (err) {
      console.error('[useLogStream] Failed to create EventSource:', err);
      setState('disconnected');
      setLastError('Failed to connect');
    }
  }, [handleLogChunk, handleTruncated]);

  const disconnect = useCallback(() => {
    manualDisconnectRef.current = true;
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    setState('disconnected');
  }, []);

  const clearChunks = useCallback(() => {
    setChunks([]);
    lastByteOffsetRef.current = 0;
    setResetVersion((prev) => prev + 1);
  }, []);

  useEffect(() => {
    if (typeof window === 'undefined') return;

    mountedRef.current = true;
    if (autoConnect) {
      connect();
    }

    return () => {
      mountedRef.current = false;
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
    };
  }, [autoConnect, connect]);

  const prevUrlRef = useRef(url);
  useEffect(() => {
    if (prevUrlRef.current !== url) {
      prevUrlRef.current = url;
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
        setChunks([]);
        lastByteOffsetRef.current = 0;
        setResetVersion((prev) => prev + 1);
        connect();
      }
    }
  }, [url, connect]);

  const isConnected = state === 'connected';

  return {
    chunks,
    state,
    isConnected,
    reconnectAttempts,
    lastError,
    resetVersion,
    clearChunks,
    connect,
    disconnect,
  };
}
