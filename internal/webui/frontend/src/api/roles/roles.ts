import { ApiError, get, patch, wsUrl } from "@/api/common";

export type RoleSourceKind =
  | "builtinTemplate"
  | "managed"
  | "file"
  | "inline"
  | "builtinSelector";

export type RoleEditableReason =
  | ""
  | "builtin"
  | "managed"
  | "unreadable"
  | "external";

export interface RoleMetadataDTO {
  name: string;
  description: string;
  kind: "worker" | "interactive";
  sourceKind: RoleSourceKind;
  editable: boolean;
  editableReason: RoleEditableReason;
  updatedAt: string;
}

export interface RolePromptDTO {
  role: RoleMetadataDTO;
  sourceKind: RoleSourceKind;
  sourceBody: string;
  sourceError?: string;
  editable: boolean;
  editableReason: RoleEditableReason;
  revision: string;
  activationNote: string;
}

export interface UpdateRolePromptRequest {
  prompt: string;
  expectedRevision: string;
}

interface ListSuccess {
  success: true;
  data: RoleMetadataDTO[];
  total: number;
}

interface ItemSuccess {
  success: true;
  data: RolePromptDTO;
}

interface Failure {
  success: false;
  error: string;
  code?: string;
}

export async function listRoles(
  workspaceId: string,
): Promise<RoleMetadataDTO[]> {
  const response = await get<ListSuccess | Failure>(
    wsUrl(workspaceId, "/roles"),
  );
  if (!response.success) {
    throw new ApiError(0, response.error, response);
  }
  return response.data;
}

export async function getRole(
  workspaceId: string,
  name: string,
): Promise<RolePromptDTO> {
  const response = await get<ItemSuccess | Failure>(roleUrl(workspaceId, name));
  if (!response.success) {
    throw new ApiError(0, response.error, response);
  }
  return response.data;
}

export async function updateRolePrompt(
  workspaceId: string,
  name: string,
  request: UpdateRolePromptRequest,
): Promise<RolePromptDTO> {
  const response = await patch<ItemSuccess | Failure>(
    roleUrl(workspaceId, name),
    request,
  );
  if (!response.success) {
    throw new ApiError(0, response.error, response);
  }
  return response.data;
}

function roleUrl(workspaceId: string, name: string): string {
  return wsUrl(workspaceId, `/roles/${encodeURIComponent(name)}`);
}
