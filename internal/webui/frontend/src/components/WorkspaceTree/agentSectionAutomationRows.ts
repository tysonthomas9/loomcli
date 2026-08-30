import type { AgentServiceDTO } from "@/api/agentServices";
import type { LoomAgentStatus } from "@/types";
import { agentServiceDotState } from "@/utils/bindingDisplay";

function agentServiceCardStatus(service: AgentServiceDTO): string {
  switch (agentServiceDotState(service)) {
    case "idle":
      return "ready";
    case "running":
      return "working";
    case "warn":
    case "unknown":
    case "failing":
      return "error";
    case "off":
      return "ready";
  }
}

/** Present a durable scheduled-agent record through the shared agent UI. */
export function agentServiceCardAgent(
  service: AgentServiceDTO,
  workspaceName: string,
): LoomAgentStatus {
  const displayName = service.name.trim() || service.id;
  const roleName = service.behavior.roleName?.trim() || "background";
  const roleLabel =
    service.behavior.roleDisplayName?.trim() ||
    (roleName === "background" ? "Background" : roleName);
  return {
    name: service.id,
    display_name: displayName,
    role: roleName,
    role_label: roleLabel,
    role_kind: "worker",
    daemon_managed: true,
    branch: "",
    status: agentServiceCardStatus(service),
    ahead: 0,
    behind: 0,
    workspace: workspaceName,
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
