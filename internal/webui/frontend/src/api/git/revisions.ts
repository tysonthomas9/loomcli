import { api, ApiError, apiErrorFromResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type ReviewRevision = components["schemas"]["ReviewRevision"];

export async function getTaskRevisions(
  workspaceId: string,
  taskId: string,
): Promise<ReviewRevision[]> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/issues/{id}/revisions",
      {
        params: { path: { ws: workspaceId, id: taskId } },
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
): Promise<void> {
  const { error, response } = await api.POST("/api/workspaces/{ws}/git/apply", {
    params: { path: { ws: workspaceId } },
    body: {
      change: revision.change_id,
      revision: revision.number,
      lead: "lead",
    },
  });
  if (error) throw apiErrorFromResponse(error, response);
}
