/**
 * useDiff - React hook for managing diff viewer state.
 * Fetches file list for an agent's worktree diff, lazy-fetches individual
 * file patches with Map-based caching, tracks viewed files, and derives
 * summary statistics. Resets all state when agent changes.
 *
 * Combines useGitStatus pattern (mountedRef, reset on agent change, enabled flag)
 * with useFileContent pattern (on-demand fetching, latest-wins semantics).
 */

import { useState, useEffect, useRef, useCallback, useMemo } from "react";

import { fetchDiffFiles, fetchDiffFile } from "@/api/diff";
import type { DiffFile, DiffFilePatch } from "@/api/diff";

export interface UseDiffOptions {
  agentName: string | null;
  enabled: boolean;
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
  viewedFiles: Set<string>;
  markViewed: (path: string) => void;
  patchCache: Map<string, DiffFilePatch>;
  fetchPatch: (path: string) => Promise<void>;
  summaryStats: SummaryStats;
}

export function useDiff({ agentName, enabled }: UseDiffOptions): UseDiffReturn {
  const [files, setFiles] = useState<DiffFile[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [patchCache, setPatchCache] = useState<Map<string, DiffFilePatch>>(
    new Map(),
  );

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
    setViewedFiles(new Set());
    setError(null);
    setIsLoading(false);
    inFlightPatchesRef.current.clear();
  }, [agentName]);

  // Fetch file list when enabled with valid agent
  useEffect(() => {
    if (!enabled || !agentName) return;
    if (fetchInProgressRef.current) return;

    fetchInProgressRef.current = true;
    setIsLoading(true);

    fetchDiffFiles(agentName, "HEAD")
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
  }, [enabled, agentName]);

  const fetchPatch = useCallback(
    async (path: string): Promise<void> => {
      if (!path || !agentName) return;
      if (patchCacheRef.current.has(path)) return;
      if (inFlightPatchesRef.current.has(path)) return;

      inFlightPatchesRef.current.add(path);
      try {
        const result = await fetchDiffFile(agentName, path, "HEAD");
        if (mountedRef.current) {
          setPatchCache((prev) => new Map(prev).set(path, result));
        }
      } catch (err) {
        if (mountedRef.current) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      } finally {
        inFlightPatchesRef.current.delete(path);
      }
    },
    [agentName],
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
    viewedFiles,
    markViewed,
    patchCache,
    fetchPatch,
    summaryStats,
  };
}
