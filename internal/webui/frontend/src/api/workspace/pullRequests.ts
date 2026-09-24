/**
 * Workspace pull request list from GitHub (connector, or gh CLI fallback).
 */

import { get, wsUrl } from "@/api/common";
import type { components } from "@/types/generated/openapi";

/**
 * One listed PR. `pr_key` is the canonical identity
 * ("github:owner/repo#number", base repo); `node_id` is GitHub's global node
 * ID for reconciling a renamed or transferred repository.
 */
export type GitPullRequest = components["schemas"]["GitPullRequest"];

export type PullRequestListState = "all" | "open" | "merged" | "review";

export interface PullRequestList {
  pullRequests: GitPullRequest[];
  /** Per-repo listing failures (non-GitHub remote, missing gh, auth, …). */
  warnings: string[];
}

interface PullRequestsResponse {
  success: boolean;
  data: {
    pull_requests: GitPullRequest[];
    warnings?: string[];
  };
  error?: string;
}

/** GET /api/workspaces/{ws}/pull-requests?state= */
export async function fetchPullRequests(
  workspaceId: string,
  state: PullRequestListState = "all",
): Promise<PullRequestList> {
  const url = `${wsUrl(workspaceId, "/pull-requests")}?state=${encodeURIComponent(state)}`;
  const result = await get<PullRequestsResponse>(url);
  return {
    pullRequests: result.data?.pull_requests ?? [],
    warnings: result.data?.warnings ?? [],
  };
}
