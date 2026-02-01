/**
 * Filter types for list/query operations.
 */

import type { ISODateString } from './common';
import type { Status } from './status';
import type { IssueType } from './issueType';
import type { MolType } from './agent';

/**
 * Sort policy for ready work.
 * Maps to Go types.SortPolicy.
 */
export type SortPolicy = 'hybrid' | 'priority' | 'oldest' | '';

/**
 * Sort policy constants.
 */
export const SortPolicyHybrid: SortPolicy = 'hybrid';
export const SortPolicyPriority: SortPolicy = 'priority';
export const SortPolicyOldest: SortPolicy = 'oldest';

/**
 * Filter for listing issues.
 * Maps to Go types.IssueFilter.
 */
export interface IssueFilter {
  status?: Status;
  priority?: number;
  issue_type?: IssueType;
  assignee?: string;
  labels?: string[];
  labels_any?: string[];
  title_search?: string;
  ids?: string[];
  id_prefix?: string;
  limit?: number;

  // Pattern matching
  title_contains?: string;
  description_contains?: string;
  notes_contains?: string;

  // Date ranges
  created_after?: ISODateString;
  created_before?: ISODateString;
  updated_after?: ISODateString;
  updated_before?: ISODateString;
  closed_after?: ISODateString;
  closed_before?: ISODateString;

  // Empty/null checks
  empty_description?: boolean;
  no_assignee?: boolean;
  no_labels?: boolean;

  // Numeric ranges
  priority_min?: number;
  priority_max?: number;

  // Tombstone filtering
  include_tombstones?: boolean;

  // Ephemeral filtering
  ephemeral?: boolean;

  // Pinned filtering
  pinned?: boolean;

  // Template filtering
  is_template?: boolean;

  // Parent filtering
  parent_id?: string;

  // Molecule type filtering
  mol_type?: MolType;

  // Status exclusion
  exclude_status?: Status[];

  // Type exclusion
  exclude_types?: IssueType[];

  // Time-based scheduling filters
  deferred?: boolean;
  defer_after?: ISODateString;
  defer_before?: ISODateString;
  due_after?: ISODateString;
  due_before?: ISODateString;
  overdue?: boolean;
}

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
}

/**
 * Filter for stale issue queries.
 * Maps to Go types.StaleFilter.
 */
export interface StaleFilter {
  days: number;
  status?: string;
  limit?: number;
}
