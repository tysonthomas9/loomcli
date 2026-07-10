import { useCallback, useMemo } from "react";

import {
  activateWorkflowVersion,
  approveWorkflowVersion,
  createWorkflowVersion,
  getWorkflowSource,
  listWorkflowVersions,
  type CreateWorkflowVersionResult,
  type WorkflowSource,
  type WorkflowVersionActionResult,
  type WorkflowVersionsResponse,
} from "@/api/workflows";

export interface SaveWorkflowSourceInput {
  files: Record<string, string>;
  entrypoint: string;
  /** Point the driver at the new version once built (default true). */
  activate?: boolean;
}

export interface UseWorkflowSourceReturn {
  /** Load a builtin workflow's TS source. Rejects with `ApiError` 404 when none. */
  getSource: (name: string) => Promise<WorkflowSource>;
  /** List built driver versions for a workflow. */
  listVersions: (name: string) => Promise<WorkflowVersionsResponse>;
  /**
   * Build + register a new version from edited source. A build failure rejects
   * with `ApiError` (400) carrying the diagnostics; success resolves with
   * `build_diagnostics` + `activated`.
   */
  saveSource: (
    name: string,
    input: SaveWorkflowSourceInput,
  ) => Promise<CreateWorkflowVersionResult>;
  /** Approve a version (untrusted → trusted) so the runtime will run it. */
  approveVersion: (
    name: string,
    versionId: string,
  ) => Promise<WorkflowVersionActionResult>;
  /** Point the driver at an existing version (no rebuild). */
  activateVersion: (
    name: string,
    versionId: string,
  ) => Promise<WorkflowVersionActionResult>;
}

/**
 * Workflow source read + version lifecycle bound to a workspace. Backs the
 * Phase B "View/Edit source" surface: load builtin TS, rebuild a version from
 * edits (surfacing build diagnostics honestly), and approve/activate versions.
 */
export function useWorkflowSource(
  workspaceId: string,
): UseWorkflowSourceReturn {
  const getSource = useCallback(
    (name: string): Promise<WorkflowSource> =>
      getWorkflowSource(workspaceId, name),
    [workspaceId],
  );

  const listVersions = useCallback(
    (name: string): Promise<WorkflowVersionsResponse> =>
      listWorkflowVersions(workspaceId, name),
    [workspaceId],
  );

  const saveSource = useCallback(
    (
      name: string,
      input: SaveWorkflowSourceInput,
    ): Promise<CreateWorkflowVersionResult> =>
      createWorkflowVersion(workspaceId, name, {
        files: input.files,
        entrypoint: input.entrypoint,
        ...(input.activate !== undefined ? { activate: input.activate } : {}),
      }),
    [workspaceId],
  );

  const approveVersion = useCallback(
    (name: string, versionId: string): Promise<WorkflowVersionActionResult> =>
      approveWorkflowVersion(workspaceId, name, versionId),
    [workspaceId],
  );

  const activateVersion = useCallback(
    (name: string, versionId: string): Promise<WorkflowVersionActionResult> =>
      activateWorkflowVersion(workspaceId, name, versionId),
    [workspaceId],
  );

  return useMemo(
    () => ({
      getSource,
      listVersions,
      saveSource,
      approveVersion,
      activateVersion,
    }),
    [getSource, listVersions, saveSource, approveVersion, activateVersion],
  );
}
