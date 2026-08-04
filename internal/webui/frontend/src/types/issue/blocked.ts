/**
 * Blocked-issue info for UI lookup.
 *
 * Lives in src/types/ so contexts, hooks, and other layers can reference
 * the shape without crossing the frontend layer DAG back into
 * src/components/KanbanBoard/.
 */

import type { BlockerRef } from "./issue";

/**
 * Blocked issue info for lookup.
 */
export interface BlockedInfo {
  blockedByCount: number;
  blockedBy: string[];
  blockedByDetails?: BlockerRef[];
}
