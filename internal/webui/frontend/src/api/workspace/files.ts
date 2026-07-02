/**
 * API functions for agent worktree file operations.
 * Uses raw fetch for the tree endpoint (untyped spec),
 * openapi-fetch for read/write endpoints.
 */

import { get, wsUrl } from "@/api/common";

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
  let url = wsUrl(workspaceId, `/files/tree?scope=workspace`);
  if (path !== undefined && path !== "") {
    url += `&path=${encodeURIComponent(path)}`;
  }
  return get<DirListData>(url);
}

/**
 * Read a file from the workspace folder (read-only).
 * GET /api/workspaces/{ws}/files?scope=workspace&path=
 */
export async function readWorkspaceFile(
  workspaceId: string,
  path: string,
): Promise<FileReadData> {
  const url = wsUrl(
    workspaceId,
    `/files?scope=workspace&path=${encodeURIComponent(path)}`,
  );
  return get<FileReadData>(url);
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
  const { put } = await import("@/api/common");
  const url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(path)}`,
  );
  await put<{ success: boolean }>(url, { content });
}
