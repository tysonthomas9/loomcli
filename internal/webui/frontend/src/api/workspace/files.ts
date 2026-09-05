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
  version: string;
}

export interface FileStatData {
  path: string;
  is_dir: boolean;
  size: number;
  mod_time: string;
  version: string;
}

export interface FileMutationData {
  success: boolean;
  version: string;
}

export interface FileWritePreconditions {
  ifMatch?: string;
  createOnly?: boolean;
}

export interface FileIndexData {
  paths: string[];
  truncated: boolean;
  partial_reasons: FilePartialReason[];
}

export type FilePartialReason =
  | "file_count"
  | "result_count"
  | "byte_limit"
  | "file_size"
  | "deadline"
  | "canceled";

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
  partial_reasons: FilePartialReason[];
}

export interface FileCheckoutError {
  kind: "agent" | "repo";
  agent?: string;
  repo: string;
  error: string;
}

export interface FileGitStatusData {
  status: Record<string, string>;
  partial: boolean;
  limit_hit: boolean;
  errors: FileCheckoutError[];
}

export interface FileDiffData {
  path: string;
  patch: string;
  partial: boolean;
  limit_hit: boolean;
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
  partial: boolean;
  limit_hit: boolean;
}

export interface FileHistoryEntry {
  kind: "commit";
  sha: string;
  author: string;
  time: string;
  summary: string;
}

export interface FileHistoryData {
  path: string;
  entries: FileHistoryEntry[];
  partial: boolean;
  limit_hit: boolean;
}

export type FileScope = "workspace" | "repo" | "agent";

export interface FileScopeRef {
  scope: FileScope;
  target?: string | null | undefined;
  repo?: string | null | undefined;
}

export type FileCheckout = components["schemas"]["FileCheckout"];
export type FileCheckoutRepairRequest =
  components["schemas"]["FileCheckoutRepairRequest"];
export type FileCheckoutRepairResponse =
  components["schemas"]["FileCheckoutRepairResponse"];

export type FileCheckoutsResponse =
  components["schemas"]["FileCheckoutsResponse"];

export type FileCapabilitiesResponse =
  components["schemas"]["FileCapabilitiesResponse"];

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
 * List files in any scoped file browser root (one level).
 * GET /api/workspaces/{ws}/files/tree?scope=&target=&path=
 */
export async function listScopedDir(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path?: string,
  requestOptions: { signal?: AbortSignal } = {},
): Promise<DirListData> {
  const url = scopedUrl(workspaceId, "/files/tree", scopeRef, path);
  return requestOptions.signal
    ? get<DirListData>(url, requestOptions)
    : get<DirListData>(url);
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
  requestOptions: { signal?: AbortSignal } = {},
): Promise<FileReadData> {
  const url = scopedUrl(
    workspaceId,
    "/files",
    scopeRef,
    path,
    rev ? { rev } : undefined,
  );
  return requestOptions.signal
    ? get<FileReadData>(url, requestOptions)
    : get<FileReadData>(url);
}

/** Get the strong mutation version for a file or bounded directory manifest. */
export async function statScopedPath(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
): Promise<FileStatData> {
  return get<FileStatData>(
    scopedUrl(workspaceId, "/files/stat", scopeRef, path),
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
  options: { signal?: AbortSignal } = {},
): Promise<FileCheckoutsResponse> {
  const url = wsUrl(workspaceId, "/files/checkouts");
  const data = await (options.signal
    ? get<FileCheckoutsResponse>(url, options)
    : get<FileCheckoutsResponse>(url));
  if (
    !data ||
    !Array.isArray(data.checkouts) ||
    typeof data.partial !== "boolean" ||
    typeof data.limit_hit !== "boolean" ||
    !Array.isArray(data.errors)
  )
    throw new Error("Invalid file checkout metadata");
  for (const checkout of data.checkouts) {
    if (
      !checkout ||
      !["agent", "repo"].includes(checkout.kind) ||
      typeof checkout.repo !== "string" ||
      !checkout.repo ||
      typeof checkout.exists !== "boolean" ||
      !Number.isInteger(checkout.change_count) ||
      checkout.change_count < 0 ||
      (checkout.kind === "agent" &&
        (typeof checkout.agent !== "string" || !checkout.agent))
    )
      throw new Error("Invalid file checkout record");
    for (const field of ["partial", "limit_hit", "status_error"] as const) {
      if (checkout[field] !== undefined && typeof checkout[field] !== "boolean")
        throw new Error("Invalid file checkout flag");
    }
    for (const field of ["agent", "branch", "error"] as const) {
      if (checkout[field] !== undefined && typeof checkout[field] !== "string")
        throw new Error("Invalid file checkout text");
    }
  }
  for (const error of data.errors) {
    if (
      !error ||
      !["agent", "repo"].includes(error.kind) ||
      typeof error.repo !== "string" ||
      typeof error.error !== "string" ||
      (error.agent !== undefined && typeof error.agent !== "string")
    )
      throw new Error("Invalid file checkout error");
  }
  return data;
}

/** GET /api/workspaces/{ws}/files/capabilities */
export async function getFileCapabilities(
  workspaceId: string,
  options: { signal?: AbortSignal } = {},
): Promise<FileCapabilitiesResponse> {
  const url = wsUrl(workspaceId, "/files/capabilities");
  const data = await (options.signal
    ? get<FileCapabilitiesResponse>(url, options)
    : get<FileCapabilitiesResponse>(url));
  if (
    !data ||
    [data.read, data.write, data.sensitive].some(
      (value) => typeof value !== "boolean",
    )
  )
    throw new Error("Invalid file capabilities");
  return data;
}

/**
 * Repair or provision a known file checkout.
 * POST /api/workspaces/{ws}/files/checkouts/repair
 */
export async function repairFileCheckout(
  workspaceId: string,
  request: FileCheckoutRepairRequest,
): Promise<FileCheckoutRepairResponse> {
  return post<FileCheckoutRepairResponse>(
    wsUrl(workspaceId, "/files/checkouts/repair"),
    request,
  );
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
 * Fetch bounded commit history for a file in any scoped root.
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
  preconditions: FileWritePreconditions = {},
  requestOptions: { signal?: AbortSignal } = {},
): Promise<FileMutationData> {
  const headers: Record<string, string> = {};
  if (preconditions.ifMatch) headers["If-Match"] = `"${preconditions.ifMatch}"`;
  if (preconditions.createOnly) headers["If-None-Match"] = "*";
  const url = scopedUrl(workspaceId, "/files", scopeRef, path);
  return Object.keys(headers).length > 0
    ? put<FileMutationData>(url, { content }, { ...requestOptions, headers })
    : requestOptions.signal
      ? put<FileMutationData>(url, { content }, requestOptions)
      : put<FileMutationData>(url, { content });
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
  version?: string,
): Promise<void> {
  const url = scopedUrl(
    workspaceId,
    "/files",
    scopeRef,
    path,
    recursive ? { recursive: 1 } : undefined,
  );
  if (version) {
    await del<ApiSuccess>(url, {
      headers: { "If-Match": `"${version}"` },
    });
  } else {
    await del<ApiSuccess>(url);
  }
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
  sourceVersion?: string,
  destinationVersion?: string,
): Promise<void> {
  await patch<ApiSuccess>(scopedUrl(workspaceId, "/files/move", scopeRef), {
    from,
    to,
    overwrite,
    ...(sourceVersion ? { source_version: sourceVersion } : {}),
    ...(destinationVersion ? { destination_version: destinationVersion } : {}),
  });
}
