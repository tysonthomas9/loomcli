/**
 * useIssueDetail - React hook for fetching full issue details on demand.
 * Used when clicking a node to open the detail panel.
 */

import {
  useState,
  useCallback,
  useRef,
  useEffect,
  useLayoutEffect,
  useMemo,
  useContext,
} from "react";

import { getIssue } from "@/api/issues";
import { ApiError } from "@/api/common/client";
import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";
import {
  QueryRecoveryContext,
  type QueryRecoveryCoordinator,
} from "@/hooks/common/queryRecovery";
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
  /** True only when the current selected detail returned HTTP 404. */
  isNotFound: boolean;
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
interface DetailScope {
  workspaceId: string;
}
interface DetailSelection {
  scope: DetailScope;
  id: string;
  revision: number;
  request: ScopedQueryRequest<IssueDetails>;
  unregister: (() => void) | null;
}
function retireSelection(selection: DetailSelection | null) {
  selection?.unregister?.();
  if (selection) selection.unregister = null;
  selection?.request.cancel();
}
function enrollSelection(
  selection: DetailSelection,
  recovery: QueryRecoveryCoordinator | null,
) {
  if (!recovery || selection.unregister) return;
  selection.unregister = recovery.register(
    `issue-detail:${selection.scope.workspaceId}:${selection.id}`,
    (signal) => selection.request.run({ signal, fresh: true }),
    () => selection.revision,
  );
}
function validDetails(
  value: IssueDetails,
  id: string,
  workspaceId: string,
): boolean {
  if (
    !value ||
    typeof value !== "object" ||
    value.id !== id ||
    typeof value.title !== "string" ||
    typeof value.priority !== "number" ||
    !Number.isFinite(value.priority) ||
    typeof value.created_at !== "string" ||
    typeof value.updated_at !== "string"
  )
    return false;
  const workspace = (value as IssueDetails & { workspace?: unknown }).workspace;
  return workspace === undefined || workspace === workspaceId;
}

export function useIssueDetail(): UseIssueDetailReturn {
  const { workspaceId } = useWorkspaceContext();
  const scope = useMemo(() => ({ workspaceId }), [workspaceId]);
  const scopeRef = useRef(scope);
  const recovery = useContext(QueryRecoveryContext);
  const recoveryRef = useRef(recovery);
  const selectionRef = useRef<DetailSelection | null>(null);
  const mountedRef = useRef(true);
  const [issueDetails, setIssueDetails] = useState<IssueDetails | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isNotFound, setIsNotFound] = useState(false);

  // Only committed scopes can retire visible work: speculative/Suspense renders
  // must not replace the active owner or coordinator.
  useLayoutEffect(() => {
    if (scopeRef.current !== scope) {
      retireSelection(selectionRef.current);
      selectionRef.current = null;
      scopeRef.current = scope;
      setIssueDetails(null);
      setError(null);
      setIsLoading(false);
      setIsNotFound(false);
    }
    if (recoveryRef.current !== recovery) {
      const selection = selectionRef.current;
      const oldUnregister = selection?.unregister;
      if (selection) selection.unregister = null;
      recoveryRef.current = recovery;
      if (selection) enrollSelection(selection, recovery);
      oldUnregister?.();
    }
  }, [scope, recovery]);
  useEffect(() => {
    mountedRef.current = true;
    const selection = selectionRef.current;
    if (selection) enrollSelection(selection, recoveryRef.current);
    return () => {
      mountedRef.current = false;
      retireSelection(selectionRef.current);
    };
  }, []);

  const fetchIssue = useCallback(
    async (id: string): Promise<void> => {
      if (!id || !mountedRef.current || scopeRef.current !== scope) return;
      let selection = selectionRef.current;
      if (!selection || selection.id !== id || selection.scope !== scope) {
        const previous = selection;
        const current = () =>
          mountedRef.current &&
          scopeRef.current === scope &&
          selectionRef.current === owner;
        const request = new ScopedQueryRequest<IssueDetails>({
          load: async (signal) => {
            const details = await getIssue(workspaceId, id, { signal });
            if (!validDetails(details, id, workspaceId))
              throw new Error("Invalid issue detail response");
            return details;
          },
          commit: (details) => {
            if (!current())
              throw new DOMException("Detail scope superseded", "AbortError");
            setIssueDetails(details);
            setError(null);
            setIsNotFound(false);
          },
          onError: (failure) => {
            if (!current()) return;
            setError(failure.message);
            const missing =
              failure instanceof ApiError && failure.status === 404;
            setIsNotFound(missing);
            if (missing) setIssueDetails(null);
          },
          onLoading: (loading) => {
            if (current()) {
              setIsLoading(loading);
              if (loading) {
                setError(null);
                setIsNotFound(false);
              }
            }
          },
        });
        const owner: DetailSelection = {
          scope,
          id,
          revision: 1,
          unregister: null,
          request,
        };
        selectionRef.current = owner;
        selection = owner;
        // Register the new selection before removing the old participant, so a
        // pending barrier cannot complete in an enrollment gap.
        enrollSelection(owner, recoveryRef.current);
        retireSelection(previous);
        setIsNotFound(false);
      } else {
        selection.revision++;
      }
      setIsLoading(true);
      setError(null);
      setIsNotFound(false);
      await selection.request.run({ fresh: true }).catch(() => {});
    },
    [scope, workspaceId],
  );
  const clearIssue = useCallback(() => {
    if (scopeRef.current !== scope) return;
    retireSelection(selectionRef.current);
    selectionRef.current = null;
    setIssueDetails(null);
    setError(null);
    setIsLoading(false);
    setIsNotFound(false);
  }, [scope]);

  const updateIssueDetails = useCallback(
    (updated: Issue) => {
      if (scopeRef.current !== scope || selectionRef.current?.id !== updated.id)
        return;
      setIssueDetails((prev) => {
        if (!prev || prev.id !== updated.id || scopeRef.current !== scope)
          return prev;
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
    },
    [scope],
  );

  return {
    issueDetails,
    isLoading,
    error,
    isNotFound,
    fetchIssue,
    clearIssue,
    updateIssueDetails,
  };
}
