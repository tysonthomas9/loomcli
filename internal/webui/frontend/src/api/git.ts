/**
 * API functions for git endpoints.
 * Interfaces with /api/agents/{name}/git/* endpoints.
 */

import { get, post, patch } from "./client";

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

export interface GitPushResult {
  success: boolean;
  message: string;
  already_up_to_date: boolean;
  conflicted_files?: string[];
}

export interface GitPullResult {
  success: boolean;
  message: string;
  already_up_to_date: boolean;
  conflicted_files?: string[];
}

export interface GitSyncResult {
  push_result: GitPushResult;
  pull_result: GitPullResult;
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

function agentGitUrl(agentName: string, action: string): string {
  return `/api/agents/${encodeURIComponent(agentName)}/git/${action}`;
}

/** GET /api/agents/{name}/git/status */
export async function fetchGitStatus(agentName: string): Promise<GitStatus> {
  return get<GitStatus>(agentGitUrl(agentName, "status"));
}

/** POST /api/agents/{name}/git/push */
export async function gitPush(
  agentName: string,
  target?: string,
): Promise<GitPushResult> {
  return post<GitPushResult>(
    agentGitUrl(agentName, "push"),
    { target },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/agents/{name}/git/pull */
export async function gitPull(
  agentName: string,
  source?: string,
): Promise<GitPullResult> {
  return post<GitPullResult>(
    agentGitUrl(agentName, "pull"),
    { source },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/agents/{name}/git/sync */
export async function gitSync(agentName: string): Promise<GitSyncResult> {
  return post<GitSyncResult>(
    agentGitUrl(agentName, "sync"),
    {},
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/agents/{name}/git/pr */
export async function gitCreatePR(
  agentName: string,
  target?: string,
): Promise<GitPRResult> {
  return post<GitPRResult>(
    agentGitUrl(agentName, "pr"),
    { target },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/agents/{name}/git/reset */
export async function gitReset(
  agentName: string,
  branch?: string,
  force?: boolean,
  push?: boolean,
): Promise<GitResetResult> {
  return post<GitResetResult>(
    agentGitUrl(agentName, "reset"),
    { branch, force, push },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/git/push-all — push all worktrees */
export interface GitPushAllResult {
  failed: number;
  results: { name: string; success: boolean }[];
}
export async function gitPushAll(): Promise<GitPushAllResult> {
  return post<GitPushAllResult>("/api/git/push-all", {}); // allow-url
}

/** PATCH /api/agents/{name}/git/target */
export async function gitUpdateTarget(
  agentName: string,
  branch: string,
): Promise<GitTargetResult> {
  return patch<GitTargetResult>(agentGitUrl(agentName, "target"), { branch });
}
