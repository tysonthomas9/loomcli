/**
 * React hook for optimistic status updates with automatic rollback on failure.
 * Used by KanbanBoard to handle drag-and-drop status changes with immediate UI feedback.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { produce } from 'immer';
import { updateIssue, ApiError } from '@/api';
import type { Issue, Status } from '@/types';

/**
 * Options for the useOptimisticStatusUpdate hook.
 */
export interface UseOptimisticStatusUpdateOptions {
  /** Current issue state as a Map for O(1) lookups */
  issues: Map<string, Issue>;

  /** Callback to update issue state */
  setIssues: (
    issues: Map<string, Issue> | ((prev: Map<string, Issue>) => Map<string, Issue>)
  ) => void;

  /** Callback when an API call fails and rollback occurs */
  onError?: (issueId: string, error: Error, oldStatus: Status, newStatus: Status) => void;

  /** Callback when an API call succeeds */
  onSuccess?: (issueId: string, newStatus: Status) => void;
}

/**
 * Return type for the useOptimisticStatusUpdate hook.
 */
export interface UseOptimisticStatusUpdateReturn {
  /** Update an issue's status with optimistic update and rollback on failure */
  updateIssueStatus: (issueId: string, newStatus: Status, oldStatus: Status) => Promise<void>;

  /** Set of issue IDs currently being updated (pending API calls) */
  pendingUpdates: Set<string>;

  /** Last error that occurred, if any */
  lastError: { issueId: string; error: Error } | null;
}

/**
 * Snapshot of an issue before optimistic update, used for rollback.
 */
interface IssueSnapshot {
  issue: Issue;
  oldStatus: Status;
  newStatus: Status;
}

/**
 * React hook that encapsulates the optimistic update + rollback pattern for issue status changes.
 *
 * The hook:
 * 1. Immediately updates local state when a status change is requested
 * 2. Makes an API call to persist the change
 * 3. If the API call fails, automatically rolls back to the previous state
 * 4. Tracks pending updates to prevent duplicate requests for the same issue
 *
 * @example
 * ```tsx
 * function KanbanPage() {
 *   const [issues, setIssues] = useState<Map<string, Issue>>(new Map())
 *
 *   const { updateIssueStatus, pendingUpdates } = useOptimisticStatusUpdate({
 *     issues,
 *     setIssues,
 *     onError: (issueId, error, oldStatus, newStatus) => {
 *       showToast(`Failed to move issue: ${error.message}`)
 *     },
 *     onSuccess: (issueId, newStatus) => {
 *       console.log(`Issue ${issueId} moved to ${newStatus}`)
 *     },
 *   })
 *
 *   const handleDragEnd = createDragEndHandler({
 *     onIssueStatusChange: (issueId, newStatus) => {
 *       const issue = issues.get(issueId)
 *       if (issue) {
 *         const oldStatus = issue.status ?? 'open'
 *         updateIssueStatus(issueId, newStatus, oldStatus)
 *       }
 *     },
 *   })
 *
 *   return <KanbanBoard issues={issues} onDragEnd={handleDragEnd} />
 * }
 * ```
 */
