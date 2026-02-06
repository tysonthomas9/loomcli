/**
 * Statistics and aggregate types.
 */

import type { Issue } from './issue';

/**
 * Aggregate statistics.
 * Maps to Go types.Statistics.
 */
export interface Statistics {
  total_issues: number;
  open_issues: number;
  in_progress_issues: number;
  closed_issues: number;
  blocked_issues: number;
  deferred_issues: number;
  ready_issues: number;
  tombstone_issues: number;
  pinned_issues: number;
  epics_eligible_for_closure: number;
  average_lead_time_hours: number;
}

/**
 * Epic status with completion information.
 * Maps to Go types.EpicStatus.
 */
export interface EpicStatus {
  epic: Issue | null;
  total_children: number;
  closed_children: number;
  eligible_for_close: boolean;
}
