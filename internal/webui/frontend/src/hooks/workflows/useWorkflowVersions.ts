/**
 * useWorkflowVersions — loads one workflow's versions (+ built-in update state)
 * and exposes the lifecycle actions (approve/unapprove, activate, rollback,
 * adopt-built-in, author). Every action refetches on success so the table and
 * update banner stay consistent.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  activateWorkflowVersion,
  approveWorkflowVersion,
  createWorkflowVersion,
  listWorkflowVersions,
  rollbackWorkflow,
  syncBuiltinWorkflow,
  unapproveWorkflowVersion,
} from "@/api";
import type {
  BuiltinTrack,
  CreateWorkflowVersionInput,
  WorkflowVersionsResponse,
} from "@/api";

export interface UseWorkflowVersionsResult {
  data: WorkflowVersionsResponse | null;
  isLoading: boolean;
  error: Error | null;
  /** Set while a lifecycle action (activate/approve/…) is in flight. */
  actionPending: boolean;
  /** The last lifecycle-action error, cleared when the next action starts. */
  actionError: Error | null;
  refetch: () => Promise<void>;
  approve: (versionId: string) => Promise<void>;
  unapprove: (versionId: string) => Promise<void>;
  activate: (versionId: string, track?: BuiltinTrack) => Promise<void>;
  rollback: (versionId?: string) => Promise<void>;
  adoptBuiltin: () => Promise<void>;
  authorVersion: (input: CreateWorkflowVersionInput) => Promise<void>;
}

export function useWorkflowVersions(
  workspaceId: string,
  workflowName: string,
): UseWorkflowVersionsResult {
  const [data, setData] = useState<WorkflowVersionsResponse | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [actionPending, setActionPending] = useState(false);
  const [actionError, setActionError] = useState<Error | null>(null);

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (!workspaceId || !workflowName || fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const result = await listWorkflowVersions(workspaceId, workflowName);
      if (mountedRef.current) {
        setData(result);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) setIsLoading(false);
      fetchInProgressRef.current = false;
    }
  }, [workspaceId, workflowName]);

  // runAction centralizes the pending/error/refetch bookkeeping so a failed
  // action surfaces actionError and never leaves the table stale.
  const runAction = useCallback(
    async (fn: () => Promise<unknown>) => {
      setActionPending(true);
      setActionError(null);
      try {
        await fn();
        await fetchData();
      } catch (err) {
        if (mountedRef.current) {
          setActionError(err instanceof Error ? err : new Error(String(err)));
        }
        throw err;
      } finally {
        if (mountedRef.current) setActionPending(false);
      }
    },
    [fetchData],
  );

  const approve = useCallback(
    (versionId: string) =>
      runAction(() =>
        approveWorkflowVersion(workspaceId, workflowName, versionId),
      ),
    [runAction, workspaceId, workflowName],
  );
  const unapprove = useCallback(
    (versionId: string) =>
      runAction(() =>
        unapproveWorkflowVersion(workspaceId, workflowName, versionId),
      ),
    [runAction, workspaceId, workflowName],
  );
  const activate = useCallback(
    (versionId: string, track?: BuiltinTrack) =>
      runAction(() =>
        activateWorkflowVersion(workspaceId, workflowName, versionId, track),
      ),
    [runAction, workspaceId, workflowName],
  );
  const rollback = useCallback(
    (versionId?: string) =>
      runAction(() => rollbackWorkflow(workspaceId, workflowName, versionId)),
    [runAction, workspaceId, workflowName],
  );
  const adoptBuiltin = useCallback(
    () =>
      runAction(() => syncBuiltinWorkflow(workspaceId, workflowName, "auto")),
    [runAction, workspaceId, workflowName],
  );
  const authorVersion = useCallback(
    (input: CreateWorkflowVersionInput) =>
      runAction(() =>
        createWorkflowVersion(workspaceId, workflowName, input),
      ),
    [runAction, workspaceId, workflowName],
  );

  useEffect(() => {
    mountedRef.current = true;
    void fetchData();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchData]);

  return {
    data,
    isLoading,
    error,
    actionPending,
    actionError,
    refetch: fetchData,
    approve,
    unapprove,
    activate,
    rollback,
    adoptBuiltin,
    authorVersion,
  };
}
