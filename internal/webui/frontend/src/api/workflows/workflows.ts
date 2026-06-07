import { post, wsUrl } from "@/api/common";

export const EPIC_RUNNER_WORKFLOW_NAME = "epic-runner";

export type WorkflowRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "needs_human"
  | "cancelled";

export interface WorkflowRun {
  workspace_key: string;
  run_id: string;
  driver_id: string;
  driver_version_id: string;
  entrypoint?: string;
  source_kind?: string;
  source_ref?: string;
  epic_id?: string;
  status: WorkflowRunStatus;
  node_id?: string;
  lease_id?: string;
  fencing_token?: number;
  idempotency_key?: string;
  payload?: unknown;
  output?: Record<string, string>;
  summary?: string;
  error_class?: string;
  started_at?: string;
  last_heartbeat?: string;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

export async function startWorkflowRun(
  workspaceId: string,
  workflowName: string,
  payload: unknown,
): Promise<WorkflowRun> {
  return post<WorkflowRun>(
    wsUrl(workspaceId, `/workflows/${encodeURIComponent(workflowName)}`),
    payload,
  );
}
