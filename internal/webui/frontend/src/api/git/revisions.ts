import { api, ApiError, apiErrorFromResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";
import { post, wsUrl } from "@/api/common";

export type ReviewRevision = components["schemas"]["ReviewRevision"];

export async function applyRevision(
  workspaceId: string,
  revision: ReviewRevision,
  lead?: string,
): Promise<void> {
  await post(wsUrl(workspaceId, "/git/apply"), {
    change: revision.change_id,
    revision: revision.number,
    lead,
  });
}

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
): Promise<void> {
  const { error, response } = await api.POST(
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
}
