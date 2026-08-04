/**
 * Workspace pull request list from GitHub via gh CLI.
 */

import { get, wsUrl } from "@/api/common";

export interface GitPullRequest {
  number: number;
  title: string;
  url: string;
  state: string;
  is_draft: boolean;
  head_ref_name: string;
  base_ref_name: string;
  author_login?: string;
  created_at?: string;
  updated_at?: string;
  review_decision?: string;
  repo_name: string;
  source_repo?: string;
  additions?: number;
  deletions?: number;
  changed_files?: number;
}

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
