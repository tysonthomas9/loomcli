import { ApiError, get, wsUrl } from "@/api/common";

export type AgentServiceKind = "scripted" | "prompt" | "unknown";

export interface AgentServiceBehaviorDTO {
  roleName?: string;
  driverId?: string;
  driverVersionId?: string;
}

export interface AgentServiceBindingDTO {
  id: string;
  sourceKind: string;
  schedule: string;
  enabled: boolean;
  routeKey: string;
}

export interface AgentServiceDTO {
  id: string;
  name: string;
  kind: AgentServiceKind;
  enabled: boolean;
  behavior: AgentServiceBehaviorDTO;
  bindings: AgentServiceBindingDTO[];
  nextFireAt: string | null;
  lastRunStatus: string;
  consecutiveFailures: number;
  errors: string[];
  createdAt: string;
  updatedAt: string;
}

export type DriverRunStatus =
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "canceled"
  | "suspended_awaiting_event";

export interface DriverRunDTO {
  workspaceKey: string;
  runId: string;
  driverId: string;
  driverVersionId: string;
  entrypoint?: string;
  sourceKind?: string;
  sourceRef?: string;
  epicId?: string;
  triggerBindingId?: string;
  agentServiceId?: string;
  status: DriverRunStatus;
  nodeId?: string;
  leaseId?: string;
  fencingToken?: number;
  idempotencyKey?: string;
  payload?: unknown;
  output?: Record<string, string>;
  summary?: string;
  errorClass?: string;
  startedAt?: string;
  lastHeartbeat?: string;
  finishedAt?: string | null;
  parentRunId?: string;
  suspendedAt?: string | null;
  cancelRequestedAt?: string | null;
  cancelRequestedReason?: string;
  resumeSourceEventId?: string;
  createdAt: string;
  updatedAt: string;
}

export interface AgentServiceList<T> {
  data: T[];
  total: number;
}

interface AgentServiceListSuccess<T> extends AgentServiceList<T> {
  success: true;
}

interface AgentServiceListFailure {
  success: false;
  error: string;
}

type AgentServiceListEnvelope<T> =
  | AgentServiceListSuccess<T>
  | AgentServiceListFailure;

function unwrapList<T>(
  response: AgentServiceListEnvelope<T>,
): AgentServiceList<T> {
  if (!response.success) {
    throw new ApiError(0, response.error, response);
  }
  return { data: response.data, total: response.total };
}

export async function listAgentServices(
  workspaceId: string,
): Promise<AgentServiceList<AgentServiceDTO>> {
  const response = await get<AgentServiceListEnvelope<AgentServiceDTO>>(
    wsUrl(workspaceId, "/agent-services"),
  );
  return unwrapList(response);
}

export async function listAgentServiceRuns(
  workspaceId: string,
  agentServiceId: string,
  limit?: number,
): Promise<AgentServiceList<DriverRunDTO>> {
  const path = wsUrl(
    workspaceId,
    `/agent-services/${encodeURIComponent(agentServiceId)}/runs`,
  );
  const response = await get<AgentServiceListEnvelope<DriverRunDTO>>(
    limit === undefined
      ? path
      : `${path}?limit=${encodeURIComponent(String(limit))}`,
  );
  return unwrapList(response);
}
