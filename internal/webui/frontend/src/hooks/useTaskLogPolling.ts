import { useCallback, useEffect, useRef, useState } from "react";

import { getTaskLogContent } from "@/api";

import { useWorkspaceContext } from "./useWorkspaceContext";

import type { LogChunk, LogStreamState } from "./logTypes";

export interface UseTaskLogPollingOptions {
  taskId: string | null;
  phase: "planning" | "implementation" | null;
  enabled: boolean;
  lines?: number;
  pollIntervalMs?: number;
}

export interface UseTaskLogPollingReturn {
  chunks: LogChunk[];
  state: LogStreamState;
  error: string | null;
  resetVersion: number;
  refresh: () => void;
}

export function useTaskLogPolling({
  taskId,
  phase,
  enabled,
  lines = 500,
  pollIntervalMs = 2000,
}: UseTaskLogPollingOptions): UseTaskLogPollingReturn {
  const { workspaceId } = useWorkspaceContext();
  const [chunks, setChunks] = useState<LogChunk[]>([]);
  const [state, setState] = useState<LogStreamState>("disconnected");
  const [error, setError] = useState<string | null>(null);
  const [resetVersion, setResetVersion] = useState(0);
  const [reloadKey, setReloadKey] = useState(0);

  const lastSnapshotRef = useRef("");
  const hasConnectedRef = useRef(false);
  const encoderRef = useRef(new TextEncoder());

  const refresh = useCallback(() => {
    setReloadKey((prev) => prev + 1);
  }, []);

  useEffect(() => {
    if (!enabled || !taskId || !phase) {
      setChunks([]);
      setState("disconnected");
      setError(null);
      setResetVersion((prev) => prev + 1);
      lastSnapshotRef.current = "";
      hasConnectedRef.current = false;
      return;
    }

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const scheduleNext = () => {
      if (cancelled) return;
      timer = setTimeout(fetchSnapshot, pollIntervalMs);
    };

    const fetchSnapshot = async () => {
      if (cancelled) return;

      setState(hasConnectedRef.current ? "reconnecting" : "connecting");

      try {
        const snapshot = await getTaskLogContent(
          workspaceId,
          taskId,
          phase,
          lines,
        );
        if (cancelled) return;

        const text = (snapshot.lines ?? []).join("\n");
        const normalized =
          text.length > 0 && !text.endsWith("\n") ? `${text}\n` : text;

        if (normalized !== lastSnapshotRef.current) {
          const bytes = encoderRef.current.encode(normalized);
          setChunks(
            bytes.length > 0
              ? [
                  {
                    chunk: bytes,
                    byteOffset: bytes.length,
                    timestamp: new Date().toISOString(),
                  },
                ]
              : [],
          );
          setResetVersion((prev) => prev + 1);
          lastSnapshotRef.current = normalized;
        }

        setError(null);
        setState("connected");
        hasConnectedRef.current = true;
      } catch (err) {
        if (cancelled) return;
        const message =
          err instanceof Error ? err.message : "Failed to fetch task logs";
        setError(message);
        setState(hasConnectedRef.current ? "reconnecting" : "disconnected");
      } finally {
        scheduleNext();
      }
    };

    void fetchSnapshot();

    return () => {
      cancelled = true;
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [enabled, lines, phase, pollIntervalMs, reloadKey, workspaceId, taskId]);

  return {
    chunks,
    state,
    error,
    resetVersion,
    refresh,
  };
}
