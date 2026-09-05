/**
 * API functions for git endpoints.
 * Uses raw fetch because most spec responses are untyped Record<string, never>.
 */

import { get, post, patch, wsUrl } from "@/api/common";

// ============= Types =============

export interface GitStatus {
  branch: string;
  target_branch: string;
  is_clean: boolean;
  ahead: number;
  behind: number;
  changed_files: string[];
  conflicted_files: string[];
  has_conflicts: boolean;
  stash_count: number;
}

export interface GitPullResult {
  success: boolean;
  message: string;
  already_up_to_date: boolean;
  conflicted_files?: string[];
}

export interface GitPRResult {
  url?: string;
  created: boolean;
  already_exists: boolean;
  no_commits: boolean;
}

export interface GitResetResult {
  success: boolean;
  message: string;
  previous_branch?: string;
  pushed: boolean;
}

export interface GitResetLockedResponse {
  error: string;
  lock_info: {
    agent: string;
    pid: number;
    duration: string;
    task_id?: string;
  };
}

export interface GitTargetResult {
  success: boolean;
  branch: string;
}

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