export function useOptimisticStatusUpdate(
  options: UseOptimisticStatusUpdateOptions
): UseOptimisticStatusUpdateReturn {
  const { issues, setIssues, onError, onSuccess } = options;

  // Track pending updates to prevent duplicate requests
  const [pendingUpdates, setPendingUpdates] = useState<Set<string>>(() => new Set());

  // Track last error for consumers who need it
  const [lastError, setLastError] = useState<{ issueId: string; error: Error } | null>(null);

  // Track mounted state to prevent state updates after unmount
  const mountedRef = useRef(true);

  // Store snapshots for rollback (not in state because we don't need re-render on snapshot changes)
  const snapshotsRef = useRef<Map<string, IssueSnapshot>>(new Map());

  // Store callbacks in refs to avoid stale closures
  const onErrorRef = useRef(onError);
  const onSuccessRef = useRef(onSuccess);

  // Keep refs up to date
  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  useEffect(() => {
    onSuccessRef.current = onSuccess;
  }, [onSuccess]);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    const snapshots = snapshotsRef.current;
    return () => {
      mountedRef.current = false;
      snapshots.clear();
    };
  }, []);

  /**
   * Apply optimistic update to local state using functional update to avoid stale closures.
   * Returns the snapshot for potential rollback via a callback.
   */
  const applyOptimisticUpdate = useCallback(
    (issueId: string, newStatus: Status, oldStatus: Status): IssueSnapshot | null => {
      let snapshot: IssueSnapshot | null = null;

      // Read current issue from the issues prop (synchronous, called immediately)
      const issue = issues.get(issueId);
      if (!issue) return null;

      // Create snapshot for rollback
      snapshot = {
        issue: { ...issue },
        oldStatus,
        newStatus,
      };

      // Apply optimistic update using immer
      const updatedIssue = produce(issue, (draft) => {
        draft.status = newStatus;
        draft.updated_at = new Date().toISOString();
      });

      // Update state with new issue
      const newIssues = new Map(issues);
      newIssues.set(issueId, updatedIssue);
      setIssues(newIssues);

      return snapshot;
    },
    [issues, setIssues]
  );

  /**
   * Roll back an optimistic update by restoring the snapshot.
   * Uses functional update to get current state and avoid stale closure issues.
   */
  const rollbackUpdate = useCallback(
    (snapshot: IssueSnapshot) => {
      if (!mountedRef.current) return;

      // Use functional update to ensure we're working with current state
      setIssues((currentIssues) => {
        const newIssues = new Map(currentIssues);
        newIssues.set(snapshot.issue.id, snapshot.issue);
        return newIssues;
      });
    },
    [setIssues]
  );

  /**
   * Apply server response to state after successful API call.
   */
  const applyServerResponse = useCallback(
    (issueId: string, serverIssue: Issue) => {
      if (!mountedRef.current) return;

      setIssues((currentIssues) => {
        const newIssues = new Map(currentIssues);
        newIssues.set(issueId, serverIssue);
        return newIssues;
      });
    },
    [setIssues]
  );

  /**
   * Update an issue's status with optimistic update and automatic rollback on failure.
   */
  const updateIssueStatus = useCallback(
    async (issueId: string, newStatus: Status, oldStatus: Status): Promise<void> => {
      // Skip if already updating this issue
      if (pendingUpdates.has(issueId)) {
        return;
      }

      // Skip if no actual status change
      if (oldStatus === newStatus) {
        return;
      }

      // Apply optimistic update
      const snapshot = applyOptimisticUpdate(issueId, newStatus, oldStatus);
      if (!snapshot) {
        // Issue not found - nothing to do
        return;
      }

      // Store snapshot for potential rollback
      snapshotsRef.current.set(issueId, snapshot);

      // Mark as pending
      setPendingUpdates((prev) => {
        const next = new Set(prev);
        next.add(issueId);
        return next;
      });

      try {
        // Make API call to persist the change
        // The API client has a 30s default timeout which is sufficient
        const serverIssue = await updateIssue(issueId, { status: newStatus });

        // Success - update with server response to ensure consistency
        snapshotsRef.current.delete(issueId);

        if (mountedRef.current) {
          // Apply the server's authoritative response
          applyServerResponse(issueId, serverIssue);
          setLastError(null);
          onSuccessRef.current?.(issueId, newStatus);
        }
      } catch (error) {
        // Get the stored snapshot for rollback
        const storedSnapshot = snapshotsRef.current.get(issueId);
        if (storedSnapshot) {
          rollbackUpdate(storedSnapshot);
          snapshotsRef.current.delete(issueId);
        }

        // Handle different error types
        let errorToReport: Error;

        if (error instanceof ApiError) {
          if (error.status === 404) {
            // Issue was deleted - don't report as error, just log
            // The issue should be removed from state by SSE mutation handler
            console.warn(`Issue ${issueId} not found (404) - may have been deleted`);
            errorToReport = new Error('Issue no longer exists');
          } else if (error.status === 409) {
            // Conflict - server has different state
            // SSE mutation handler will eventually sync correct state
            errorToReport = new Error('Conflict with server state');
          } else if (error.status === 0) {
            // Network error or timeout from API client
            errorToReport = new Error(error.statusText || 'Network error');
          } else {
            errorToReport = new Error(`Failed to update status: ${error.statusText}`);
          }
        } else if (error instanceof Error) {
          errorToReport = new Error(`Network error: ${error.message}`);
        } else {
          errorToReport = new Error('Unknown error occurred');
        }

        if (mountedRef.current) {
          setLastError({ issueId, error: errorToReport });
          onErrorRef.current?.(issueId, errorToReport, oldStatus, newStatus);
        }
      } finally {
        // Clear pending state
        if (mountedRef.current) {
          setPendingUpdates((prev) => {
            const next = new Set(prev);
            next.delete(issueId);
            return next;
          });
        }
      }
    },
    [pendingUpdates, applyOptimisticUpdate, rollbackUpdate, applyServerResponse]
  );

  return {
    updateIssueStatus,
    pendingUpdates,
    lastError,
  };
}
