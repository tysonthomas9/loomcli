import { useCallback, useEffect, useRef, useState } from 'react';

import {
  getAgentLogArchive,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
} from '@/api';
import { DEFAULT_RECONNECT_CONFIG, startAutoReconnect } from '@/utils/reconnectBackoff';

import type { LogChunk, LogStreamState } from './logTypes';

export type AgentLogTransportMode = 'idle' | 'loading' | 'tmux' | 'archive';

export interface UseAgentTerminalLogsOptions {
  agentName: string | null;
  enabled: boolean;
  archiveLines?: number;
}

export interface UseAgentTerminalLogsReturn {
  mode: AgentLogTransportMode;
  chunks: LogChunk[];
  state: LogStreamState;
  error: string | null;
  resetVersion: number;
  refresh: () => void;
  resize: (cols: number, rows: number) => void;
  sendInput: (data: string) => void;
  /** Load older log lines (for infinite scroll in archive mode). */
  loadOlderLogs: () => void;
  /** Whether there are older lines available to load. */
  hasMoreLines: boolean;
  /** Whether older lines are currently being fetched. */
  isLoadingMore: boolean;
}

function clampTerminalSize(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, Math.floor(value)));
}

function buildResizeFrame(cols: number, rows: number): ArrayBuffer {
  const buf = new ArrayBuffer(5);
  const view = new DataView(buf);
  view.setUint8(0, 0x01);
  view.setUint16(1, cols, false);
  view.setUint16(3, rows, false);
  return buf;
}

