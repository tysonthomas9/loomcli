/**
 * API functions for agent worktree file operations.
 * Uses raw fetch for the tree endpoint (untyped spec),
 * openapi-fetch for read/write endpoints.
 */

import { del, get, patch, post, put, wsUrl } from "@/api/common";
import type { components } from "@/types/generated/openapi";

// ============= Types =============

export interface FileEntry {
  name: string;
  is_dir: boolean;
  size: number;
  mod_time: string; // RFC3339
}

export interface DirListData {
  path: string;
  entries: FileEntry[];
}

export interface FileReadData {
  path: string;
  content?: string; // omitted when binary is true
  size: number;
  binary: boolean;
  truncated?: boolean;
}

export interface FileIndexData {
  paths: string[];
  truncated: boolean;
}

export interface FileSearchRequest {
  query: string;
  regex?: boolean;
  include?: string[];
  exclude?: string[];
  caseSensitive?: boolean;
}

export interface FileSearchMatch {
  line: number;
  col: number;
  preview: string;
}

export interface FileSearchFileResult {
  path: string;
  matches: FileSearchMatch[];
}

export interface FileSearchData {
  results: FileSearchFileResult[];
  limitHit: boolean;
}

export type FileGitStatusData = Record<string, string>;

export interface FileDiffData {
  path: string;
  patch: string;
}

export interface FileBlameLine {
  line: number;
  lines: number;
  sha: string;
  author: string;
  time: string;
  summary: string;
}

export interface FileBlameData {
  path: string;
  skipped: boolean;
  reason?: string;
  message?: string;
  lines: FileBlameLine[];
}

export interface FileHistoryEntry {
  kind: "commit" | "save";
  id?: string;
  sha?: string;
  author?: string;
  time: string;
  summary: string;
  content?: string;
  size?: number;
  binary?: boolean;
  truncated?: boolean;
}

export interface FileHistoryData {
  path: string;
  entries: FileHistoryEntry[];
}

export type FileScope = "workspace" | "repo" | "agent";

export interface FileScopeRef {
  scope: FileScope;
  target?: string | null | undefined;
  repo?: string | null | undefined;
}

export type FileCheckout = components["schemas"]["FileCheckout"];

export interface FileCheckoutsResponse {
  checkouts: FileCheckout[];
}

interface ApiSuccess {
  success: boolean;
}

function scopedQuery(
  scopeRef: FileScopeRef,
  path?: string,
  extra?: Record<string, string | number | boolean | undefined>,
): string {
  const parts = [`scope=${encodeURIComponent(scopeRef.scope)}`];
  if (scopeRef.target) {
    parts.push(`target=${encodeURIComponent(scopeRef.target)}`);
  }
  if (scopeRef.scope === "agent" && scopeRef.repo) {
    parts.push(`repo=${encodeURIComponent(scopeRef.repo)}`);
  }
  if (path !== undefined && path !== "") {
    parts.push(`path=${encodeURIComponent(path)}`);
  }
  if (extra) {
    for (const [key, value] of Object.entries(extra)) {
      if (value !== undefined) {
        parts.push(
          `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`,
        );
      }
    }
  }
  return parts.join("&");
}

function scopedUrl(
  workspaceId: string,
  pathPrefix: string,
  scopeRef: FileScopeRef,
  path?: string,
  extra?: Record<string, string | number | boolean | undefined>,
): string {
  return wsUrl(
    workspaceId,
    `${pathPrefix}?${scopedQuery(scopeRef, path, extra)}`,
  );
}

// ============= API Functions =============

/**
 * List files in an agent worktree directory (one level).
 * Uses raw fetch because the spec response is untyped.
 */
export async function listWorktreeDir(
  workspaceId: string,
  agentName: string,
  path?: string,
): Promise<DirListData> {
  let url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/files/tree`,
  );
  if (path !== undefined && path !== "") {
    url += `?path=${encodeURIComponent(path)}`;
  }
  return get<DirListData>(url);
}

/**
 * Read a file from an agent worktree.
 * GET /api/workspaces/{ws}/agents/{name}/files?path=
 */
export async function readWorktreeFile(
  workspaceId: string,
  agentName: string,
  path: string,
): Promise<FileReadData> {
  const url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(path)}`,
  );
  return get<FileReadData>(url);
}

/**
 * List files in the workspace folder (one level), the read-only file browser
 * root that spans every repo checkout and agent worktree.
 * GET /api/workspaces/{ws}/files/tree?scope=workspace&path=
 */
export async function listWorkspaceDir(
  workspaceId: string,
  path?: string,
): Promise<DirListData> {
  return listScopedDir(workspaceId, { scope: "workspace" }, path);
}

/**
 * Read a file from the workspace folder (read-only).
 * GET /api/workspaces/{ws}/files?scope=workspace&path=
 */
export async function readWorkspaceFile(
  workspaceId: string,
  path: string,
): Promise<FileReadData> {
  return readScopedFile(workspaceId, { scope: "workspace" }, path);
}

/**
 * Write a file to an agent worktree (atomic write).
 * PUT /api/workspaces/{ws}/agents/{name}/files?path=
 */
