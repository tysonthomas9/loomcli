import { api, ApiError, apiErrorFromResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type ReviewRevision = components["schemas"]["ReviewRevision"];

export async function getTaskRevisions(
  workspaceId: string,
  taskId: string,
  lead?: string,
): Promise<ReviewRevision[]> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/issues/{id}/revisions",
      {
        params: {
          path: { ws: workspaceId, id: taskId },
          ...(lead ? { query: { lead } } : {}),
        },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return data?.data ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return [];
    throw err;
  }
}

/**
 * Approves the task's newest revision in every repo with one request: the
 * server opens the PRs only when every repo applied, otherwise none (P2.23).
 */
export async function approveTask(
  workspaceId: string,
  taskId: string,
  revisions: ReviewRevision[],
  verdict: "approve" | "override",
  reason: string,
  lead?: string,
): Promise<string | undefined> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/issues/{id}/approval",
    {
      params: { path: { ws: workspaceId, id: taskId } },
      body: {
        verdict,
        reason,
        ...(lead ? { lead } : {}),
        actor: { kind: "human", id: "local-user" },
        revisions: revisions.map((r) => ({
          change_id: r.change_id,
          number: r.number,
          head_sha: r.head_sha,
        })),
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data as { status?: string } | undefined)?.status;
}

export async function submitRevisionVerdict(
  workspaceId: string,
  revision: ReviewRevision,
  verdict: "approve" | "reject" | "override",
  reason: string,
  lead?: string,
  merge?: boolean,
): Promise<string | undefined> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict",
    {
      params: {
        path: {
          ws: workspaceId,
          change: revision.change_id,
          r: revision.number,
        },
      },
      body: {
        head_sha: revision.head_sha,
        verdict,
        reason,
        ...(lead ? { lead } : {}),
        ...(merge ? { merge: true } : {}),
        actor: { kind: "human", id: "local-user" },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data as { status?: string } | undefined)?.status;
}

/**
 * Rebuild a task whose code was built on a revision of the task it depends on
 * that was rejected or replaced: its newest revision is rejected and the task
 * reopens, built on that task's newest revision. Never automatic.
 */
export async function rebuildTask(
  workspaceId: string,
  taskId: string,
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/issues/{id}/rebuild",
    {
      params: { path: { ws: workspaceId, id: taskId } },
      body: { actor: { kind: "human", id: "local-user" } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

export async function applyRevision(
  workspaceId: string,
  revision: ReviewRevision,
  lead: string,
): Promise<void> {
  const { error, response } = await api.POST("/api/workspaces/{ws}/git/apply", {
    params: { path: { ws: workspaceId } },
    body: {
      change: revision.change_id,
      revision: revision.number,
      lead,
    },
  });
  if (error) throw apiErrorFromResponse(error, response);
}

export type RevisionDiff = components["schemas"]["RevisionDiff"];
export type TaskDiff = components["schemas"]["TaskDiff"];

/** A task's diff per repo, ordered by repo name: what its PR contains. */
export async function getTaskDiff(
  workspaceId: string,
  taskId: string,
  lead?: string,
): Promise<TaskDiff[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{id}/diff",
    {
      params: {
        path: { ws: workspaceId, id: taskId },
        ...(lead ? { query: { lead } } : {}),
      },
    },
  );
  if (error || !data) throw apiErrorFromResponse(error, response);
  return data.data;
}

/**
 * Approve and merge a task's open PR at the head the user sees (D29). The
 * bottom PR merges now; a higher one waits for the PRs below it.
 */
export async function approveRevisionMerge(
  workspaceId: string,
  revision: ReviewRevision,
  lead: string,
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/changes/{change}/merge-approval",
    {
      params: { path: { ws: workspaceId, change: revision.change_id } },
      body: {
        lead,
        head_sha: revision.pr_head ?? revision.head_sha,
        actor: { kind: "human", id: "local-user" },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/** Cancel auto-merge: drop a pending Approve and merge. */
export async function cancelRevisionMerge(
  workspaceId: string,
  changeId: string,
): Promise<void> {
  const { error, response } = await api.DELETE(
    "/api/workspaces/{ws}/changes/{change}/merge-approval",
    {
      params: { path: { ws: workspaceId, change: changeId } },
      body: { actor: { kind: "human", id: "local-user" } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}