export function useAgentTerminalLogs({
  agentName,
  enabled,
  archiveLines = 500,
}: UseAgentTerminalLogsOptions): UseAgentTerminalLogsReturn {
  const [mode, setMode] = useState<AgentLogTransportMode>('idle');
  const [chunks, setChunks] = useState<LogChunk[]>([]);
  const [state, setState] = useState<LogStreamState>('disconnected');
  const [error, setError] = useState<string | null>(null);
  const [resetVersion, setResetVersion] = useState(0);
  const [reloadKey, setReloadKey] = useState(0);
  const [isLoadingMore, setIsLoadingMore] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);
  const byteOffsetRef = useRef(0);
  const encoderRef = useRef(new TextEncoder());
  const pendingSizeRef = useRef<{ cols: number; rows: number } | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);

  // Infinite scroll state — refs to avoid re-renders and stale closures
  const allLinesRef = useRef<string[]>([]);
  const oldestLineRef = useRef<number>(Infinity);
  const agentNameRef = useRef(agentName);
  useEffect(() => { agentNameRef.current = agentName; }, [agentName]);
  const modeRef = useRef(mode);
  useEffect(() => { modeRef.current = mode; }, [mode]);

  const hasMoreLines = oldestLineRef.current > 1 && mode === 'archive';

  const resize = useCallback((cols: number, rows: number) => {
    const safeCols = clampTerminalSize(cols, 1, 500);
    const safeRows = clampTerminalSize(rows, 1, 200);
    pendingSizeRef.current = { cols: safeCols, rows: safeRows };

    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    ws.send(buildResizeFrame(safeCols, safeRows));
  }, []);

  const sendInput = useCallback((data: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    ws.send(data);
  }, []);

  const refresh = useCallback(() => {
    setReloadKey((prev) => prev + 1);
  }, []);

  const loadOlderLogs = useCallback(async () => {
    const currentAgent = agentNameRef.current;
    if (
      !currentAgent ||
      modeRef.current !== 'archive' ||
      isLoadingMore ||
      oldestLineRef.current <= 1
    ) {
      return;
    }

    setIsLoadingMore(true);
    try {
      const archive = await getAgentLogArchive(
        currentAgent,
        archiveLines,
        oldestLineRef.current
      );

      // Guard: agent may have changed while fetching
      if (agentNameRef.current !== currentAgent || modeRef.current !== 'archive') {
        return;
      }

      if (archive.lines.length === 0) {
        // No more content — mark as at line 1
        oldestLineRef.current = 1;
        return;
      }

      // Prepend older lines
      oldestLineRef.current = archive.startLine;
      allLinesRef.current = [...archive.lines, ...allLinesRef.current];

      // Rebuild chunks from accumulated lines
      const text = allLinesRef.current.join('\n');
      const normalized = text.length > 0 && !text.endsWith('\n') ? `${text}\n` : text;
      const chunkBytes = encoderRef.current.encode(normalized);
      byteOffsetRef.current = chunkBytes.length;

      setChunks(
        chunkBytes.length > 0
          ? [
              {
                chunk: chunkBytes,
                byteOffset: chunkBytes.length,
                timestamp: new Date().toISOString(),
              },
            ]
          : []
      );
      setResetVersion((prev) => prev + 1);
    } catch (err) {
      // Silently fail — user can try scrolling up again
      const message = err instanceof Error ? err.message : 'Failed to load older logs';
      setError(message);
    } finally {
      setIsLoadingMore(false);
    }
  }, [archiveLines, isLoadingMore]);

  useEffect(() => {
    reconnectCancelRef.current?.();
    reconnectCancelRef.current = null;

    if (!enabled || !agentName) {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      byteOffsetRef.current = 0;
      allLinesRef.current = [];
      oldestLineRef.current = Infinity;
      setChunks([]);
      setMode('idle');
      setState('disconnected');
      setError(null);
      setIsLoadingMore(false);
      setResetVersion((prev) => prev + 1);
      return;
    }

    let cancelled = false;

    const closeSocket = () => {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };

    const start = async (): Promise<void> => {
      closeSocket();
      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;
      byteOffsetRef.current = 0;
      pendingSizeRef.current = null;
      allLinesRef.current = [];
      oldestLineRef.current = Infinity;
      setChunks([]);
      setState('connecting');
      setMode('loading');
      setError(null);
      setIsLoadingMore(false);
      setResetVersion((prev) => prev + 1);

      try {
        const transport = await getAgentTerminalInfo(agentName);
        if (cancelled) return;

        if (transport === 'archive') {
          const archive = await getAgentLogArchive(agentName, archiveLines);
          if (cancelled) return;

          // Store lines for infinite scroll
          allLinesRef.current = archive.lines ?? [];
          oldestLineRef.current = archive.startLine;

          const text = allLinesRef.current.join('\n');
          const normalized = text.length > 0 && !text.endsWith('\n') ? `${text}\n` : text;
          const chunkBytes = encoderRef.current.encode(normalized);
          byteOffsetRef.current = chunkBytes.length;

          setChunks(
            chunkBytes.length > 0
              ? [
                  {
                    chunk: chunkBytes,
                    byteOffset: chunkBytes.length,
                    timestamp: new Date().toISOString(),
                  },
                ]
              : []
          );
          setMode('archive');
          setState('connected');
          return;
        }

        const scheduleReconnect = () => {
          if (reconnectCancelRef.current) {
            return;
          }
          reconnectCancelRef.current = startAutoReconnect(
            () => {
              if (cancelled) {
                return true;
              }
              void connectTmux();
              return false;
            },
            () => {},
            DEFAULT_RECONNECT_CONFIG
          );
        };

        const connectTmux = async (): Promise<void> => {
          try {
            const token = await getAgentTerminalToken(agentName);
            if (cancelled) return;

            const ws = new WebSocket(getAgentTerminalWsUrl(agentName, token));
            ws.binaryType = 'arraybuffer';
            wsRef.current = ws;
            setMode('tmux');

            ws.onopen = () => {
              if (cancelled) return;
              reconnectCancelRef.current?.();
              reconnectCancelRef.current = null;
              setState('connected');
              setError(null);
              const pendingSize = pendingSizeRef.current;
              if (pendingSize) {
                ws.send(buildResizeFrame(pendingSize.cols, pendingSize.rows));
              }
            };

            ws.onmessage = (event: MessageEvent) => {
              if (cancelled) return;

              let bytes: Uint8Array;
              if (typeof event.data === 'string') {
                bytes = encoderRef.current.encode(event.data);
              } else if (event.data instanceof ArrayBuffer) {
                bytes = new Uint8Array(event.data);
              } else {
                return;
              }

              byteOffsetRef.current += bytes.length;
              const nextChunk: LogChunk = {
                chunk: bytes,
                byteOffset: byteOffsetRef.current,
                timestamp: new Date().toISOString(),
              };
              setChunks((prev) => [...prev, nextChunk]);
            };

            ws.onerror = () => {
              if (cancelled) return;
              setError('Terminal stream error');
            };

            ws.onclose = () => {
              if (cancelled) return;
              wsRef.current = null;
              setState('reconnecting');
              setResetVersion((prev) => prev + 1);
              setChunks([]);
              byteOffsetRef.current = 0;
              scheduleReconnect();
            };
          } catch (connectErr) {
            if (cancelled) return;
            const message =
              connectErr instanceof Error ? connectErr.message : 'Failed to connect terminal';
            setError(message);
            setState('reconnecting');
            scheduleReconnect();
          }
        };

        void connectTmux();
      } catch (err) {
        if (cancelled) return;
        const message = err instanceof Error ? err.message : 'Failed to load logs';
        setError(message);
        setState('disconnected');
        setMode('archive');
      }
    };

    void start();

    return () => {
      cancelled = true;
      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;
      closeSocket();
    };
  }, [agentName, archiveLines, enabled, reloadKey]);

  return {
    mode,
    chunks,
    state,
    error,
    resetVersion,
    refresh,
    resize,
    sendInput,
    loadOlderLogs,
    hasMoreLines,
    isLoadingMore,
  };
}
