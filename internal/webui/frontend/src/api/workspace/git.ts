/**
 * API functions for git endpoints.
 * Uses raw fetch because most spec responses are untyped Record<string, never>.
 */

import { get, post, patch, put, wsUrl } from "@/api/common";

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
  push_result: GitPushResult | null;
  pull_result: GitPullResult;
}

export interface GitPRResult {
  url?: string;
  created: boolean;
  already_exists: boolean;
  no_commits: boolean;
}

export interface MergeStackView {
  stack_id: string;
  target: string;
  backend: string;
  phase: string;
  reason?: string;
  layers: Array<{
    change: string;
    head: string;
    pr_url: string;
    state: string;
  }>;
}

// The local server names the human as its OS user; human-only is advisory (D28).
const LOCAL_HUMAN = { kind: "human" } as const;

/**
 * Queue "merge up to here" for a change's PR as the local human (D38): the PR
 * and every approved PR below it merge bottom up. The lead's `loom merge`
 * joins the same queue.
 */
export async function queueMergeUpTo(
  workspaceId: string,
  change: string,
): Promise<MergeStackView> {
  return post<MergeStackView>(
    wsUrl(workspaceId, `/changes/${encodeURIComponent(change)}/merge-up-to`),
    { actor: LOCAL_HUMAN },
  );
}

/** A stack's queued, running or blocked merge. */
export interface QueuedMerge {
  stack_id: string;
  target: string;
  pr_number?: number;
  pr_url?: string;
  backend: string;
  phase: string;
  reason?: string;
  /** "lead" for the lead, otherwise the human who asked. */
  queued_by?: string;
}

export async function fetchMergeQueue(
  workspaceId: string,
): Promise<QueuedMerge[]> {
  return get<QueuedMerge[]>(wsUrl(workspaceId, "/git/merge-queue"));
}

export interface GitSettings {
  delivery_mode: "stack" | "trunk";
  lead_may_approve_publish: boolean;
  lead_may_merge: "off" | "when_green";
}

export async function getGitSettings(
  workspaceId: string,
): Promise<GitSettings> {
  return get<GitSettings>(wsUrl(workspaceId, "/git/settings"));
}

export async function updateGitSettings(
  workspaceId: string,
  change: Partial<GitSettings>,
): Promise<{ settings: GitSettings; warning: string }> {
  return put<{ settings: GitSettings; warning: string }>(
    wsUrl(workspaceId, "/git/settings"),
    { ...change, actor: LOCAL_HUMAN },
  );
}

export interface GitResetResult {
  success: boolean;
  message: string;
  previous_branch?: string;
  pushed: boolean;
  capture_ref?: string;
  ignored?: Array<{ path: string; size: number }>;
}

export interface GitResetPreview {
  ignored: Array<{ path: string; size: number }>;
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
  changeId: string,
): Promise<GitPRResult> {
  return post<GitPRResult>(
    agentGitUrl(workspaceId, agentName, "pr"),
    { change_id: changeId },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** GET /api/workspaces/{ws}/agents/{name}/git/reset-preview */
export async function gitResetPreview(
  workspaceId: string,
  agentName: string,
): Promise<GitResetPreview> {
  return get<GitResetPreview>(
    agentGitUrl(workspaceId, agentName, "reset-preview"),
  );
}

/** POST /api/workspaces/{ws}/agents/{name}/git/reset */
export async function gitReset(
  workspaceId: string,
  agentName: string,
  branch?: string,
  force?: boolean,
): Promise<GitResetResult> {
  return post<GitResetResult>(
    agentGitUrl(workspaceId, agentName, "reset"),
    { branch, force },
    {
      timeout: GIT_ACTION_TIMEOUT,
    },
  );
}

/** POST /api/workspaces/{ws}/git/push-all — push all worktrees */
export interface GitPushAllResult {
  failed: number;
  results: { name: string; success: boolean }[];
}
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
