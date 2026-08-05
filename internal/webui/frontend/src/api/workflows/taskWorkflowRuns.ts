import type { components } from "@/types/generated/openapi";

import { api, ApiError, apiErrorFromResponse } from "@/api/common";

/** A trigger-admitted workflow run not represented by an agent session. */
export type TaskWorkflowRun = components["schemas"]["DriverRun"];

/**
 * Fetch workflow runs associated with exactly one task through the immutable
 * TriggerEvent subject_ref. Runs already represented by an AgentSession are
 * omitted by the server; a TaskRun without a session remains visible here.
 */
export async function getTaskWorkflowRuns(
  workspaceId: string,
  taskId: string,
): Promise<TaskWorkflowRun[]> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{taskId}/workflow-runs",
      { params: { path: { ws: workspaceId, taskId } } },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return data?.runs ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return [];
    throw err;
  }
}
