import { useCallback, useEffect, useRef, useState } from "react";

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
  type WorkflowRunStatus,
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

function workflowRunStatus(
  value: string | undefined,
): WorkflowRunStatus | undefined {
  switch (value) {
    case "queued":
    case "running":
    case "completed":
    case "failed":
    case "needs_review":
    case "cancelled":
    case "suspended_awaiting_event":
      return value;
    default:
      return undefined;
  }
}

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
  /** Durable prompt/scripted identities used for stable routing and history. */
  agentRecords: AgentRecordSummary[];
  bindings: TriggerBinding[];
  loading: boolean;
  /** True once the first fetch has settled (success or error) — lets callers
   * distinguish "not fetched yet" from "fetched, empty". */
  initialized: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  createBinding: (req: CreateTriggerBindingRequest) => Promise<TriggerBinding>;
  setRecordEnabled: (agentId: string, enabled: boolean) => Promise<void>;
  deleteRecord: (agentId: string) => Promise<void>;
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

interface AutomationsState {
  workspaceId: string;
  workflows: WorkflowSummary[];
  agentRecords: AgentRecordSummary[];
  bindings: TriggerBinding[];
  loading: boolean;
  initialized: boolean;
  error: string | null;
}

function emptyAutomationsState(
  workspaceId: string,
  loading = false,
): AutomationsState {
  return {
    workspaceId,
    workflows: [],
    agentRecords: [],
    bindings: [],
    loading,
    initialized: false,
    error: null,
  };
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
  const [state, setState] = useState<AutomationsState>(() =>
    emptyAutomationsState(workspaceId),
  );
  const activeWorkspaceRef = useRef(workspaceId);
  // Both full refreshes and binding-only refreshes read the record+binding
  // identity graph. One shared generation prevents an older snapshot from
  // overwriting a newer post-mutation read. Workflow catalog reads have their
  // own generation because a binding-only refresh deliberately does not fetch
  // that otherwise-static catalog.
  const dataRequestGenerationRef = useRef(0);
  const workflowRequestGenerationRef = useRef(0);

  // Effects run after render. Update the scope fence synchronously so a response
  // from workspace A cannot commit during the first render of workspace B.
  if (activeWorkspaceRef.current !== workspaceId) {
    activeWorkspaceRef.current = workspaceId;
    dataRequestGenerationRef.current += 1;
    workflowRequestGenerationRef.current += 1;
  }

  const refresh = useCallback(async () => {
    if (!workspaceId) return;
    const requestWorkspace = workspaceId;
    const dataGeneration = ++dataRequestGenerationRef.current;
    const workflowGeneration = ++workflowRequestGenerationRef.current;
    setState((current) => {
      const scoped =
        current.workspaceId === requestWorkspace
          ? current
          : emptyAutomationsState(requestWorkspace);
      return { ...scoped, loading: true, error: null };
    });
    try {
      const [wf, bs, records] = await Promise.all([
        listWorkflows(requestWorkspace),
        listTriggerBindings(requestWorkspace),
        listAgentRecords(requestWorkspace),
      ]);
      if (activeWorkspaceRef.current !== requestWorkspace) return;
      setState((current) => {
        const scoped =
          current.workspaceId === requestWorkspace
            ? current
            : emptyAutomationsState(requestWorkspace);
        const workflowCurrent =
          workflowGeneration === workflowRequestGenerationRef.current;
        const dataCurrent = dataGeneration === dataRequestGenerationRef.current;
        if (!workflowCurrent && !dataCurrent) return current;
        return {
          ...scoped,
          ...(workflowCurrent ? { workflows: wf } : {}),
          ...(dataCurrent
            ? {
                agentRecords: records,
                bindings: withAgentRecordNames(bs, records),
                loading: false,
                initialized: true,
                error: null,
              }
            : {}),
        };
      });
    } catch (err) {
      if (
        activeWorkspaceRef.current !== requestWorkspace ||
        dataGeneration !== dataRequestGenerationRef.current
      ) {
        return;
      }
      setState((current) => ({
        ...(current.workspaceId === requestWorkspace
          ? current
          : emptyAutomationsState(requestWorkspace)),
        loading: false,
        initialized: true,
        error:
          err instanceof Error ? err.message : "Failed to load automations",
      }));
    }
  }, [workspaceId]);

  // Bindings change on create/toggle; the workflow catalog is static, so a
  // post-mutation refresh re-pulls bindings only (no spinner, best-effort).
  // Inactive consumers (e.g. CreateAgentModal, which only needs createBinding)
  // render no catalog, so refetching for them would be wasted I/O.
  const refreshBindings = useCallback(async () => {
    if (!workspaceId || !active) return;
    const requestWorkspace = workspaceId;
    const dataGeneration = ++dataRequestGenerationRef.current;
    try {
      const [nextBindings, records] = await Promise.all([
        listTriggerBindings(requestWorkspace),
        listAgentRecords(requestWorkspace),
      ]);
      if (
        activeWorkspaceRef.current !== requestWorkspace ||
        dataGeneration !== dataRequestGenerationRef.current
      ) {
        return;
      }
      setState((current) => ({
        ...(current.workspaceId === requestWorkspace
          ? current
          : emptyAutomationsState(requestWorkspace)),
        agentRecords: records,
        bindings: withAgentRecordNames(nextBindings, records),
        loading: false,
        initialized: true,
        error: null,
      }));
    } catch (err) {
      if (
        activeWorkspaceRef.current !== requestWorkspace ||
        dataGeneration !== dataRequestGenerationRef.current
      ) {
        return;
      }
      setState((current) => ({
        ...(current.workspaceId === requestWorkspace
          ? current
          : emptyAutomationsState(requestWorkspace)),
        loading: false,
        initialized: true,
        error:
          err instanceof Error
            ? err.message
            : "Failed to load trigger bindings",
      }));
    }
  }, [workspaceId, active]);

  useEffect(() => {
    if (!workspaceId) {
      setState(emptyAutomationsState(""));
      return;
    }
    if (active) {
      void refresh();
      return;
    }
    setState((current) =>
      current.workspaceId === workspaceId
        ? current
        : emptyAutomationsState(workspaceId),
    );
  }, [active, refresh, workspaceId]);

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

  const setRecordEnabled = useCallback(
    async (agentId: string, on: boolean): Promise<void> => {
      await setAgentRecordEnabled(workspaceId, agentId, on);
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
    },
    [workspaceId, refreshBindings],
  );

  const deleteRecord = useCallback(
    async (agentId: string): Promise<void> => {
      await deleteAgentRecord(workspaceId, agentId);
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
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
      const binding = state.bindings.find((b) => b.binding_id === bindingId);
      const agentId = binding?.target_agent_service_id?.trim();
      if (agentId) {
        await setRecordEnabled(agentId, on);
      } else {
        await setTriggerBindingEnabled(workspaceId, bindingId, on);
        await refreshBindings();
        dispatchBindingsChanged(workspaceId);
      }
    },
    [workspaceId, state.bindings, refreshBindings, setRecordEnabled],
  );

  const updateBinding = useCallback(
    async (
      bindingId: string,
      req: UpdateTriggerBindingRequest,
    ): Promise<TriggerBinding> => {
      const current = state.bindings.find(
        (binding) => binding.binding_id === bindingId,
      );
      const agentId = current?.target_agent_service_id?.trim();
      let binding: TriggerBinding;

      if (current && agentId) {
        // Agent-owned PATCH is the only public mutation path for attached
        // managed bindings. Carry the exact binding identity for cron edits so
        // the server can enforce ownership without opening the generic binding
        // PATCH surface.
        const allowed = new Set(["name", "schedule", "schedule_timezone"]);
        const unsupported = Object.keys(req).filter((key) => !allowed.has(key));
        if (unsupported.length > 0) {
          throw new Error(
            `Managed agent update does not support: ${unsupported.join(", ")}`,
          );
        }
        const hasRecordUpdate = req.name !== undefined;
        const hasScheduleUpdate =
          req.schedule !== undefined || req.schedule_timezone !== undefined;
        if (hasRecordUpdate && hasScheduleUpdate) {
          throw new Error("Save the agent name and cadence separately.");
        }
        const record = await updateAgentRecord(workspaceId, agentId, {
          ...req,
          ...(hasScheduleUpdate ? { binding_id: bindingId } : {}),
        });
        const persisted = record.bindings?.find(
          (candidate) => candidate.binding_id === bindingId,
        );
        if (persisted) {
          const {
            last_run_status: persistedLastRunStatus,
            ...persistedFields
          } = persisted;
          const status = workflowRunStatus(persistedLastRunStatus);
          binding = {
            ...current,
            ...persistedFields,
            name: record.name,
            ...(status ? { last_run_status: status } : {}),
          };
        } else {
          binding = {
            ...current,
            ...(req.schedule !== undefined
              ? { schedule: req.schedule.trim() }
              : {}),
            ...(req.schedule_timezone !== undefined
              ? { schedule_timezone: req.schedule_timezone.trim() }
              : {}),
            name: record.name,
          };
        }
      } else {
        // Unattached bindings are legacy standalone records.
        binding = await updateTriggerBinding(workspaceId, bindingId, req);
      }
      await refreshBindings();
      dispatchBindingsChanged(workspaceId);
      return binding;
    },
    [workspaceId, state.bindings, refreshBindings],
  );

  const deleteBinding = useCallback(
    async (bindingId: string): Promise<void> => {
      const binding = state.bindings.find(
        (item) => item.binding_id === bindingId,
      );
      const agentId = binding?.target_agent_service_id?.trim();
      if (agentId) {
        // The agent-scoped delete archives the AgentService and cleans up all
        // attached bindings/grants. Deleting only this binding would orphan a
        // still-live durable identity.
        await deleteRecord(agentId);
      } else {
        await deleteTriggerBinding(workspaceId, bindingId);
        await refreshBindings();
        dispatchBindingsChanged(workspaceId);
      }
    },
    [workspaceId, state.bindings, refreshBindings, deleteRecord],
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

  const visibleState =
    state.workspaceId === workspaceId
      ? state
      : emptyAutomationsState(workspaceId, Boolean(active && workspaceId));

  return {
    workflows: visibleState.workflows,
    agentRecords: visibleState.agentRecords,
    bindings: visibleState.bindings,
    loading: visibleState.loading,
    initialized: visibleState.initialized,
    error: visibleState.error,
    refresh,
    createBinding,
    setRecordEnabled,
    deleteRecord,
    setEnabled,
    updateBinding,
    deleteBinding,
    runWorkflow,
    runBinding,
  };
}
