import { ApiError, get, wsUrl } from "@/api/common";
import type { TranscriptEntry, TranscriptResponse } from "@/types/agent";

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
  | "completed"
  | "failed"
  | "needs_review"
  | "cancelled"
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

export interface RunEventDTO {
  id: string;
  timestamp: string;
  actor: string;
  action: string;
  entity_type: string;
  entity_id: string;
  workspace_id: string;
  before?: string;
  after?: string;
  metadata?: Record<string, unknown>;
}

export interface RunEventsPage {
  events: RunEventDTO[];
  cursor?: string;
}

export type TaskRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface TaskRunDTO {
  taskRunId: string;
  taskId: string;
  status: TaskRunStatus;
  runner: string;
  errorClass?: string;
  startedAt?: string;
  finishedAt?: string | null;
  logsAvailable: boolean;
  transcriptAvailable: boolean;
}

export interface PersistedLogDTO {
  content: string;
  modifiedAt: string;
  truncated: boolean;
}

export interface AgentServiceJournalDTO {
  serviceId: string;
  filename: string;
  content: string;
  modifiedAt: string;
  truncated: boolean;
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

interface AgentServiceItemSuccess<T> {
  success: true;
  data: T;
}

type AgentServiceItemEnvelope<T> =
  | AgentServiceItemSuccess<T>
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

export async function listRunEvents(
  workspaceId: string,
  runId: string,
): Promise<RunEventsPage> {
  return get<RunEventsPage>(
    wsUrl(workspaceId, `/runs/${encodeURIComponent(runId)}/events`),
  );
}

export async function listAgentServiceRunTasks(
  workspaceId: string,
  agentServiceId: string,
  runId: string,
): Promise<AgentServiceList<TaskRunDTO>> {
  const response = await get<AgentServiceListEnvelope<TaskRunDTO>>(
    wsUrl(
      workspaceId,
      "/agent-services/" +
        encodeURIComponent(agentServiceId) +
        "/runs/" +
        encodeURIComponent(runId) +
        "/tasks",
    ),
  );
  return unwrapList(response);
}

async function getPersistedLog(path: string): Promise<PersistedLogDTO> {
  const response = await get<AgentServiceItemEnvelope<PersistedLogDTO>>(path);
  if (!response.success) {
    throw new ApiError(0, response.error, response);
  }
  return response.data;
}

export function getTaskRunLog(
  workspaceId: string,
  taskRunId: string,
): Promise<PersistedLogDTO> {
  return getPersistedLog(
    wsUrl(workspaceId, "/task-runs/" + encodeURIComponent(taskRunId) + "/log"),
  );
}

export async function getTaskRunTranscript(
  workspaceId: string,
  taskRunId: string,
): Promise<TranscriptEntry[]> {
  const response = await get<TranscriptResponse>(
    wsUrl(
      workspaceId,
      "/task-runs/" + encodeURIComponent(taskRunId) + "/transcript",
    ),
  );
  if (!response.success) {
    throw new ApiError(0, "Failed to load task transcript", response);
  }
  return response.data.entries;
}

export function getDriverRunLog(
  workspaceId: string,
  runId: string,
): Promise<PersistedLogDTO> {
  return getPersistedLog(
    wsUrl(workspaceId, "/runs/" + encodeURIComponent(runId) + "/log"),
  );
}

export async function getAgentServiceJournal(
  workspaceId: string,
  agentServiceId: string,
): Promise<AgentServiceJournalDTO> {
  const response = await get<AgentServiceItemEnvelope<AgentServiceJournalDTO>>(
    wsUrl(
      workspaceId,
      `/agent-services/${encodeURIComponent(agentServiceId)}/journal`,
    ),
  );
  if (!response.success) {
    throw new ApiError(0, response.error, response);
  }
  return response.data;
}
