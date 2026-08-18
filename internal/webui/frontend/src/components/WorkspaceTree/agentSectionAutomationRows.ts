import type { AgentServiceDTO } from "@/api/agentServices";
import type { LoomAgentStatus } from "@/types";

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
