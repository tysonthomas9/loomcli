/**
 * Filter types for list/query operations.
 */

import type { MolType } from "./agent";
import type { Status } from "./status";

/**
 * Sort policy for ready work.
 * Maps to Go types.SortPolicy.
 */
export type SortPolicy = "hybrid" | "priority" | "oldest" | "";

/**
 * Sort policy constants.
 */
export const SortPolicyHybrid: SortPolicy = "hybrid";
export const SortPolicyPriority: SortPolicy = "priority";
export const SortPolicyOldest: SortPolicy = "oldest";

/**
 * Filter for ready work queries.
 * Maps to Go types.WorkFilter.
 */
export interface WorkFilter {
  status?: Status;
  type?: string;
  priority?: number;
  assignee?: string;
  unassigned?: boolean;
  labels?: string[];
  labels_any?: string[];
  limit?: number;
  sort_policy?: SortPolicy;
  parent_id?: string;
  mol_type?: MolType;
  include_deferred?: boolean;
  include_mol_steps?: boolean;
  source_repos?: string[];
}
