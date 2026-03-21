/**
 * Hook for managing optimistic update lifecycle with SSE mutation buffering.
 * Tracks pending optimistic updates per-issue, buffers SSE mutations during
 * the optimistic window, and provides confirm/rollback handles.
 */

import { useCallback, useRef, useState } from "react";

import type { MutationPayload } from "@/api/sse";
import type { Issue } from "@/types";
import type { ToastOptions } from "./useToast";

/** Auto-rollback timeout (ms) — guards against lost API responses */
const AUTO_ROLLBACK_TIMEOUT_MS = 30_000;

/**
 * Internal state for a single optimistic update.
 */
interface OptimisticEntry {
  /** Pre-mutation snapshot for rollback */
  snapshot: Issue;
  /** SSE mutations buffered during the optimistic window */
  bufferedMutations: MutationPayload[];
  /** Timestamp when the optimistic update started */
  startedAt: number;
  /** Safety timeout ID for auto-rollback */
  timeoutId: ReturnType<typeof setTimeout>;
}

/**
 * Handle returned by startOptimistic for confirming or rolling back.
 */
export interface OptimisticHandle {
  /** Call on API success — clears optimistic state, flushes buffered SSE mutations */
  confirm: () => void;
  /** Call on API failure — restores snapshot, shows error toast */
  rollback: (errorMessage?: string) => void;
}

/**
 * Options for the useOptimisticUpdate hook.
 */
export interface UseOptimisticUpdateOptions {
  /** Setter for issue state (functional updates for race safety) */
  setIssuesMap: (
    updater:
      | Map<string, Issue>
      | ((prev: Map<string, Issue>) => Map<string, Issue>),
  ) => void;
  /** Mutation handler for replaying buffered SSE mutations */
  handleMutation: (mutation: MutationPayload) => void;
  /** Toast function for error notifications */
  showToast: (message: string, options?: ToastOptions) => string;
  /** Ref to track component mount state */
  mountedRef: React.RefObject<boolean>;
}

/**
 * Return type for the useOptimisticUpdate hook.
 */
export interface UseOptimisticUpdateReturn {
  /** Start an optimistic update for an issue. Returns null if issue is already optimistic. */
  startOptimistic: (
    issueId: string,
    snapshot: Issue,
  ) => OptimisticHandle | null;
  /** Set of issue IDs currently in optimistic state */
  pendingIds: Set<string>;
  /** Check if an issue is currently in optimistic state */
  isOptimistic: (issueId: string) => boolean;
  /** Filter function for SSE mutations — returns true to pass through, false to buffer */
  filterMutation: (mutation: MutationPayload) => boolean;
}

/**
 * Hook for managing optimistic update lifecycle.
 *
 * Provides per-issue optimistic state tracking, SSE mutation buffering during
 * the optimistic window, auto-rollback safety net, and confirm/rollback handles.
 */
