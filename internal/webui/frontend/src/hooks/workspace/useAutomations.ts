import { useCallback, useEffect, useState } from "react";

import {
  createTriggerBinding,
  listTriggerBindings,
  listWorkflows,
  setTriggerBindingEnabled,
  startWorkflowRun,
  type CreateTriggerBindingRequest,
  type TriggerBinding,
  type WorkflowRun,
  type WorkflowSummary,
} from "@/api/workflows";

export interface UseAutomationsReturn {
  workflows: WorkflowSummary[];
  bindings: TriggerBinding[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  createBinding: (req: CreateTriggerBindingRequest) => Promise<TriggerBinding>;
  setEnabled: (bindingId: string, enabled: boolean) => Promise<void>;
  runWorkflow: (name: string, payload: unknown) => Promise<WorkflowRun>;
}

/**
 * Data + actions for the Automations surface: the workflow catalog, trigger
 * bindings, and the generic workflow runner. `active` gates the initial fetch so
 * the modal only loads when opened.
 */
export function useAutomations(
  workspaceId: string,
  active: boolean,
): UseAutomationsReturn {
  const [workflows, setWorkflows] = useState<WorkflowSummary[]>([]);
  const [bindings, setBindings] = useState<TriggerBinding[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!workspaceId) return;
    setLoading(true);
    setError(null);
    try {
      const [wf, bs] = await Promise.all([
        listWorkflows(workspaceId),
        listTriggerBindings(workspaceId),
      ]);
      setWorkflows(wf);
      setBindings(bs);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load automations",
      );
    } finally {
      setLoading(false);
    }
  }, [workspaceId]);

  // Bindings change on create/toggle; the workflow catalog is static, so a
  // post-mutation refresh re-pulls bindings only (no spinner, best-effort).
  // Inactive consumers (e.g. CreateAgentModal, which only needs createBinding)
  // render no catalog, so refetching for them would be wasted I/O.
  const refreshBindings = useCallback(async () => {
    if (!workspaceId || !active) return;
    try {
      setBindings(await listTriggerBindings(workspaceId));
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load trigger bindings",
      );
    }
  }, [workspaceId, active]);

  useEffect(() => {
    if (active) void refresh();
  }, [active, refresh]);

  const createBinding = useCallback(
    async (req: CreateTriggerBindingRequest): Promise<TriggerBinding> => {
      const binding = await createTriggerBinding(workspaceId, req);
      await refreshBindings();
      return binding;
    },
    [workspaceId, refreshBindings],
  );

  const setEnabled = useCallback(
    async (bindingId: string, on: boolean): Promise<void> => {
      await setTriggerBindingEnabled(workspaceId, bindingId, on);
      await refreshBindings();
    },
    [workspaceId, refreshBindings],
  );

  const runWorkflow = useCallback(
    (name: string, payload: unknown): Promise<WorkflowRun> =>
      startWorkflowRun(workspaceId, name, payload),
    [workspaceId],
  );

  return {
    workflows,
    bindings,
    loading,
    error,
    refresh,
    createBinding,
    setEnabled,
    runWorkflow,
  };
}
