/**
 * useIssueDetail - React hook for fetching full issue details on demand.
 * Used when clicking a node to open the detail panel.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import { getIssue } from "@/api/issues";
import type { Issue, IssueDetails } from "@/types";

import { useWorkspaceContext } from "@/hooks/workspace";

/**
 * Return type for the useIssueDetail hook.
 */
export interface UseIssueDetailReturn {
  /** Full issue details, null if not loaded */
  issueDetails: IssueDetails | null;
  /** Whether a fetch is currently in progress */
  isLoading: boolean;
  /** Error from the last fetch attempt, null if successful */
  error: string | null;
  /** Fetch full details for an issue by ID */
  fetchIssue: (id: string) => Promise<void>;
  /** Clear the current issue details */
  clearIssue: () => void;
  /** Merge updated Issue fields into the current IssueDetails state */
  updateIssueDetails: (updated: Issue) => void;
}

/**
 * React hook for fetching full issue details on demand.
 *
 * @returns Object with issueDetails, isLoading, error states, and fetch/clear functions
 *
 * @example
 * ```tsx
 * function NodeClickHandler() {
 *   const { issueDetails, isLoading, error, fetchIssue, clearIssue } = useIssueDetail()
 *
 *   const handleNodeClick = (issue: Issue) => {
 *     fetchIssue(issue.id)
 *   }
 *
 *   const handleClose = () => {
 *     clearIssue()
 *   }
 *
 *   return (
 *     <IssueDetailPanel
 *       isOpen={!!issueDetails}
 *       issue={issueDetails}
 *       isLoading={isLoading}
 *       onClose={handleClose}
 *     />
 *   )
 * }
 * ```
 */
export function useIssueDetail(): UseIssueDetailReturn {
  const { workspaceId } = useWorkspaceContext();
  const [issueDetails, setIssueDetails] = useState<IssueDetails | null>(null);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  // Track the current request ID to handle concurrent requests (latest wins)
  const currentRequestIdRef = useRef<number>(0);

  // Track if the component is mounted
  const mountedRef = useRef<boolean>(true);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const fetchIssue = useCallback(
    async (id: string): Promise<void> => {
      // Validate ID
      if (!id) {
        return;
      }

      // Increment request ID to handle concurrent requests
      const requestId = ++currentRequestIdRef.current;

      setIsLoading(true);
      setError(null);

      try {
        const details = await getIssue(workspaceId, id);

        // Only update state if this is the latest request and still mounted
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setIssueDetails(details);
          setError(null);
        }
      } catch (err) {
        // Only update state if this is the latest request and still mounted
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          const errorMessage = err instanceof Error ? err.message : String(err);
          setError(errorMessage);
          // Don't clear existing details on error - show stale data with error
        }
      } finally {
        // Only update loading state if this is the latest request
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setIsLoading(false);
        }
      }
    },
    [workspaceId],
  );

  const clearIssue = useCallback(() => {
    currentRequestIdRef.current++; // Cancel any in-flight requests
    setIssueDetails(null);
    setError(null);
    setIsLoading(false);
  }, []);

  const updateIssueDetails = useCallback((updated: Issue) => {
    setIssueDetails((prev) => {
      if (!prev) return prev;
      // Build a partial update from defined fields only (respects exactOptionalPropertyTypes)
      const patch: Partial<IssueDetails> = {
        title: updated.title,
        priority: updated.priority,
        updated_at: updated.updated_at,
      };
      if (updated.status !== undefined) patch.status = updated.status;
      if (updated.issue_type !== undefined)
        patch.issue_type = updated.issue_type;
      if (updated.assignee !== undefined) patch.assignee = updated.assignee;
      if (updated.owner !== undefined) patch.owner = updated.owner;
      if (updated.description !== undefined)
        patch.description = updated.description;
      if (updated.design !== undefined) patch.design = updated.design;
      if (updated.notes !== undefined) patch.notes = updated.notes;
      if (updated.labels !== undefined) patch.labels = updated.labels;
      return { ...prev, ...patch };
    });
  }, []);

  return {
    issueDetails,
    isLoading,
    error,
    fetchIssue,
    clearIssue,
    updateIssueDetails,
  };
}
