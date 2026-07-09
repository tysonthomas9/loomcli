/**
 * useWorkflowAgentDetail — run history + live status for a workflow-plane agent
 * (trigger binding). Mirrors the epic-runner pattern in AgentsPage: it fetches
 * the run list once (GET /trigger-bindings/{id}/runs, newest-first), then keeps
 * any non-terminal run live via the shared per-run SSE stream and refreshes the
 * whole page when a run reaches a terminal status (so finished_at / summary /
 * error_class settle in).
 *
 * `bindingId` is the trigger binding id. Runs are scoped by trigger_binding_id,
 * not driver id, because prompt agents share one driver.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import {
  isTerminalWorkflowRunStatus,
  listTriggerBindingRuns,
  type WorkflowRun,
} from "@/api";
import { useWorkflowRunStreams } from "@/hooks/workflows/useWorkflowRunStreams";

export interface WorkflowAgentRunStats {
  total: number;
  completed: number;
  failed: number;
  running: number;
}

export interface UseWorkflowAgentDetailReturn {
  runs: WorkflowRun[];
  loading: boolean;
  error: string | null;
  stats: WorkflowAgentRunStats;
  refresh: () => Promise<void>;
}

const DEFAULT_LIMIT = 25;

export function useWorkflowAgentDetail(
  workspaceId: string,
  bindingId: string,
  opts?: { limit?: number },
): UseWorkflowAgentDetailReturn {
  const limit = opts?.limit ?? DEFAULT_LIMIT;
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!workspaceId || !bindingId) return;
    setLoading(true);
    setError(null);
    try {
      const res = await listTriggerBindingRuns(workspaceId, bindingId, {
        limit,
      });
      setRuns(res.runs ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load runs");
    } finally {
      setLoading(false);
    }
  }, [workspaceId, bindingId, limit]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Non-terminal runs stream live; keyed by run_id for the streams hook.
  const activeRuns = useMemo<Record<string, WorkflowRun>>(() => {
    const map: Record<string, WorkflowRun> = {};
    for (const run of runs) {
      if (run.run_id && !isTerminalWorkflowRunStatus(run.status)) {
        map[run.run_id] = run;
      }
    }
    return map;
  }, [runs]);

  const handleRunUpdate = useCallback(
    (_key: string, run: WorkflowRun) => {
      setRuns((prev) => {
        const idx = prev.findIndex((r) => r.run_id === run.run_id);
        if (idx === -1) return [run, ...prev];
        const next = [...prev];
        next[idx] = run;
        return next;
      });
      // A terminal transition carries fields the stream patch cannot (final
      // summary, finished_at, error_class) — re-pull the canonical page once.
      if (isTerminalWorkflowRunStatus(run.status)) void refresh();
    },
    [refresh],
  );

  useWorkflowRunStreams({
    workspaceId,
    runs: activeRuns,
    onRunUpdate: handleRunUpdate,
  });

  const stats = useMemo<WorkflowAgentRunStats>(() => {
    let completed = 0;
    let failed = 0;
    let running = 0;
    for (const run of runs) {
      if (run.status === "completed") completed += 1;
      else if (run.status === "failed") failed += 1;
      else if (!isTerminalWorkflowRunStatus(run.status)) running += 1;
    }
    return { total: runs.length, completed, failed, running };
  }, [runs]);

  return {
    runs,
    loading,
    error,
    stats,
    refresh,
  };
}
