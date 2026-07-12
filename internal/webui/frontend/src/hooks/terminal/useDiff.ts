/**
 * useDiff - React hook for managing diff viewer state.
 * Fetches file list for an agent's worktree diff, lazy-fetches individual
 * file patches with Map-based caching, tracks viewed files, and derives
 * summary statistics. Resets all state when agent changes.
 *
 * Combines the useGitStatus mountedRef/reset pattern with on-demand fetching
 * and latest-wins semantics.
 */

import { useState, useEffect, useRef, useCallback, useMemo } from "react";

import { fetchDiffFiles, fetchDiffFile } from "@/api/issues";
import type { DiffFile, DiffFilePatch } from "@/api/issues";

import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseDiffOptions {
  agentName: string | null;
  enabled: boolean;
  commitSignal?: number;
}

export interface SummaryStats {
  filesChanged: number;
  additions: number;
  deletions: number;
}

export interface UseDiffReturn {
  files: DiffFile[];
  isLoading: boolean;
  error: Error | null;
  patchErrors: Map<string, Error>;
  viewedFiles: Set<string>;
  markViewed: (path: string) => void;
  patchCache: Map<string, DiffFilePatch>;
  fetchPatch: (path: string) => Promise<void>;
  summaryStats: SummaryStats;
}

export function useDiff({
  agentName,
  enabled,
  commitSignal,
}: UseDiffOptions): UseDiffReturn {
  const { workspaceId } = useWorkspaceContext();
  const [files, setFiles] = useState<DiffFile[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [patchCache, setPatchCache] = useState<Map<string, DiffFilePatch>>(
    new Map(),
  );
  const [patchErrors, setPatchErrors] = useState<Map<string, Error>>(new Map());

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);
  const patchCacheRef = useRef(patchCache);
  patchCacheRef.current = patchCache;
  const inFlightPatchesRef = useRef(new Set<string>());

  // Track mount/unmount independently of fetch logic
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Reset all state when agent changes
  useEffect(() => {
    setFiles([]);
    setPatchCache(new Map());
    setPatchErrors(new Map());
    setViewedFiles(new Set());
    setError(null);
    setIsLoading(false);
    fetchInProgressRef.current = false;
    inFlightPatchesRef.current.clear();
  }, [agentName, commitSignal]);

  // Allow fresh fetch when re-enabled after being disabled while fetch was in-flight
  useEffect(() => {
    if (!enabled) {
      fetchInProgressRef.current = false;
    }
  }, [enabled]);

  // Fetch file list when enabled with valid agent
  useEffect(() => {
    if (!enabled || !agentName) return;
    if (fetchInProgressRef.current) return;

    fetchInProgressRef.current = true;
    setIsLoading(true);

    fetchDiffFiles(workspaceId, agentName, "HEAD")
      .then((result) => {
        if (mountedRef.current) {
          setFiles(result);
          setError(null);
        }
      })
      .catch((err) => {
        if (mountedRef.current) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      })
      .finally(() => {
        fetchInProgressRef.current = false;
        if (mountedRef.current) {
          setIsLoading(false);
        }
      });
  }, [enabled, agentName, commitSignal]);

  const fetchPatch = useCallback(
    async (path: string): Promise<void> => {
      if (!path || !agentName) return;
      if (patchCacheRef.current.has(path)) return;
      if (inFlightPatchesRef.current.has(path)) return;

      inFlightPatchesRef.current.add(path);
      setPatchErrors((prev) => {
        if (!prev.has(path)) return prev;
        const next = new Map(prev);
        next.delete(path);
        return next;
      });
      try {
        const result = await fetchDiffFile(
          workspaceId,
          agentName,
          path,
          "HEAD",
        );
        if (mountedRef.current) {
          setPatchCache((prev) => new Map(prev).set(path, result));
        }
      } catch (err) {
        if (mountedRef.current) {
          const error = err instanceof Error ? err : new Error(String(err));
          setPatchErrors((prev) => new Map(prev).set(path, error));
        }
      } finally {
        inFlightPatchesRef.current.delete(path);
      }
    },
    [workspaceId, agentName],
  );

  const markViewed = useCallback((path: string): void => {
    setViewedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  }, []);

  const summaryStats = useMemo<SummaryStats>(
    () => ({
      filesChanged: files.length,
      additions: files.reduce((sum, f) => sum + f.additions, 0),
      deletions: files.reduce((sum, f) => sum + f.deletions, 0),
    }),
    [files],
  );

  return {
    files,
    isLoading,
    error,
    patchErrors,
    viewedFiles,
    markViewed,
    patchCache,
    fetchPatch,
    summaryStats,
  };
}
