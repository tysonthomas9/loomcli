import { ApiError, get, post, wsUrl } from "@/api/common";

export type WorkflowRunStatus =
  | "queued"
  | "running"
  | "waiting"
  | "completed"
  | "failed"
  | "cancelled";

export interface WorkflowRun {
  workspace_key: string;
  run_id: string;
  workflow_name: string;
  workflow_version: string;
  bundle_hash?: string;
  idempotency_key?: string;
  input?: unknown;
  status: WorkflowRunStatus;
  result?: unknown;
  error_class?: string;
  error_message?: string;
  wait_condition?: string;
  lease_owner?: string;
  lease_token?: string;
  fencing_token?: number;
  started_at?: string;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface WorkflowDefinition {
  workspace_key: string;
  name: string;
  version: string;
  description?: string;
  input_schema?: unknown;
  result_schema?: unknown;
  singleton_policy?: string;
  runtime_profile_name?: string;
  source_ref?: string;
  bundle_hash?: string;
  manifest?: unknown;
  capability_manifest?: unknown;
  status: "draft" | "active" | "deprecated" | "disabled";
  created_at: string;
  updated_at: string;
}

export interface TaskRun {
  workspace_key: string;
  task_run_id: string;
  idempotency_key: string;
  workflow_run_id: string;
  work_item_id: string;
  role_name: string;
  status: string;
  attempt: number;
  agent_id?: string;
  command_id?: string;
  session_id?: string;
  reason?: string;
  metadata?: Record<string, string>;
  created_at: string;
  updated_at: string;
}

export interface WorkflowRunEvent {
  workspace_key: string;
  event_id: string;
  workflow_run_id: string;
  task_run_id?: string;
  event_index: number;
  type: string;
  message?: string;
  data?: unknown;
  created_at: string;
}

export interface WorkflowRunStreamCompletionRun {
  run_id: string;
  workflow_name: string;
  status: WorkflowRunStatus;
  finished_at?: string;
}

export interface WorkflowRunStreamCompletion {
  run_ids: string[];
  runs: WorkflowRunStreamCompletionRun[];
}

export interface WorkflowRunStreamError {
  run_ids?: string[];
  error?: string;
  message?: string;
  terminal?: boolean;
}

export interface WorkflowRunListItem {
  run: WorkflowRun;
  task_runs?: TaskRun[];
}

export interface WorkflowRunResponse {
  run: WorkflowRun;
  builtin?: {
    run: WorkflowRun;
    task_runs?: TaskRun[];
    done: boolean;
    ready_count: number;
    open_count: number;
    blocked_count: number;
    dispatched_count?: number;
  };
}

export interface ListWorkflowRunsParams {
  workItemId?: string;
  workflowName?: string;
  status?: WorkflowRunStatus;
  limit?: number;
}

export interface StartWorkflowRunOptions {
  input?: unknown;
  once?: boolean;
  wait?: boolean;
}

interface ListEnvelope<T> {
  success?: boolean;
  data?: T[];
  total?: number;
  error?: string;
}

export async function listWorkflowDefinitions(
  workspaceId: string,
): Promise<WorkflowDefinition[]> {
  const response = await get<ListEnvelope<WorkflowDefinition>>(
    wsUrl(workspaceId, "/workflows"),
  );
  return unwrapList(response);
}

export async function listWorkflowRuns(
  workspaceId: string,
  params: ListWorkflowRunsParams = {},
): Promise<WorkflowRunListItem[]> {
  const response = await get<ListEnvelope<WorkflowRunListItem>>(
    `${wsUrl(workspaceId, "/workflow-runs")}${workflowRunQuery(params)}`,
  );
  return unwrapList(response);
}

export function startWorkflowRun(
  workspaceId: string,
  workflowName: string,
  options: StartWorkflowRunOptions = {},
): Promise<WorkflowRunResponse> {
  return post<WorkflowRunResponse>(
    wsUrl(workspaceId, `/workflows/${encodeURIComponent(workflowName)}/runs`),
    {
      input: options.input ?? {},
      once: options.once ?? true,
      wait: options.wait ?? false,
    },
  );
}

export async function getWorkflowRunEvents(
  workspaceId: string,
  runId: string,
): Promise<WorkflowRunEvent[]> {
  const response = await get<ListEnvelope<WorkflowRunEvent>>(
    wsUrl(workspaceId, `/workflow-runs/${encodeURIComponent(runId)}/events`),
  );
  return unwrapList(response);
}

export function workflowRunEventStreamUrl(
  workspaceId: string,
  runId: string,
  options: { untilTerminal?: boolean; since?: string } = {},
): string {
  const path = wsUrl(
    workspaceId,
    `/workflow-runs/${encodeURIComponent(runId)}/events/stream`,
  );
  const query = new URLSearchParams();
  if (options.untilTerminal) query.set("until", "terminal");
  if (options.since) query.set("since", options.since);
  const encoded = query.toString();
  return encoded ? `${path}?${encoded}` : path;
}

export function cancelWorkflowRun(
  workspaceId: string,
  runId: string,
): Promise<WorkflowRun> {
  return post<WorkflowRun>(
    wsUrl(workspaceId, `/workflow-runs/${encodeURIComponent(runId)}/cancel`),
    {},
  );
}

export function isWorkflowRunLive(status: WorkflowRunStatus): boolean {
  return status === "queued" || status === "running" || status === "waiting";
}

function workflowRunQuery(params: ListWorkflowRunsParams): string {
  const query = new URLSearchParams();
  if (params.workItemId) query.set("work_item_id", params.workItemId);
  if (params.workflowName) query.set("workflow_name", params.workflowName);
  if (params.status) query.set("status", params.status);
  if (params.limit != null) query.set("limit", String(params.limit));
  const encoded = query.toString();
  return encoded ? `?${encoded}` : "";
}

function unwrapList<T>(response: ListEnvelope<T>): T[] {
  if (response.success === false) {
    throw new ApiError(0, response.error || "Workflow API error", response);
  }
  return response.data ?? [];
}
