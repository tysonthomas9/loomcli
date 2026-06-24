/**
 * Persists the WorkspaceTree sidebar width per workspace.
 */

import {
  usePersistedPanelWidth,
  type UsePersistedPanelWidthReturn,
} from "./usePersistedPanelWidth";

export const WORKSPACE_TREE_DEFAULT_WIDTH = 210;
export const WORKSPACE_TREE_MIN_WIDTH = 160;
export const WORKSPACE_TREE_MAX_WIDTH = 420;

export type UseWorkspaceTreeWidthReturn = UsePersistedPanelWidthReturn;

export function useWorkspaceTreeWidth(
  workspaceId: string | undefined,
): UseWorkspaceTreeWidthReturn {
  return usePersistedPanelWidth(workspaceId, {
    storageKey: "workspace-tree-width",
    defaultWidth: WORKSPACE_TREE_DEFAULT_WIDTH,
    minWidth: WORKSPACE_TREE_MIN_WIDTH,
    maxWidth: WORKSPACE_TREE_MAX_WIDTH,
  });
}
