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

/**
 * Cross-instance sync: the sidebar (AgentSection), the agent detail page, and
 * the modals each own a useAutomations instance. A binding mutation in one
 * must not leave the others stale (e.g. disabling an agent from its detail
 * page must flip the sidebar dot), so mutations broadcast this window event
 * and every active instance re-pulls its bindings.
 */
const BINDINGS_CHANGED_EVENT = "loom:trigger-bindings-changed";

function dispatchBindingsChanged(workspaceId: string): void {
  window.dispatchEvent(
    new CustomEvent<{ workspaceId: string }>(BINDINGS_CHANGED_EVENT, {
      detail: { workspaceId },
    }),
  );
}

export interface UseAutomationsReturn {
  workflows: WorkflowSummary[];
  bindings: TriggerBinding[];
  loading: boolean;
  /** True once the first fetch has settled (success or error) — lets callers
   * distinguish "not fetched yet" from "fetched, empty". */
  initialized: boolean;
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
  const [initialized, setInitialized] = useState(false);
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
      setInitialized(true);
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

  // Re-pull bindings when another instance mutates them (sidebar ↔ detail ↔
  // modals). The mutating instance receives its own broadcast too, costing it
  // one redundant (cheap, idempotent) list fetch per mutation.
  useEffect(() => {
    if (!workspaceId || !active) return;
    const onChanged = (event: Event): void => {
      const detail = (event as CustomEvent<{ workspaceId?: string }>).detail;
      if (detail?.workspaceId && detail.workspaceId !== workspaceId) return;
      void refreshBindings();
    };
    window.addEventListener(BINDINGS_CHANGED_EVENT, onChanged);
    return () => window.removeEventListener(BINDINGS_CHANGED_EVENT, onChanged);
  }, [workspaceId, active, refreshBindings]);

  const createBinding = useCallback(
    async (req: CreateTriggerBindingRequest): Promise<TriggerBinding> => {
      const binding = await createTriggerBinding(workspaceId, req);
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
      return binding;
    },
    [workspaceId, refreshBindings],
  );

  const setEnabled = useCallback(
    async (bindingId: string, on: boolean): Promise<void> => {
      await setTriggerBindingEnabled(workspaceId, bindingId, on);
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
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
    initialized,
    error,
    refresh,
    createBinding,
    setEnabled,
    runWorkflow,
  };
}
