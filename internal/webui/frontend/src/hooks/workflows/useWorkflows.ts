/**
 * useWorkflows — loads the workspace's registered workflows (drivers) for the
 * Workflows management view. Fetch-on-mount + manual refetch (no polling; the
 * list changes only on explicit authoring/version actions).
 */

import { useCallback, useEffect, useRef, useState } from "react";

import { listWorkflows } from "@/api";
import type { WorkflowSummary } from "@/api";

export interface UseWorkflowsResult {
  workflows: WorkflowSummary[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useWorkflows(workspaceId: string): UseWorkflowsResult {
  const [workflows, setWorkflows] = useState<WorkflowSummary[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (!workspaceId || fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const result = await listWorkflows(workspaceId);
      if (mountedRef.current) {
        setWorkflows(result);
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
  }, [workspaceId]);

  useEffect(() => {
    mountedRef.current = true;
    void fetchData();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchData]);

  return { workflows, isLoading, error, refetch: fetchData };
}
