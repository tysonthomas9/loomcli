import { useCallback, useEffect, useState } from "react";

import {
  deleteAgentRecord,
  listAgentRecords,
  setAgentRecordEnabled,
  updateAgentRecord,
  type AgentRecordSummary,
} from "@/api/agents";
import {
  createTriggerBinding,
  deleteTriggerBinding,
  listTriggerBindings,
  listWorkflows,
  runTriggerBinding,
  setTriggerBindingEnabled,
  startWorkflowRun,
  updateTriggerBinding,
  type CreateTriggerBindingRequest,
  type TriggerBinding,
  type UpdateTriggerBindingRequest,
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

/**
 * Attached bindings are trigger/config children of a durable AgentService.
 * Render the record's name as the agent identity while retaining the binding's
 * own name for legacy unattached entries.
 */
function withAgentRecordNames(
  bindings: TriggerBinding[],
  records: AgentRecordSummary[],
): TriggerBinding[] {
  const namesById = new Map(
    records
      .filter((record) => record.name.trim() !== "")
      .map((record) => [record.id, record.name] as const),
  );
  return bindings.map((binding) => {
    const agentId = binding.target_agent_service_id?.trim();
    const recordName = agentId ? namesById.get(agentId) : undefined;
    return recordName ? { ...binding, name: recordName } : binding;
  });
}

// Exported so out-of-hook binding mutations (the transactional agent create in
// CreateAgentModal) can nudge every mounted useAutomations instance — without
// it, navigating to a just-created agent on an already-mounted AgentsPage hits
// a stale bindings list and renders an empty shell.
export function dispatchBindingsChanged(workspaceId: string): void {
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
  updateBinding: (
    bindingId: string,
    req: UpdateTriggerBindingRequest,
  ) => Promise<TriggerBinding>;
  deleteBinding: (bindingId: string) => Promise<void>;
  runWorkflow: (name: string, payload: unknown) => Promise<WorkflowRun>;
  /**
   * Run a binding on demand — config-by-reference (the run wears the binding's
   * role via provenance, no client-side run-input merge). Prefer this over
   * runWorkflow whenever a binding is in scope.
   */
  runBinding: (bindingId: string) => Promise<WorkflowRun>;
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
      const [wf, bs, records] = await Promise.all([
        listWorkflows(workspaceId),
        listTriggerBindings(workspaceId),
        listAgentRecords(workspaceId),
      ]);
      setWorkflows(wf);
      setBindings(withAgentRecordNames(bs, records));
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
      const [nextBindings, records] = await Promise.all([
        listTriggerBindings(workspaceId),
        listAgentRecords(workspaceId),
      ]);
      setBindings(withAgentRecordNames(nextBindings, records));
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
      // ATTACHED bindings (target_agent_service_id set — every prompt-agent
      // binding after the identity migration) are managed by their Agent
      // record: the binding-scoped enable route 409s them, so drive the
      // agent-scoped route instead, which sets desired_state and fans out to
      // every attached binding. Unattached (legacy) bindings keep the direct
      // route.
      const binding = bindings.find((b) => b.binding_id === bindingId);
      const agentId = binding?.target_agent_service_id?.trim();
      if (agentId) {
        await setAgentRecordEnabled(workspaceId, agentId, on);
      } else {
        await setTriggerBindingEnabled(workspaceId, bindingId, on);
      }
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
    },
    [workspaceId, bindings, refreshBindings],
  );

  const updateBinding = useCallback(
    async (
      bindingId: string,
      req: UpdateTriggerBindingRequest,
    ): Promise<TriggerBinding> => {
      const current = bindings.find(
        (binding) => binding.binding_id === bindingId,
      );
      const agentId = current?.target_agent_service_id?.trim();
      let binding: TriggerBinding;

      if (current && agentId && req.name !== undefined) {
        // The user-facing name belongs to the durable AgentService identity.
        // Keep schedule/timezone on the attached trigger binding, but never
        // rename only that binding and leave the agent record stale.
        const { name, ...bindingPatch } = req;
        binding = current;
        if (Object.keys(bindingPatch).length > 0) {
          binding = await updateTriggerBinding(
            workspaceId,
            bindingId,
            bindingPatch,
          );
        }
        const record = await updateAgentRecord(workspaceId, agentId, { name });
        binding = { ...binding, name: record.name };
      } else {
        // Unattached bindings are legacy standalone records. Schedule-only
        // edits also remain binding configuration for both representations.
        binding = await updateTriggerBinding(workspaceId, bindingId, req);
      }
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
      return binding;
    },
    [workspaceId, bindings, refreshBindings],
  );

  const deleteBinding = useCallback(
    async (bindingId: string): Promise<void> => {
      const binding = bindings.find((item) => item.binding_id === bindingId);
      const agentId = binding?.target_agent_service_id?.trim();
      if (agentId) {
        // The agent-scoped delete archives the AgentService and cleans up all
        // attached bindings/grants. Deleting only this binding would orphan a
        // still-live durable identity.
        await deleteAgentRecord(workspaceId, agentId);
      } else {
        await deleteTriggerBinding(workspaceId, bindingId);
      }
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
    },
    [workspaceId, bindings, refreshBindings],
  );

  const runWorkflow = useCallback(
    (name: string, payload: unknown): Promise<WorkflowRun> =>
      startWorkflowRun(workspaceId, name, payload),
    [workspaceId],
  );

  const runBinding = useCallback(
    (bindingId: string): Promise<WorkflowRun> =>
      runTriggerBinding(workspaceId, bindingId),
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
    updateBinding,
    deleteBinding,
    runWorkflow,
    runBinding,
  };
}
