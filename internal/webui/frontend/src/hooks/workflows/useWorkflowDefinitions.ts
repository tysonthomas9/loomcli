import { useCallback, useEffect, useRef, useState } from "react";

import {
  listWorkflowDefinitions,
  type WorkflowDefinition,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkflowDefinitionsResult {
  definitions: WorkflowDefinition[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

export function useWorkflowDefinitions(): UseWorkflowDefinitionsResult {
  const { workspaceId } = useWorkspaceContext();
  const [definitions, setDefinitions] = useState<WorkflowDefinition[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const result = await listWorkflowDefinitions(workspaceId);
      if (mountedRef.current) {
        setDefinitions(result);
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

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  useEffect(() => {
    mountedRef.current = true;
    void fetchData();
    return () => {
      mountedRef.current = false;
    };
  }, [workspaceId, fetchData]);

  return { definitions, isLoading, error, refetch };
}
