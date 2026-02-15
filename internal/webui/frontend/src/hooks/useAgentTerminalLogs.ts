import { useCallback, useEffect, useRef, useState } from 'react';

import {
  getAgentLogArchive,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
} from '@/api';
import { calculateBackoffDelay, DEFAULT_RECONNECT_CONFIG } from '@/utils/reconnectBackoff';

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

const ARCHIVE_RECHECK_INTERVAL_MS = 5000;

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
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  const archiveProbeTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const archiveProbeInFlightRef = useRef(false);
  const runIdRef = useRef(0);
  const isLoadingMoreRef = useRef(false);

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
      isLoadingMoreRef.current ||
      oldestLineRef.current <= 1
    ) {
      return;
    }

    isLoadingMoreRef.current = true;
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
      isLoadingMoreRef.current = false;
      setIsLoadingMore(false);
    }
  }, [archiveLines]);

  useEffect(() => {
    runIdRef.current += 1;
    const runId = runIdRef.current;
    let cancelled = false;

    const isCurrentRun = () => !cancelled && runIdRef.current === runId;

    const closeSocket = () => {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };

    const stopReconnectTimer = () => {
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
    };

    const stopArchiveProbe = () => {
      if (archiveProbeTimerRef.current) {
        clearInterval(archiveProbeTimerRef.current);
        archiveProbeTimerRef.current = null;
      }
      archiveProbeInFlightRef.current = false;
    };

    stopReconnectTimer();
    stopArchiveProbe();

    if (!enabled || !agentName) {
      closeSocket();
      reconnectAttemptRef.current = 0;
      byteOffsetRef.current = 0;
      allLinesRef.current = [];
      oldestLineRef.current = Infinity;
      setChunks([]);
      setMode('idle');
      setState('disconnected');
      setError(null);
      isLoadingMoreRef.current = false;
      setIsLoadingMore(false);
      setResetVersion((prev) => prev + 1);
      return;
    }

    const connectTmux = async (): Promise<void> => {
      if (!isCurrentRun()) return;
      stopArchiveProbe();
      closeSocket();
      setMode('tmux');
      setState('connecting');

      try {
        const token = await getAgentTerminalToken(agentName);
        if (!isCurrentRun()) return;

        const ws = new WebSocket(getAgentTerminalWsUrl(agentName, token));
        ws.binaryType = 'arraybuffer';
        wsRef.current = ws;

        ws.onopen = () => {
          if (!isCurrentRun() || wsRef.current !== ws) return;
          reconnectAttemptRef.current = 0;
          stopReconnectTimer();
          setState('connected');
          setError(null);
          const pendingSize = pendingSizeRef.current;
          if (pendingSize) {
            ws.send(buildResizeFrame(pendingSize.cols, pendingSize.rows));
          }
        };

        ws.onmessage = (event: MessageEvent) => {
          if (!isCurrentRun() || wsRef.current !== ws) return;

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
          if (!isCurrentRun() || wsRef.current !== ws) return;
          setError('Terminal stream error');
        };

        ws.onclose = () => {
          if (!isCurrentRun() || wsRef.current !== ws) return;
          wsRef.current = null;
          setState('reconnecting');

          const attempt = reconnectAttemptRef.current;
          if (attempt >= DEFAULT_RECONNECT_CONFIG.maxAttempts) {
            reconnectAttemptRef.current = 0;
            void (async () => {
              try {
                const transport = await getAgentTerminalInfo(agentName);
                if (!isCurrentRun()) return;

                if (transport === 'archive') {
                  const archive = await getAgentLogArchive(agentName, archiveLines);
                  if (!isCurrentRun()) return;

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
                  setResetVersion((prev) => prev + 1);

                  if (!archiveProbeTimerRef.current) {
                    archiveProbeTimerRef.current = setInterval(async () => {
                      if (!isCurrentRun() || archiveProbeInFlightRef.current) return;
                      archiveProbeInFlightRef.current = true;
                      try {
                        const probeTransport = await getAgentTerminalInfo(agentName);
                        if (!isCurrentRun()) return;
                        if (probeTransport === 'tmux') {
                          stopArchiveProbe();
                          reconnectAttemptRef.current = 0;
                          void connectTmux();
                        }
                      } catch {
                        // Keep archive mode active; probe again on next interval.
                      } finally {
                        archiveProbeInFlightRef.current = false;
                      }
                    }, ARCHIVE_RECHECK_INTERVAL_MS);
                  }
                  return;
                }
              } catch (err) {
                if (!isCurrentRun()) return;
                const message =
                  err instanceof Error ? err.message : 'Failed to inspect terminal availability';
                setError(message);
              }

              if (!isCurrentRun() || reconnectTimerRef.current) return;
              reconnectTimerRef.current = setTimeout(() => {
                reconnectTimerRef.current = null;
                if (!isCurrentRun()) return;
                reconnectAttemptRef.current = 1;
                void connectTmux();
              }, ARCHIVE_RECHECK_INTERVAL_MS);
            })();
            return;
          }

          if (reconnectTimerRef.current) {
            return;
          }

          const delay = calculateBackoffDelay(attempt, DEFAULT_RECONNECT_CONFIG);
          reconnectTimerRef.current = setTimeout(() => {
            reconnectTimerRef.current = null;
            if (!isCurrentRun()) return;
            reconnectAttemptRef.current += 1;
            void connectTmux();
          }, delay);
        };
      } catch (connectErr) {
        if (!isCurrentRun()) return;
        const message =
          connectErr instanceof Error ? connectErr.message : 'Failed to connect terminal';
        setError(message);
        setState('reconnecting');

        if (reconnectTimerRef.current) {
          return;
        }

        const attempt = reconnectAttemptRef.current;
        const delay = calculateBackoffDelay(attempt, DEFAULT_RECONNECT_CONFIG);
        reconnectTimerRef.current = setTimeout(() => {
          reconnectTimerRef.current = null;
          if (!isCurrentRun()) return;
          reconnectAttemptRef.current += 1;
          void connectTmux();
        }, delay);
      }
    };

    const start = async (): Promise<void> => {
      closeSocket();
      stopReconnectTimer();
      stopArchiveProbe();
      reconnectAttemptRef.current = 0;
      byteOffsetRef.current = 0;
      pendingSizeRef.current = null;
      allLinesRef.current = [];
      oldestLineRef.current = Infinity;
      setChunks([]);
      setState('connecting');
      setMode('loading');
      setError(null);
      isLoadingMoreRef.current = false;
      setIsLoadingMore(false);
      setResetVersion((prev) => prev + 1);

      try {
        const transport = await getAgentTerminalInfo(agentName);
        if (!isCurrentRun()) return;

        if (transport === 'archive') {
          const archive = await getAgentLogArchive(agentName, archiveLines);
          if (!isCurrentRun()) return;

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
          if (!archiveProbeTimerRef.current) {
            archiveProbeTimerRef.current = setInterval(async () => {
              if (!isCurrentRun() || archiveProbeInFlightRef.current) return;
              archiveProbeInFlightRef.current = true;
              try {
                const probeTransport = await getAgentTerminalInfo(agentName);
                if (!isCurrentRun()) return;
                if (probeTransport === 'tmux') {
                  stopArchiveProbe();
                  reconnectAttemptRef.current = 0;
                  void connectTmux();
                }
              } catch {
                // Keep archive mode active; probe again on next interval.
              } finally {
                archiveProbeInFlightRef.current = false;
              }
            }, ARCHIVE_RECHECK_INTERVAL_MS);
          }
          return;
        }

        void connectTmux();
      } catch (err) {
        if (!isCurrentRun()) return;
        const message = err instanceof Error ? err.message : 'Failed to load logs';
        setError(message);
        setState('disconnected');
        setMode('archive');
      }
    };

    void start();

    return () => {
      cancelled = true;
      isLoadingMoreRef.current = false;
      stopReconnectTimer();
      stopArchiveProbe();
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
