/**
 * useDiff - React hook for fetching and caching diff data (file list + patches).
 *
 * Per-file error tracking: patch fetch failures are tracked per-file in `patchErrors`
 * instead of a single global `error` state. This ensures one file's failure does not
 * affect the display of other files.
 *
 * Global `error` is reserved for file-list-level failures only.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import {
  fetchDiffFiles as apiFetchDiffFiles,
  fetchDiffFile as apiFetchDiffFile,
  type DiffFile,
  type DiffFilePatch,
} from "@/api/diff";

export type { DiffFile, DiffFilePatch };

/** Return type for the useDiff hook. */
export interface UseDiffReturn {
  /** List of changed files. */
  files: DiffFile[];
  /** Whether file list is loading. */
  isLoading: boolean;
  /** File-list-level error (null if no error). */
  error: Error | null;
  /** Cached patches keyed by file path. */
  patchCache: Map<string, DiffFilePatch>;
  /** Per-file patch fetch errors keyed by file path. */
  patchErrors: Map<string, Error>;
  /** Fetch the patch for a specific file. */
  fetchPatch: (path: string) => Promise<void>;
  /** Clear a specific file's error (for retry). */
  clearPatchError: (path: string) => void;
}

/** Options for useDiff. */
export interface UseDiffOptions {
  /** Agent name/ID. */
  agentId: string | null;
  /** Target commit hash. */
  toCommit: string;
  /** Base commit hash (optional). */
  fromCommit?: string;
}

/**
 * React hook for fetching and managing diff data with per-file error tracking.
 */
export function useDiff(options: UseDiffOptions): UseDiffReturn {
  const { agentId, toCommit, fromCommit } = options;

  const [files, setFiles] = useState<DiffFile[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [patchCache, setPatchCache] = useState<Map<string, DiffFilePatch>>(
    () => new Map(),
  );
  const [patchErrors, setPatchErrors] = useState<Map<string, Error>>(
    () => new Map(),
  );

  const mountedRef = useRef(true);
  const patchCacheRef = useRef(patchCache);
  patchCacheRef.current = patchCache;

  // Reset all state when agent or commit changes
  useEffect(() => {
    setFiles([]);
    setIsLoading(false);
    setError(null);
    setPatchCache(new Map());
    setPatchErrors(new Map());
  }, [agentId, toCommit, fromCommit]);

  // Fetch file list when agent and commit are set
  useEffect(() => {
    if (!agentId || !toCommit) return;

    let cancelled = false;
    setIsLoading(true);
    setError(null);

    apiFetchDiffFiles(agentId, toCommit, fromCommit)
      .then((data) => {
        if (!cancelled && mountedRef.current) {
          setFiles(data);
        }
      })
      .catch((err) => {
        if (!cancelled && mountedRef.current) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      })
      .finally(() => {
        if (!cancelled && mountedRef.current) {
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [agentId, toCommit, fromCommit]);

  // Fetch patch for a specific file (per-file error tracking)
  const fetchPatch = useCallback(
    async (path: string) => {
      if (!agentId || !toCommit) return;
      if (patchCacheRef.current.has(path)) return;

      // Clear any previous error for this file
      setPatchErrors((prev) => {
        if (!prev.has(path)) return prev;
        const next = new Map(prev);
        next.delete(path);
        return next;
      });

      try {
        const patch = await apiFetchDiffFile(
          agentId,
          path,
          toCommit,
          fromCommit,
        );
        if (!mountedRef.current) return;

        setPatchCache((prev) => {
          const next = new Map(prev);
          next.set(path, patch);
          return next;
        });
      } catch (err) {
        if (!mountedRef.current) return;

        // Set per-file error — NOT global error
        const fetchError =
          err instanceof Error ? err : new Error(String(err));
        setPatchErrors((prev) => {
          const next = new Map(prev);
          next.set(path, fetchError);
          return next;
        });
      }
    },
    [agentId, toCommit, fromCommit],
  );

  const clearPatchError = useCallback((path: string) => {
    setPatchErrors((prev) => {
      if (!prev.has(path)) return prev;
      const next = new Map(prev);
      next.delete(path);
      return next;
    });
  }, []);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  return {
    files,
    isLoading,
    error,
    patchCache,
    patchErrors,
    fetchPatch,
    clearPatchError,
  };
}