export async function writeWorktreeFile(
  workspaceId: string,
  agentName: string,
  path: string,
  content: string,
): Promise<void> {
  const url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(path)}`,
  );
  await put<{ success: boolean }>(url, { content });
}

/**
 * List files in any scoped file browser root (one level).
 * GET /api/workspaces/{ws}/files/tree?scope=&target=&path=
 */
export async function listScopedDir(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path?: string,
): Promise<DirListData> {
  return get<DirListData>(
    scopedUrl(workspaceId, "/files/tree", scopeRef, path),
  );
}

/**
 * Read a file from any scoped file browser root.
 * GET /api/workspaces/{ws}/files?scope=&target=&path=
 */
export async function readScopedFile(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
  rev?: string,
): Promise<FileReadData> {
  return get<FileReadData>(
    scopedUrl(workspaceId, "/files", scopeRef, path, rev ? { rev } : undefined),
  );
}

/**
 * Fetch the quick-open file index for any scoped file browser root.
 * GET /api/workspaces/{ws}/files/index?scope=&target=
 */
export async function indexScopedFiles(
  workspaceId: string,
  scopeRef: FileScopeRef,
): Promise<FileIndexData> {
  return get<FileIndexData>(scopedUrl(workspaceId, "/files/index", scopeRef));
}

/**
 * List known file checkouts and their current local change counts.
 * GET /api/workspaces/{ws}/files/checkouts
 */
export async function listFileCheckouts(
  workspaceId: string,
): Promise<FileCheckoutsResponse> {
  return get<FileCheckoutsResponse>(wsUrl(workspaceId, "/files/checkouts"));
}

/**
 * Search text files in any scoped file browser root.
 * POST /api/workspaces/{ws}/files/search?scope=&target=
 */
export async function searchScopedFiles(
  workspaceId: string,
  scopeRef: FileScopeRef,
  request: FileSearchRequest,
): Promise<FileSearchData> {
  return post<FileSearchData>(
    scopedUrl(workspaceId, "/files/search", scopeRef),
    request,
  );
}

/**
 * Fetch git status decoration data for any scoped file browser root.
 * GET /api/workspaces/{ws}/files/git-status?scope=&target=
 */
export async function gitStatusScoped(
  workspaceId: string,
  scopeRef: FileScopeRef,
): Promise<FileGitStatusData> {
  return get<FileGitStatusData>(
    scopedUrl(workspaceId, "/files/git-status", scopeRef),
  );
}

/**
 * Fetch a unified diff for a file in any scoped file browser root.
 * GET /api/workspaces/{ws}/files/diff?scope=&target=&path=&from=&to=
 */
export async function diffScopedFile(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
  from?: string,
  to?: string,
): Promise<FileDiffData> {
  return get<FileDiffData>(
    scopedUrl(workspaceId, "/files/diff", scopeRef, path, { from, to }),
  );
}

/**
 * Fetch bounded git blame data for a file in any scoped file browser root.
 * GET /api/workspaces/{ws}/files/blame?scope=&target=&path=
 */
export async function blameScopedFile(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
): Promise<FileBlameData> {
  return get<FileBlameData>(
    scopedUrl(workspaceId, "/files/blame", scopeRef, path),
  );
}

/**
 * Fetch commit and browser-save history for a file in any scoped root.
 * GET /api/workspaces/{ws}/files/history?scope=&target=&path=
 */
export async function historyScopedFile(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
): Promise<FileHistoryData> {
  return get<FileHistoryData>(
    scopedUrl(workspaceId, "/files/history", scopeRef, path),
  );
}

/**
 * Create or update a file in any scoped file browser root.
 * PUT /api/workspaces/{ws}/files?scope=&target=&path=
 */
export async function writeScopedFile(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
  content: string,
): Promise<void> {
  await put<ApiSuccess>(scopedUrl(workspaceId, "/files", scopeRef, path), {
    content,
  });
}

/**
 * Delete a file or directory in any scoped file browser root.
 * DELETE /api/workspaces/{ws}/files?scope=&target=&path=&recursive=1
 */
export async function deleteScopedPath(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
  recursive = false,
): Promise<void> {
  await del<ApiSuccess>(
    scopedUrl(
      workspaceId,
      "/files",
      scopeRef,
      path,
      recursive ? { recursive: 1 } : undefined,
    ),
  );
}

/**
 * Create a directory path in any scoped file browser root.
 * POST /api/workspaces/{ws}/files/mkdir?scope=&target=&path=
 */
export async function mkdirScoped(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
): Promise<void> {
  await post<ApiSuccess>(
    scopedUrl(workspaceId, "/files/mkdir", scopeRef, path),
    undefined,
  );
}

/**
 * Rename or move a path within one scoped file browser root.
 * PATCH /api/workspaces/{ws}/files/move?scope=&target=
 */
export async function moveScopedPath(
  workspaceId: string,
  scopeRef: FileScopeRef,
  from: string,
  to: string,
  overwrite = false,
): Promise<void> {
  await patch<ApiSuccess>(scopedUrl(workspaceId, "/files/move", scopeRef), {
    from,
    to,
    overwrite,
  });
}
