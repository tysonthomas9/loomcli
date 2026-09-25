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

/**
 * Readiness view for one PR. `snapshot` is the last-known evidence (pinned to
 * head/base SHA and `observed_at`) and becomes history once not fresh;
 * `current_verdict` is the only verdict a surface may present as current and
 * is never "ready" unless `freshness` is "fresh".
 */
export type PullRequestReadinessView =
  components["schemas"]["PullRequestReadinessView"];
export type PullRequestReadinessVerdict =
  components["schemas"]["PullRequestReadinessVerdict"];
export type PullRequestReadinessRepoError =
  components["schemas"]["PullRequestReadinessRepoError"];
export type PullRequestReadinessList =
  components["schemas"]["PullRequestReadinessList"];
export type PullRequestReadinessPreviewResponse =
  components["schemas"]["PullRequestReadinessPreviewResponse"];

interface ReadinessEnvelope<T> {
  success: boolean;
  data: T;
  error?: string;
}

function readinessQuery(prKeys: readonly string[]): URLSearchParams {
  const params = new URLSearchParams();
  for (const key of prKeys) params.append("pr", key);
  return params;
}

/**
 * GET /api/workspaces/{ws}/pull-requests/readiness?pr=…&force=
 *
 * Read-only. Failing repositories come back in `repo_errors`; their rows keep
 * the last-known snapshot. Ages must be computed from `server_now`.
 */
export async function fetchPullRequestReadiness(
  workspaceId: string,
  prKeys: readonly string[],
  options: { force?: boolean } = {},
): Promise<PullRequestReadinessList> {
  const params = readinessQuery(prKeys);
  if (options.force) params.set("force", "true");
  const url = `${wsUrl(workspaceId, "/pull-requests/readiness")}?${params.toString()}`;
  const result = await get<ReadinessEnvelope<PullRequestReadinessList>>(url);
  return result.data;
}

/**
 * GET /api/workspaces/{ws}/pull-requests/readiness/preview?pr=…
 *
 * Read-only ordered ready-prefix preview; `prKeys[0]` lands first. Always
 * re-reads GitHub. `preview.ready_count` is the only source for ready counts.
 */
export async function fetchPullRequestReadinessPreview(
  workspaceId: string,
  prKeys: readonly string[],
): Promise<PullRequestReadinessPreviewResponse> {
  const url = `${wsUrl(workspaceId, "/pull-requests/readiness/preview")}?${readinessQuery(prKeys).toString()}`;
  const result =
    await get<ReadinessEnvelope<PullRequestReadinessPreviewResponse>>(url);
  return result.data;
}
