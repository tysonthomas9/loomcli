import { useCallback, useEffect, useState } from "react";

import { getSessionEval, rejudgeSession } from "@/api/evals";
import type { EvalRejudgeResult, SessionEvalState } from "@/types";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseSessionEvalResult {
  evalState: SessionEvalState | null;
  isLoading: boolean;
  isRejudging: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  requestRejudge: () => Promise<EvalRejudgeResult | null>;
}

export function useSessionEval(
  sessionId: string | null,
  enabled = true,
): UseSessionEvalResult {
  const { workspaceId } = useWorkspaceContext();
  const [evalState, setEvalState] = useState<SessionEvalState | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isRejudging, setIsRejudging] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const fetchState = useCallback(async () => {
    if (!sessionId || !enabled) return;
    setIsLoading(true);
    try {
      const state = await getSessionEval(workspaceId, sessionId);
      setEvalState(state);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setIsLoading(false);
    }
  }, [enabled, sessionId, workspaceId]);

  useEffect(() => {
    if (!sessionId || !enabled) {
      setEvalState(null);
      setError(null);
      setIsLoading(false);
      return;
    }
    let cancelled = false;
    setIsLoading(true);
    void getSessionEval(workspaceId, sessionId).then(
      (state) => {
        if (cancelled) return;
        setEvalState(state);
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
  }, [enabled, sessionId, workspaceId]);

  // Rejudge rejections (e.g. verb-time candidate validation) propagate to the
  // caller for a toast; they must not land in `error`, which the panel renders
  // as a load failure.
  const requestRejudge = useCallback(async () => {
    if (!sessionId || isRejudging) return null;
    setIsRejudging(true);
    try {
      const result = await rejudgeSession(workspaceId, sessionId);
      await fetchState();
      return result;
    } finally {
      setIsRejudging(false);
    }
  }, [fetchState, isRejudging, sessionId, workspaceId]);

  return {
    evalState,
    isLoading,
    isRejudging,
    error,
    refetch: fetchState,
    requestRejudge,
  };
}
