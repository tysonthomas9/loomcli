/**
 * Persistence helpers for SwimLaneBoard collapsed-lane state.
 * Uses scoped localStorage keyed by workspace and groupBy field.
 */

import {
  DEFAULT_COLUMNS,
  createColumns,
  type KanbanColumnConfig,
} from "@/components/KanbanBoard";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import type { GroupByField } from "./groupingUtils";

/**
 * Scoped key suffix for collapsed lanes state.
 * Combined with groupBy for unique key per grouping mode.
 */
function scopedLaneKey(groupBy: GroupByField): string {
  return `swimlane-collapsed-${groupBy}`;
}

/**
 * Load collapsed lanes from scoped localStorage.
 */
export function loadCollapsedLanes(
  groupBy: GroupByField,
  wsId: string | null,
): Set<string> {
  if (groupBy === "none" || !wsId) return new Set();
  try {
    const stored = wsGet(wsId, scopedLaneKey(groupBy));
    if (stored) {
      const parsed: unknown = JSON.parse(stored);
      if (
        Array.isArray(parsed) &&
        parsed.every((item): item is string => typeof item === "string")
      ) {
        return new Set(parsed);
      }
    }
  } catch {
    // Silently fail if localStorage unavailable or invalid JSON
  }
  return new Set();
}

/**
 * Save collapsed lanes to scoped localStorage.
 */
export function saveCollapsedLanes(
  groupBy: GroupByField,
  lanes: Set<string>,
  wsId: string | null,
): void {
  if (groupBy === "none" || !wsId) return;
  wsSet(wsId, scopedLaneKey(groupBy), JSON.stringify([...lanes]));
}

/**
 * Resolve the final column configuration for a SwimLaneBoard.
 * Priority: props.columns > includeEpics when swim lanes are active >
 * default 5-column layout.
 */
export function resolveColumns(
  propColumns: KanbanColumnConfig[] | undefined,
  groupBy: GroupByField,
): KanbanColumnConfig[] {
  if (propColumns) return propColumns;
  if (groupBy !== "none") return createColumns({ includeEpics: true });
  return DEFAULT_COLUMNS;
}
