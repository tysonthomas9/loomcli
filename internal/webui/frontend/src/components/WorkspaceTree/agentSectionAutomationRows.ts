import type { AgentServiceDTO } from "@/api/agentServices";
import type { LoomAgentStatus } from "@/types";

export interface DurableRecordRow {
  id: string;
  record: AgentServiceDTO;
  bindings: AgentServiceDTO["bindings"];
}

/**
 * The agent-service endpoint already attaches bindings to each durable record.
 * Keep that server-owned relationship intact instead of rebuilding it from a
 * second client-side collection.
 */
export function buildAgentAutomationRows(agentServices: AgentServiceDTO[]): {
  durableRecords: DurableRecordRow[];
} {
  return {
    durableRecords: agentServices.map((record) => ({
      id: record.id,
      record,
      bindings: record.bindings,
    })),
  };
}

/** Remove legacy live-roster projections owned by durable service records. */
export function withoutDurableAgentProjections(
  agents: LoomAgentStatus[],
  records: AgentServiceDTO[],
): LoomAgentStatus[] {
  if (records.length === 0) return agents;
  const durableIdentities = new Set(
    records
      .flatMap((record) => [record.id.trim(), record.name.trim()])
      .filter(Boolean),
  );
  return agents.filter((agent) => !durableIdentities.has(agent.name.trim()));
}
