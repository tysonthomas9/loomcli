import { del, get, patch, post, put, wsUrl } from "@/api/common";
import { ApiError } from "@/types/common";

export type SkillScope = "workspace" | "role";

export type SkillsScopeGroup =
  | { kind: "workspace" }
  | { kind: "role"; role: string };

export interface SkillCatalogFile {
  path: string;
  revision: string;
  executable: boolean;
}

export interface SkillCatalogSkill {
  name: string;
  scope: SkillScope;
  role?: string | undefined;
  description: string;
  content_revision: string;
  files: SkillCatalogFile[];
  created_by?: string | undefined;
  updated_by?: string | undefined;
  source?: string | undefined;
  source_ref?: string | undefined;
  created_at: string;
  updated_at: string;
}

export interface SkillCatalogGroup {
  scope: SkillScope;
  role?: string | undefined;
  skills: SkillCatalogSkill[];
}

export interface SkillsCatalogResponse {
  groups: SkillCatalogGroup[];
}

export interface SkillDetail extends SkillCatalogSkill {
  content: string;
}

export interface SkillFileResponse {
  path: string;
  content: string;
  executable: boolean;
  revision: string;
  skill_ref: string;
}

export interface CreateSkillRequest {
  name: string;
  description: string;
  content?: string | undefined;
  source_ref?: string | undefined;
}

export interface PatchSkillRequest {
  description?: string | undefined;
  source_ref?: string | undefined;
}

export interface PutSkillFileRequest {
  content: string;
  executable: boolean;
}

export interface SkillCapabilitiesResponse {
  can_edit_role_scope: boolean;
  workspace_scope: "read_only";
}

export type SkillApiErrorCode =
  | "workspace_scope_readonly"
  | "precondition_failed"
  | "skill_provenance_conflict"
  | "precondition_required"
  | "invalid_precondition";

export interface SkillApiFailure {
  status: number;
  code: SkillApiErrorCode | null;
  message: string;
  revision?: string | undefined;
  owner?: string | undefined;
  source?: string | undefined;
}

function skillBasePath(group: SkillsScopeGroup): string {
  return group.kind === "workspace"
    ? "/skills"
    : `/roles/${encodeURIComponent(group.role)}/skills`;
}

function skillPath(
  workspaceId: string,
  group: SkillsScopeGroup,
  name?: string,
): string {
  const suffix = name ? `/${encodeURIComponent(name)}` : "";
  return wsUrl(workspaceId, `${skillBasePath(group)}${suffix}`);
}

function skillFilePath(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  path: string,
): string {
  return `${skillPath(workspaceId, group, name)}/files/${path
    .split("/")
    .map(encodeURIComponent)
    .join("/")}`;
}

function etag(revision: string): string {
  return revision === "*" ? revision : `"${revision}"`;
}

function errorBody(error: ApiError): Record<string, unknown> | null {
  return error.body && typeof error.body === "object"
    ? (error.body as Record<string, unknown>)
    : null;
}

export function mapSkillApiError(error: unknown): SkillApiFailure | null {
  if (!(error instanceof ApiError)) return null;
  const body = errorBody(error);
  const rawCode = body?.code;
  const code =
    typeof rawCode === "string" &&
    [
      "workspace_scope_readonly",
      "precondition_failed",
      "skill_provenance_conflict",
      "precondition_required",
      "invalid_precondition",
    ].includes(rawCode)
      ? (rawCode as SkillApiErrorCode)
      : null;
  const rawRevision = body?.revision;
  const rawOwner = body?.owner;
  const rawSource = body?.source;
  return {
    status: error.status,
    code,
    message: error.message,
    ...(typeof rawRevision === "string" ? { revision: rawRevision } : {}),
    ...(typeof rawOwner === "string" && rawOwner ? { owner: rawOwner } : {}),
    ...(typeof rawSource === "string" && rawSource
      ? { source: rawSource }
      : {}),
  };
}

export function listSkills(
  workspaceId: string,
  options: { signal?: AbortSignal } = {},
): Promise<SkillsCatalogResponse> {
  const url = wsUrl(workspaceId, "/skills");
  return options.signal
    ? get<SkillsCatalogResponse>(url, options)
    : get<SkillsCatalogResponse>(url);
}

export function getSkill(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  options: { signal?: AbortSignal } = {},
): Promise<SkillDetail> {
  const url = skillPath(workspaceId, group, name);
  return options.signal
    ? get<SkillDetail>(url, options)
    : get<SkillDetail>(url);
}

export function createSkill(
  workspaceId: string,
  group: SkillsScopeGroup,
  request: CreateSkillRequest,
): Promise<SkillDetail> {
  return post<SkillDetail>(skillPath(workspaceId, group), request);
}

export function patchSkill(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  request: PatchSkillRequest,
  ifMatch: string,
): Promise<SkillDetail> {
  return patch<SkillDetail>(skillPath(workspaceId, group, name), request, {
    headers: { "If-Match": etag(ifMatch) },
  });
}

export async function deleteSkill(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  ifMatch: string,
): Promise<void> {
  await del<void>(skillPath(workspaceId, group, name), {
    headers: { "If-Match": etag(ifMatch) },
  });
}

export function getSkillFile(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  path: string,
  options: { signal?: AbortSignal } = {},
): Promise<SkillFileResponse> {
  const url = skillFilePath(workspaceId, group, name, path);
  return options.signal
    ? get<SkillFileResponse>(url, options)
    : get<SkillFileResponse>(url);
}

export function putSkillFile(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  path: string,
  request: PutSkillFileRequest,
  preconditions: { ifMatch?: string; createOnly?: boolean },
  options: { signal?: AbortSignal } = {},
): Promise<SkillFileResponse> {
  const headers: Record<string, string> = {};
  if (preconditions.ifMatch) {
    headers["If-Match"] = etag(preconditions.ifMatch);
  }
  if (preconditions.createOnly) headers["If-None-Match"] = "*";
  return put<SkillFileResponse>(
    skillFilePath(workspaceId, group, name, path),
    request,
    { ...options, headers },
  );
}

export async function deleteSkillFile(
  workspaceId: string,
  group: SkillsScopeGroup,
  name: string,
  path: string,
  ifMatch: string,
): Promise<void> {
  await del<void>(skillFilePath(workspaceId, group, name, path), {
    headers: { "If-Match": etag(ifMatch) },
  });
}

export function getSkillCapabilities(
  workspaceId: string,
): Promise<SkillCapabilitiesResponse> {
  return get<SkillCapabilitiesResponse>(
    wsUrl(workspaceId, "/skill-capabilities"),
  );
}
