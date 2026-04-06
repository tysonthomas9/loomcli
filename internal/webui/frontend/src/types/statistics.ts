/**
 * Statistics and aggregate types.
 * Statistics aliased from generated OpenAPI schema: components.schemas.Statistics
 * EpicStatus kept hand-written (no standalone schema in spec).
 */

import type { components } from "./generated/openapi";
import type { Issue } from "./issue";

/**
 * Aggregate statistics.
 * Maps to Go types.Statistics via OpenAPI schema.
 */
export type Statistics = components["schemas"]["Statistics"];

/**
 * Epic status with completion information.
 * Maps to Go types.EpicStatus. No standalone schema in spec — kept hand-written.
 */
export interface EpicStatus {
  epic: Issue | null;
  total_children: number;
  closed_children: number;
  eligible_for_close: boolean;
}