export function useOptimisticUpdate(
  options: UseOptimisticUpdateOptions,
): UseOptimisticUpdateReturn {
  const { setIssuesMap, handleMutation, showToast, mountedRef } = options;

  // Internal state map (ref to avoid re-renders on mutation buffering)
  const optimisticMapRef = useRef<Map<string, OptimisticEntry>>(new Map());

  // Reactive pending IDs for component rendering
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set());

  // Store callbacks in refs to avoid stale closures in timeouts
  const handleMutationRef = useRef(handleMutation);
  handleMutationRef.current = handleMutation;
  const showToastRef = useRef(showToast);
  showToastRef.current = showToast;
  const setIssuesMapRef = useRef(setIssuesMap);
  setIssuesMapRef.current = setIssuesMap;

  /**
   * Remove an entry from the optimistic map and update pendingIds.
   */
  const removeEntry = useCallback((issueId: string) => {
    const entry = optimisticMapRef.current.get(issueId);
    if (entry) {
      clearTimeout(entry.timeoutId);
      optimisticMapRef.current.delete(issueId);
      setPendingIds((prev) => {
        const next = new Set(prev);
        next.delete(issueId);
        return next;
      });
    }
  }, []);

  /**
   * Flush buffered mutations for an issue through the mutation handler.
   */
  const flushBufferedMutations = useCallback((issueId: string) => {
    const entry = optimisticMapRef.current.get(issueId);
    if (!entry) return;
    for (const mutation of entry.bufferedMutations) {
      handleMutationRef.current(mutation);
    }
  }, []);

  /**
   * Start an optimistic update for an issue.
   * Returns a handle with confirm/rollback methods, or null if the issue
   * is already in an optimistic state (prevents concurrent drags of same issue).
   */
  const startOptimistic = useCallback(
    (issueId: string, snapshot: Issue): OptimisticHandle | null => {
      // Reject concurrent optimistic on same issue
      if (optimisticMapRef.current.has(issueId)) {
        return null;
      }

      // Auto-rollback timeout
      const timeoutId = setTimeout(() => {
        if (!mountedRef.current) return;
        const entry = optimisticMapRef.current.get(issueId);
        if (!entry) return;

        // Restore snapshot
        setIssuesMapRef.current((currentMap) => {
          const newMap = new Map(currentMap);
          newMap.set(issueId, entry.snapshot);
          return newMap;
        });

        // Flush buffered mutations (they represent server truth)
        for (const mutation of entry.bufferedMutations) {
          handleMutationRef.current(mutation);
        }

        // Clean up
        optimisticMapRef.current.delete(issueId);
        setPendingIds((prev) => {
          const next = new Set(prev);
          next.delete(issueId);
          return next;
        });

        showToastRef.current("Update timed out — changes reverted", {
          type: "error",
        });
      }, AUTO_ROLLBACK_TIMEOUT_MS);

      // Register the optimistic entry
      const entry: OptimisticEntry = {
        snapshot,
        bufferedMutations: [],
        startedAt: Date.now(),
        timeoutId,
      };
      optimisticMapRef.current.set(issueId, entry);
      setPendingIds((prev) => {
        const next = new Set(prev);
        next.add(issueId);
        return next;
      });

      const handle: OptimisticHandle = {
        confirm: () => {
          if (!optimisticMapRef.current.has(issueId)) return;
          flushBufferedMutations(issueId);
          removeEntry(issueId);
        },
        rollback: (errorMessage?: string) => {
          const currentEntry = optimisticMapRef.current.get(issueId);
          if (!currentEntry) return;

          if (mountedRef.current) {
            // Restore snapshot via functional update
            setIssuesMapRef.current((currentMap) => {
              const newMap = new Map(currentMap);
              newMap.set(issueId, currentEntry.snapshot);
              return newMap;
            });

            // Flush buffered mutations (server truth)
            for (const mutation of currentEntry.bufferedMutations) {
              handleMutationRef.current(mutation);
            }

            if (errorMessage) {
              showToastRef.current(errorMessage, { type: "error" });
            }
          }

          removeEntry(issueId);
        },
      };

      return handle;
    },
    [mountedRef, flushBufferedMutations, removeEntry],
  );

  /**
   * Check if an issue is currently in optimistic state.
   */
  const isOptimistic = useCallback(
    (issueId: string): boolean => pendingIds.has(issueId),
    [pendingIds],
  );

  /**
   * Filter function for SSE mutations.
   * Returns true to pass through (process normally), false to buffer (skip processing).
   * When false is returned, the mutation is stored internally for later replay.
   */
  const filterMutation = useCallback((mutation: MutationPayload): boolean => {
    const entry = optimisticMapRef.current.get(mutation.issue_id);
    if (!entry) return true; // Not optimistic — pass through

    // Buffer the mutation for later replay
    entry.bufferedMutations.push(mutation);
    return false;
  }, []);

  return {
    startOptimistic,
    pendingIds,
    isOptimistic,
    filterMutation,
  };
}
