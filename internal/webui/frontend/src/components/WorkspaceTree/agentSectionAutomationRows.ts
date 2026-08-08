import type { AgentRecordSummary, TriggerBinding } from "@/api";
import type { LoomAgentStatus } from "@/types";
import {
  bindingCadenceLabel,
  formatFireTime,
  type BindingDotState,
} from "@/utils/bindingDisplay";

export interface DurableRecordRow {
  id: string;
  record: AgentRecordSummary;
  bindings: TriggerBinding[];
}

export interface LegacyBindingRow {
  id: string;
  binding: TriggerBinding;
}

/**
 * The live-agent endpoint still projects prompt/scripted AgentServices into the
 * legacy roster so older monitor consumers can see their runtime status. The
 * durable AgentService collection is the canonical UI identity, though. When
 * both responses contain the same stable ID, keep only the durable record row;
 * otherwise the rail renders two names for one agent and the legacy projection
 * shadows the record's aggregate run-history route.
 */
export function withoutDurableAgentProjections(
  agents: LoomAgentStatus[],
  records: AgentRecordSummary[],
): LoomAgentStatus[] {
  if (records.length === 0) return agents;
  const durableIdentities = new Set(
    records
      .flatMap((record) => [record.id.trim(), record.name.trim()])
      .filter(Boolean),
  );
  return agents.filter((agent) => !durableIdentities.has(agent.name.trim()));
}

export function buildAgentAutomationRows(
  agentRecords: AgentRecordSummary[],
  bindings: TriggerBinding[],
): {
  durableRecords: DurableRecordRow[];
  legacyBindings: LegacyBindingRow[];
} {
  const attached = new Map<string, TriggerBinding[]>();
  for (const binding of bindings) {
    const agentId = binding.target_agent_service_id?.trim();
    if (!agentId) continue;
    const current = attached.get(agentId) ?? [];
    current.push(binding);
    attached.set(agentId, current);
  }
  return {
    durableRecords: agentRecords.map((record) => ({
      id: record.id,
      record,
      bindings: attached.get(record.id) ?? [],
    })),
    legacyBindings: bindings
      .filter((binding) => !binding.target_agent_service_id?.trim())
      .map((binding) => ({ id: binding.binding_id, binding })),
  };
}

/**
 * Binding-id detail routes are child views of one durable agent identity.
 * Keep that parent record selected in the rail even though the URL names the
 * exact trigger being inspected.
 */
export function selectedDurableRecordID(
  selectedAgentID: string | null | undefined,
  bindings: TriggerBinding[],
): string | null {
  if (!selectedAgentID) return null;
  const selectedBinding = bindings.find(
    (binding) => binding.binding_id === selectedAgentID,
  );
  return selectedBinding?.target_agent_service_id?.trim() || selectedAgentID;
}

export function durableRecordDotState({
  record,
  bindings,
}: DurableRecordRow): BindingDotState {
  if (!record.enabled) return "off";
  const failures = Math.max(
    record.consecutive_failures ?? 0,
    ...bindings.map((binding) => binding.consecutive_failures ?? 0),
  );
  if (failures >= 2) return "failing";
  if (failures === 1) return "warn";
  return "idle";
}

export function durableRecordTooltip(row: DurableRecordRow): string {
  if (!row.record.enabled) return "Disabled";
  const failures = Math.max(
    row.record.consecutive_failures ?? 0,
    ...row.bindings.map((binding) => binding.consecutive_failures ?? 0),
  );
  if (failures >= 2) return `Failing — ${failures} consecutive runs failed`;
  if (failures === 1) return "Last run failed";
  if (row.bindings.length === 0) return "Enabled · no triggers configured";
  const next = formatFireTime(row.record.next_fire_at);
  if (next) return `Enabled · next fire ${next}`;
  return `Enabled · ${row.bindings.length} ${
    row.bindings.length === 1 ? "trigger" : "triggers"
  }`;
}

export function durableRecordCadence(row: DurableRecordRow): string {
  if (row.bindings.length === 0) return "No triggers configured";
  if (row.bindings.length === 1) {
    return bindingCadenceLabel(row.bindings[0]!);
  }
  return `${row.bindings.length} triggers`;
}
