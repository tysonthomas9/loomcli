import { useCallback, useEffect, useState } from "react";

import { getEvalCronState, setEvalCronEnabled } from "@/api/evals";
import type { EvalCronState } from "@/types";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseEvalCronResult {
  cron: EvalCronState | null;
  isLoading: boolean;
  isSaving: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  setEnabled: (enabled: boolean) => Promise<EvalCronState | null>;
}

export function useEvalCron(): UseEvalCronResult {
  const { workspaceId } = useWorkspaceContext();
  const [cron, setCron] = useState<EvalCronState | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const fetchCron = useCallback(async () => {
    setIsLoading(true);
    try {
      const state = await getEvalCronState(workspaceId);
      setCron(state);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setIsLoading(false);
    }
  }, [workspaceId]);

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    void getEvalCronState(workspaceId).then(
      (state) => {
        if (cancelled) return;
        setCron(state);
        setError(null);
        setIsLoading(false);
      },
      (err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err : new Error(String(err)));
        setIsLoading(false);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [workspaceId]);

  const setEnabled = useCallback(
    async (enabled: boolean): Promise<EvalCronState | null> => {
      if (isSaving) return null;
      setIsSaving(true);
      try {
        const state = await setEvalCronEnabled(workspaceId, enabled);
        setCron(state);
        setError(null);
        return state;
      } catch (err) {
        setError(err instanceof Error ? err : new Error(String(err)));
        return null;
      } finally {
        setIsSaving(false);
      }
    },
    [isSaving, workspaceId],
  );

  return { cron, isLoading, isSaving, error, refetch: fetchCron, setEnabled };
}
