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
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { fetchDiffFiles, fetchDiffFile } from "@/api/issues";
import type { DiffFile, DiffFilePatch } from "@/api/issues";

import { agentQueryKeys } from "@/hooks/queryKeys";
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

function toError(error: unknown): Error | null {
  if (error == null) return null;
  return error instanceof Error ? error : new Error(String(error));
}

const EMPTY_DIFF_FILES: DiffFile[] = [];

export function useDiff({
  agentName,
  enabled,
  commitSignal,
}: UseDiffOptions): UseDiffReturn {
  const { workspaceId } = useWorkspaceContext();
  const queryClient = useQueryClient();
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [patchCache, setPatchCache] = useState<Map<string, DiffFilePatch>>(
    new Map(),
  );
  const [patchErrors, setPatchErrors] = useState<Map<string, Error>>(new Map());

  const mountedRef = useRef(true);
  const patchCacheRef = useRef(patchCache);
  patchCacheRef.current = patchCache;
  const inFlightPatchesRef = useRef(new Set<string>());
  const previousInputsRef = useRef<{
    agentName: string | null;
    commitSignal: number | undefined;
  } | null>(null);
  const diffFilesQueryKey = agentQueryKeys.diffFiles(
    workspaceId,
    agentName ?? "",
    "HEAD",
  );
  const canFetchDiffFiles = enabled && !!agentName;

  const diffFilesQuery = useQuery({
    queryKey: diffFilesQueryKey,
    queryFn: () => fetchDiffFiles(workspaceId, agentName ?? "", "HEAD"),
    enabled: canFetchDiffFiles,
  });

  // Track mount/unmount independently of fetch logic
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Reset all state when agent changes
  useEffect(() => {
    setPatchCache(new Map());
    setPatchErrors(new Map());
    setViewedFiles(new Set());
    inFlightPatchesRef.current.clear();

    const previous = previousInputsRef.current;
    const commitSignalChanged =
      previous !== null &&
      previous.agentName === agentName &&
      previous.commitSignal !== commitSignal;
    previousInputsRef.current = { agentName, commitSignal };

    if (commitSignalChanged && agentName) {
      void queryClient.invalidateQueries({
        queryKey: agentQueryKeys.diffFiles(workspaceId, agentName, "HEAD"),
      });
    }
  }, [agentName, commitSignal, queryClient, workspaceId]);

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

  const files = diffFilesQuery.data ?? EMPTY_DIFF_FILES;
  const error = toError(diffFilesQuery.error);
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
    isLoading: diffFilesQuery.isFetching,
    error,
    patchErrors,
    viewedFiles,
    markViewed,
    patchCache,
    fetchPatch,
    summaryStats,
  };
}
