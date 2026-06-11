/**
 * Persists the WorkspaceTree sidebar width per workspace.
 */

import { useCallback, useEffect, useState } from "react";

import { wsGet, wsSet } from "@/utils/scopedStorage";

const SK_TREE_WIDTH = "workspace-tree-width";
export const WORKSPACE_TREE_DEFAULT_WIDTH = 210;
export const WORKSPACE_TREE_MIN_WIDTH = 160;
export const WORKSPACE_TREE_MAX_WIDTH = 420;

function clampWidth(value: number): number {
  return Math.min(
    WORKSPACE_TREE_MAX_WIDTH,
    Math.max(WORKSPACE_TREE_MIN_WIDTH, value),
  );
}

function readStoredWidth(workspaceId: string | undefined): number {
  if (!workspaceId) return WORKSPACE_TREE_DEFAULT_WIDTH;
  const stored = wsGet(workspaceId, SK_TREE_WIDTH);
  if (stored === null) return WORKSPACE_TREE_DEFAULT_WIDTH;
  const parsed = Number(stored);
  if (Number.isNaN(parsed)) return WORKSPACE_TREE_DEFAULT_WIDTH;
  return clampWidth(parsed);
}

export interface UseWorkspaceTreeWidthReturn {
  width: number;
  applyDelta: (deltaPx: number) => void;
  resetWidth: () => void;
}

export function useWorkspaceTreeWidth(
  workspaceId: string | undefined,
): UseWorkspaceTreeWidthReturn {
  const [width, setWidth] = useState(() => readStoredWidth(workspaceId));

  useEffect(() => {
    setWidth(readStoredWidth(workspaceId));
  }, [workspaceId]);

  const persist = useCallback(
    (next: number) => {
      const clamped = clampWidth(next);
      setWidth(clamped);
      if (workspaceId) {
        wsSet(workspaceId, SK_TREE_WIDTH, String(clamped));
      }
    },
    [workspaceId],
  );

  const applyDelta = useCallback(
    (deltaPx: number) => {
      setWidth((prev) => {
        const clamped = clampWidth(prev + deltaPx);
        if (workspaceId) {
          wsSet(workspaceId, SK_TREE_WIDTH, String(clamped));
        }
        return clamped;
      });
    },
    [workspaceId],
  );

  const resetWidth = useCallback(() => {
    persist(WORKSPACE_TREE_DEFAULT_WIDTH);
  }, [persist]);

  return { width, applyDelta, resetWidth };
}
