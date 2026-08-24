/**
 * Workflow versioning + authoring API (DEV-V5-40 / spec DEV-V5-32 + DEV-V5-33).
 *
 * These endpoints are not in the OpenAPI spec, so — like the workflow-run API in
 * ./workflows.ts — they use the untyped get/post helpers with hand-written types.
 */

import { get, post, wsUrl } from "@/api/common";

/** A registered workflow (driver) with its active version's trust summary. */
export interface WorkflowSummary {
  driver_id: string;
  name: string;
  status: string;
  active_version_id?: string;
  built_in: boolean;
  approved: boolean;
  effective_trust?: string;
  provenance?: string;
  selected_by?: string;
}

/** One immutable built artifact of a workflow. */
export interface WorkflowDriverVersion {
  version_id: string;
  driver_id: string;
  version: number;
  source_digest?: string;
  bundle_digest?: string;
  manifest?: Record<string, string>;
  validation_status?: string;
  created_at?: string;
  updated_at?: string;
}

/** Minimal driver shape returned by the version-action endpoints. */
export interface WorkflowDriver {
  driver_id: string;
  name: string;
  status: string;
  active_version_id?: string;
  metadata?: Record<string, string>;
}

/** One row of GET …/versions. */
export interface WorkflowVersionItem {
  version: WorkflowDriverVersion;
  active: boolean;
  approved: boolean;
  effective_trust: string;
  provenance?: string;
  selected_by?: string;
  bundle_verified: boolean;
}

/** Built-in update state for a built-in workflow (DEV-V5-33 track policy). */
export interface BuiltinVersionsInfo {
  packaged_version_id: string;
  packaged_source_digest: string;
  packaged_artifact_digest: string;
  track: string;
  update_available: boolean;
  previous_active_version_id: string;
  packaged_error?: string;
}

export interface WorkflowVersionsResponse {
  driver_id: string;
  versions: WorkflowVersionItem[];
  builtin?: BuiltinVersionsInfo;
}

/** Shared response for activate / approve / unapprove / rollback. */
export interface WorkflowVersionActionResult {
  driver: WorkflowDriver;
  version: WorkflowDriverVersion;
  active: boolean;
  approved: boolean;
  effective_trust: string;
}

/** Result of POST …/builtin/sync (register + apply track policy). */
export interface BuiltinSyncResult {
  workflow: string;
  driver_id: string;
  packaged: {
    version_id: string;
    source_digest: string;
    artifact_digest: string;
    flue_commit: string;
    registered_new: boolean;
  };
  active_version_id: string;
  previous_active_version_id: string;
  track: string;
  activated: boolean;
  update_available: boolean;
  active_bundle_available: boolean;
  repaired: boolean;
}

/** Result of POST …/versions (author + build a custom version). */
export interface CreateWorkflowVersionResult {
  driver: WorkflowDriver;
  version: WorkflowDriverVersion;
  created_driver: boolean;
  created_version: boolean;
  reused_version: boolean;
  activated: boolean;
  build_diagnostics?: unknown;
}

export interface CreateWorkflowVersionInput {
  files: Record<string, string>;
  entrypoint?: string;
  activate?: boolean;
}

/** The built-in track a version follows. */
export type BuiltinTrack = "auto" | "pinned";

function versionsPath(workflowName: string): string {
  return `/workflows/${encodeURIComponent(workflowName)}/versions`;
}

export async function listWorkflows(
  workspaceId: string,
): Promise<WorkflowSummary[]> {
  const res = await get<{ workflows: WorkflowSummary[] }>(
    wsUrl(workspaceId, "/workflows"),
  );
  return res.workflows ?? [];
}

export async function listWorkflowVersions(
  workspaceId: string,
  workflowName: string,
): Promise<WorkflowVersionsResponse> {
  return get<WorkflowVersionsResponse>(
    wsUrl(workspaceId, versionsPath(workflowName)),
  );
}

export async function createWorkflowVersion(
  workspaceId: string,
  workflowName: string,
  input: CreateWorkflowVersionInput,
): Promise<CreateWorkflowVersionResult> {
  return post<CreateWorkflowVersionResult>(
    wsUrl(workspaceId, versionsPath(workflowName)),
    input,
  );
}

export async function approveWorkflowVersion(
  workspaceId: string,
  workflowName: string,
  versionId: string,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `${versionsPath(workflowName)}/${encodeURIComponent(versionId)}/approve`,
    ),
    {},
  );
}

export async function unapproveWorkflowVersion(
  workspaceId: string,
  workflowName: string,
  versionId: string,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `${versionsPath(workflowName)}/${encodeURIComponent(versionId)}/unapprove`,
    ),
    {},
  );
}

export async function activateWorkflowVersion(
  workspaceId: string,
  workflowName: string,
  versionId: string,
  track?: BuiltinTrack,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `${versionsPath(workflowName)}/${encodeURIComponent(versionId)}/activate`,
    ),
    track ? { track } : {},
  );
}

export async function rollbackWorkflow(
  workspaceId: string,
  workflowName: string,
  versionId?: string,
): Promise<WorkflowVersionActionResult> {
  return post<WorkflowVersionActionResult>(
    wsUrl(
      workspaceId,
      `/workflows/${encodeURIComponent(workflowName)}/rollback`,
    ),
    versionId ? { version_id: versionId } : {},
  );
}

export async function syncBuiltinWorkflow(
  workspaceId: string,
  workflowName: string,
  track?: BuiltinTrack,
): Promise<BuiltinSyncResult> {
  return post<BuiltinSyncResult>(
    wsUrl(
      workspaceId,
      `/workflows/${encodeURIComponent(workflowName)}/builtin/sync`,
    ),
    track ? { track } : {},
  );
}
