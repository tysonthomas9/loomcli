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

export async function submitRevisionVerdict(
  workspaceId: string,
  revision: ReviewRevision,
  verdict: "approve" | "reject" | "override",
  reason: string,
  lead?: string,
  approveOnly?: boolean,
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
        ...(approveOnly ? { approve_only: true } : {}),
        actor: { kind: "human", id: "local-user" },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data as { status?: string } | undefined)?.status;
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

/** Create PR for a change already applied in the lead's working area (D29). */
export async function createRevisionPR(
  workspaceId: string,
  lead: string,
  changeId: string,
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/git/pr",
    {
      params: { path: { ws: workspaceId, name: lead } },
      body: { change_id: changeId },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}
