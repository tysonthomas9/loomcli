/**
 * Persists the /agents Open Queue panel width per workspace.
 */

import {
  usePersistedPanelWidth,
  type UsePersistedPanelWidthReturn,
} from "./usePersistedPanelWidth";

export const OPEN_QUEUE_PANEL_DEFAULT_WIDTH = 420;
export const OPEN_QUEUE_PANEL_MIN_WIDTH = 280;
export const OPEN_QUEUE_PANEL_MAX_WIDTH = 720;

export type UseOpenQueuePanelWidthReturn = UsePersistedPanelWidthReturn;

export function useOpenQueuePanelWidth(
  workspaceId: string | undefined,
): UseOpenQueuePanelWidthReturn {
  return usePersistedPanelWidth(workspaceId, {
    storageKey: "open-queue-panel-width",
    defaultWidth: OPEN_QUEUE_PANEL_DEFAULT_WIDTH,
    minWidth: OPEN_QUEUE_PANEL_MIN_WIDTH,
    maxWidth: OPEN_QUEUE_PANEL_MAX_WIDTH,
  });
}
