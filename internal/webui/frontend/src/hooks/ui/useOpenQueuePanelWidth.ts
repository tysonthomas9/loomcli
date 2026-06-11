/**
 * Persists the /agents Open Queue panel width per workspace.
 */

import { useCallback, useEffect, useState } from "react";

import { wsGet, wsSet } from "@/utils/scopedStorage";

const SK_OPEN_QUEUE_WIDTH = "open-queue-panel-width";
export const OPEN_QUEUE_PANEL_DEFAULT_WIDTH = 420;
export const OPEN_QUEUE_PANEL_MIN_WIDTH = 280;
export const OPEN_QUEUE_PANEL_MAX_WIDTH = 720;

function clampWidth(value: number): number {
  return Math.min(
    OPEN_QUEUE_PANEL_MAX_WIDTH,
    Math.max(OPEN_QUEUE_PANEL_MIN_WIDTH, value),
  );
}

function readStoredWidth(workspaceId: string | undefined): number {
  if (!workspaceId) return OPEN_QUEUE_PANEL_DEFAULT_WIDTH;
  const stored = wsGet(workspaceId, SK_OPEN_QUEUE_WIDTH);
  if (stored === null) return OPEN_QUEUE_PANEL_DEFAULT_WIDTH;
  const parsed = Number(stored);
  if (Number.isNaN(parsed)) return OPEN_QUEUE_PANEL_DEFAULT_WIDTH;
  return clampWidth(parsed);
}

export interface UseOpenQueuePanelWidthReturn {
  width: number;
  applyDelta: (deltaPx: number) => void;
  resetWidth: () => void;
}

export function useOpenQueuePanelWidth(
  workspaceId: string | undefined,
): UseOpenQueuePanelWidthReturn {
  const [width, setWidth] = useState(() => readStoredWidth(workspaceId));

  useEffect(() => {
    setWidth(readStoredWidth(workspaceId));
  }, [workspaceId]);

  const persist = useCallback(
    (next: number) => {
      const clamped = clampWidth(next);
      setWidth(clamped);
      if (workspaceId) {
        wsSet(workspaceId, SK_OPEN_QUEUE_WIDTH, String(clamped));
      }
    },
    [workspaceId],
  );

  const applyDelta = useCallback(
    (deltaPx: number) => {
      setWidth((prev) => {
        const clamped = clampWidth(prev + deltaPx);
        if (workspaceId) {
          wsSet(workspaceId, SK_OPEN_QUEUE_WIDTH, String(clamped));
        }
        return clamped;
      });
    },
    [workspaceId],
  );

  const resetWidth = useCallback(() => {
    persist(OPEN_QUEUE_PANEL_DEFAULT_WIDTH);
  }, [persist]);

  return { width, applyDelta, resetWidth };
}
