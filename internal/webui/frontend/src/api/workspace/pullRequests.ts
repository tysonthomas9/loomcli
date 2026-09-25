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

type DeliveryGroupView = components["schemas"]["DeliveryGroupView"];
export type StandalonePRContinuation =
  components["schemas"]["StandalonePRContinuation"];
/** Verified GitHub login for the PR-list credential — never Auth display name. */
export type GitHubViewerIdentity =
  components["schemas"]["GitHubViewerIdentity"];

export interface PullRequestList {
  pullRequests: GitPullRequest[];
  /** Per-repo listing failures (non-GitHub remote, missing gh, auth, …). */
  warnings: string[];
  /** Active delivery groups page joined by the facade (may be empty). */
  deliveryGroups: DeliveryGroupView[];
  deliveryGroupsCount: number;
  deliveryGroupsHasMore: boolean;
  deliveryGroupsNextCursor?: string;
  /** Honest bounded-discovery / membership-completeness contract. */
  standaloneContinuation?: StandalonePRContinuation;
  /** Always present from the list envelope. */
  githubViewer: GitHubViewerIdentity;
}

interface PullRequestsResponse {
  success: boolean;
  data: {
    pull_requests: GitPullRequest[];
    github_viewer: GitHubViewerIdentity;
    warnings?: string[];
    delivery_groups?: DeliveryGroupView[];
    delivery_groups_count?: number;
    delivery_groups_has_more?: boolean;
    delivery_groups_next_cursor?: string;
    standalone_continuation?: StandalonePRContinuation;
  };
  error?: string;
}

export interface FetchPullRequestsOptions {
  state?: PullRequestListState;
  deliveryGroupsLimit?: number;
  deliveryGroupsCursor?: string;
  standaloneRepo?: string;
  standalonePage?: number;
}

/** GET /api/workspaces/{ws}/pull-requests?state= */
export async function fetchPullRequests(
  workspaceId: string,
  stateOrOptions: PullRequestListState | FetchPullRequestsOptions = "all",
): Promise<PullRequestList> {
  const options: FetchPullRequestsOptions =
    typeof stateOrOptions === "string"
      ? { state: stateOrOptions }
      : stateOrOptions;
  const state = options.state ?? "all";
  const params = new URLSearchParams();
  params.set("state", state);
  if (options.deliveryGroupsLimit != null) {
    params.set("delivery_groups_limit", String(options.deliveryGroupsLimit));
  }
  if (options.deliveryGroupsCursor) {
    params.set("delivery_groups_cursor", options.deliveryGroupsCursor);
  }
  if (options.standaloneRepo) {
    params.set("standalone_repo", options.standaloneRepo);
  }
  if (options.standalonePage != null) {
    params.set("standalone_page", String(options.standalonePage));
  }
  const url = `${wsUrl(workspaceId, "/pull-requests")}?${params.toString()}`;
  const result = await get<PullRequestsResponse>(url);
  const data = result.data;
  return {
    pullRequests: data?.pull_requests ?? [],
    warnings: data?.warnings ?? [],
    deliveryGroups: data?.delivery_groups ?? [],
    deliveryGroupsCount: data?.delivery_groups_count ?? 0,
    deliveryGroupsHasMore: data?.delivery_groups_has_more ?? false,
    ...(data?.delivery_groups_next_cursor
      ? { deliveryGroupsNextCursor: data.delivery_groups_next_cursor }
      : {}),
    ...(data?.standalone_continuation
      ? { standaloneContinuation: data.standalone_continuation }
      : {}),
    githubViewer: data?.github_viewer ?? {
      status: "unavailable",
      source: "none",
      message: "GitHub viewer missing from list response",
    },
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
export type PullRequestReadinessPreview =
  components["schemas"]["PullRequestReadinessPreview"];
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
