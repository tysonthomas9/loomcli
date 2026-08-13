/**
 * API functions for git endpoints.
 * Uses the canonical OpenAPI-generated Source Control contracts.
 */

import { get, post, patch, wsUrl } from "@/api/common";
import type { components } from "@/types/generated/openapi";

// ============= Types =============

export type GitStatus = components["schemas"]["GitStatusResponse"];
export type GitPushResult = components["schemas"]["GitMergeResponse"];
export type GitPullResult = components["schemas"]["GitMergeResponse"];
export type GitSyncResult = components["schemas"]["GitSyncResponse"];
export type GitPRResult =
  components["schemas"]["GitPullRequestCreationResponse"];
export type GitResetResult = components["schemas"]["GitResetResponse"];
export type GitResetLockedResponse =
  components["schemas"]["GitResetLockedResponse"];
export type GitTargetResult = components["schemas"]["GitTargetResponse"];

// ============= API Functions =============

const GIT_ACTION_TIMEOUT = 60000;

function agentGitUrl(
  workspaceId: string,
  agentName: string,
  action: string,
): string {
  return wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/git/${action}`,
  );
}

/** GET /api/workspaces/{ws}/agents/{name}/git/status */
export async function fetchGitStatus(
  workspaceId: string,
  agentName: string,
): Promise<GitStatus> {
  return get<GitStatus>(agentGitUrl(workspaceId, agentName, "status"));
}

/** POST /api/workspaces/{ws}/agents/{name}/git/push */
export async function gitPush(
  workspaceId: string,
  agentName: string,
  target?: string,
): Promise<GitPushResult> {
  return post<GitPushResult>(
    agentGitUrl(workspaceId, agentName, "push"),
    { target },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/workspaces/{ws}/agents/{name}/git/pull */
export async function gitPull(
  workspaceId: string,
  agentName: string,
  source?: string,
): Promise<GitPullResult> {
  return post<GitPullResult>(
    agentGitUrl(workspaceId, agentName, "pull"),
    { source },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/workspaces/{ws}/agents/{name}/git/sync */
export async function gitSync(
  workspaceId: string,
  agentName: string,
): Promise<GitSyncResult> {
  return post<GitSyncResult>(
    agentGitUrl(workspaceId, agentName, "sync"),
    {},
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/workspaces/{ws}/agents/{name}/git/pr */
export async function gitCreatePR(
  workspaceId: string,
  agentName: string,
  target?: string,
): Promise<GitPRResult> {
  return post<GitPRResult>(
    agentGitUrl(workspaceId, agentName, "pr"),
    { target },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/workspaces/{ws}/agents/{name}/git/reset */
export async function gitReset(
  workspaceId: string,
  agentName: string,
  branch?: string,
  force?: boolean,
  push?: boolean,
): Promise<GitResetResult> {
  return post<GitResetResult>(
    agentGitUrl(workspaceId, agentName, "reset"),
    { branch, force, push },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/workspaces/{ws}/git/push-all — push all worktrees */
export type GitPushAllResult = components["schemas"]["GitPushAllResponse"];
export async function gitPushAll(
  workspaceId: string,
): Promise<GitPushAllResult> {
  return post<GitPushAllResult>(wsUrl(workspaceId, "/git/push-all"), {});
}

/** PATCH /api/workspaces/{ws}/agents/{name}/git/target */
export async function gitUpdateTarget(
  workspaceId: string,
  agentName: string,
  branch: string,
): Promise<GitTargetResult> {
  return patch<GitTargetResult>(agentGitUrl(workspaceId, agentName, "target"), {
    branch,
  });
}
