import { api, apiErrorFromResponse, unwrapResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type PullRequestDetail = components["schemas"]["PullRequestDetail"];
export type PullRequestDiff = components["schemas"]["PullRequestDiff"];

export async function getPullRequestDetail(
  ws: string,
  owner: string,
  repo: string,
  number: number,
): Promise<PullRequestDetail> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}",
    {
      params: { path: { ws, owner, repo, number } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function getPullRequestDiff(
  ws: string,
  owner: string,
  repo: string,
  number: number,
): Promise<PullRequestDiff> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff",
    {
      params: { path: { ws, owner, repo, number } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}
