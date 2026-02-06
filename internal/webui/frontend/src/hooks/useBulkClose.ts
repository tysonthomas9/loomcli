/**
 * useBulkClose - React hook for closing multiple issues at once.
 * Provides loading state, error handling, and BulkActionToolbar integration.
 */

import { useState, useCallback, useRef, useEffect } from 'react';

import { closeIssue } from '@/api';
import type { BulkAction } from '@/components/BulkActionToolbar';

/**
 * Options for the useBulkClose hook.
 */
export interface UseBulkCloseOptions {
  /** Callback after all issues are successfully closed */
  onSuccess?: (closedIds: string[]) => void;
  /** Callback after partial success (some issues failed to close) */
  onPartialSuccess?: (closedIds: string[], failedIds: string[]) => void;
  /** Callback after total failure (no issues closed) */
  onError?: (error: Error, failedIds: string[]) => void;
  /** Optional reason to pass to closeIssue API */
  closeReason?: string;
}

/**
 * Return type for the useBulkClose hook.
 */
export interface UseBulkCloseReturn {
  /** Execute bulk close operation */
  bulkClose: (issueIds: Set<string> | string[]) => Promise<void>;
  /** Whether a bulk close operation is in progress */
  isLoading: boolean;
  /** Error message if the operation failed completely */
  error: string | null;
  /** Set of issue IDs that failed to close in the last operation */
  failedIds: Set<string>;
  /** Number of issues successfully closed in the last operation */
  successCount: number;
  /** Create a BulkAction object for use with BulkActionToolbar */
  createBulkAction: (options?: { label?: string }) => BulkAction;
  /** Reset error and failed state */
  reset: () => void;
}

/**
 * React hook for bulk closing issues.
 *
 * @example
 * ```tsx
 * const { bulkClose, isLoading, createBulkAction } = useBulkClose({
 *   onSuccess: (ids) => clearSelection(),
 * })
 * ```
 */
export function useBulkClose(options: UseBulkCloseOptions = {}): UseBulkCloseReturn {
  const { onSuccess, onPartialSuccess, onError, closeReason } = options;

  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [failedIds, setFailedIds] = useState<Set<string>>(new Set());
  const [successCount, setSuccessCount] = useState(0);

  // Track mounted state to prevent state updates after unmount
  const mountedRef = useRef(true);

  // Store callbacks in refs to avoid stale closures
  const onSuccessRef = useRef(onSuccess);
  const onPartialSuccessRef = useRef(onPartialSuccess);
  const onErrorRef = useRef(onError);

  useEffect(() => {
    onSuccessRef.current = onSuccess;
  }, [onSuccess]);
  useEffect(() => {
    onPartialSuccessRef.current = onPartialSuccess;
  }, [onPartialSuccess]);
  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      mountedRef.current = false;
    };
  }, []);

  /**
   * Execute bulk close operation.
   */
  const bulkClose = useCallback(
    async (issueIds: Set<string> | string[]) => {
      // Convert to array if Set
      const ids = Array.isArray(issueIds) ? issueIds : Array.from(issueIds);

      if (ids.length === 0) return;

      setIsLoading(true);
      setError(null);
      setFailedIds(new Set());
      setSuccessCount(0);

      try {
        // Close all issues in parallel
        const results = await Promise.allSettled(ids.map((id) => closeIssue(id, closeReason)));

        // Guard against unmount
        if (!mountedRef.current) return;

        // Categorize results
        const closedIds: string[] = [];
        const failedIdsList: string[] = [];

        results.forEach((result, index) => {
          const id = ids[index];
          if (id !== undefined) {
            if (result.status === 'fulfilled') {
              closedIds.push(id);
            } else {
              failedIdsList.push(id);
            }
          }
        });

        setSuccessCount(closedIds.length);
        setFailedIds(new Set(failedIdsList));

        // Determine outcome and call appropriate callback
        if (failedIdsList.length === 0) {
          // All succeeded
          onSuccessRef.current?.(closedIds);
        } else if (closedIds.length === 0) {
          // All failed - find first rejected result to get error message
          const firstRejected = results.find(
            (r): r is PromiseRejectedResult => r.status === 'rejected'
          );
          const errorMessage = firstRejected
            ? (firstRejected.reason as Error).message
            : 'All issues failed to close';
          setError(errorMessage);
          onErrorRef.current?.(new Error(errorMessage), failedIdsList);
        } else {
          // Partial success
          const errorMessage = `Closed ${closedIds.length} of ${ids.length} issues`;
          setError(errorMessage);
          onPartialSuccessRef.current?.(closedIds, failedIdsList);
        }
      } catch (err) {
        // Unexpected error (shouldn't happen with allSettled, but be safe)
        if (!mountedRef.current) return;
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        setError(errorMessage);
        onErrorRef.current?.(err instanceof Error ? err : new Error(errorMessage), ids);
      } finally {
        if (mountedRef.current) {
          setIsLoading(false);
        }
      }
    },
    // Only closeReason is a dependency; callbacks use refs to avoid stale closures
    [closeReason]
  );

  /**
   * Reset error and failed state.
   */
  const reset = useCallback(() => {
    setError(null);
    setFailedIds(new Set());
    setSuccessCount(0);
  }, []);

  /**
   * Create a BulkAction object for BulkActionToolbar integration.
   */
  const createBulkAction = useCallback(
    (actionOptions?: { label?: string }): BulkAction => ({
      id: 'close',
      label: actionOptions?.label ?? 'Close',
      variant: 'danger',
      loading: isLoading,
      disabled: isLoading,
      onClick: bulkClose,
    }),
    [isLoading, bulkClose]
  );

  return {
    bulkClose,
    isLoading,
    error,
    failedIds,
    successCount,
    createBulkAction,
    reset,
  };
}
