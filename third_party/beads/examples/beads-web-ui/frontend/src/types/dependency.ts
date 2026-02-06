/**
 * Dependency relationship types.
 */

import type { ISODateString } from './common';

/**
 * Well-known dependency types.
 */
export type KnownDependencyType =
  // Workflow types (affect ready work calculation)
  | 'blocks'
  | 'parent-child'
  | 'conditional-blocks'
  | 'waits-for'
  // Association types
  | 'related'
  | 'discovered-from'
  // Graph link types
  | 'replies-to'
  | 'relates-to'
  | 'duplicates'
  | 'supersedes'
  // Entity types (HOP foundation)
  | 'authored-by'
  | 'assigned-to'
  | 'approved-by'
  | 'attests'
  // Convoy tracking
  | 'tracks'
  // Reference types
  | 'until'
  | 'caused-by'
  | 'validates'
  // Delegation types
  | 'delegated-from';

/**
 * Dependency type that allows custom types.
 */
export type DependencyType = KnownDependencyType | (string & {});

/**
 * Dependency type constants.
 */
export const DepBlocks: DependencyType = 'blocks';
export const DepParentChild: DependencyType = 'parent-child';
export const DepConditionalBlocks: DependencyType = 'conditional-blocks';
export const DepWaitsFor: DependencyType = 'waits-for';
export const DepRelated: DependencyType = 'related';
export const DepDiscoveredFrom: DependencyType = 'discovered-from';
export const DepRepliesTo: DependencyType = 'replies-to';
export const DepRelatesTo: DependencyType = 'relates-to';
export const DepDuplicates: DependencyType = 'duplicates';
export const DepSupersedes: DependencyType = 'supersedes';
export const DepAuthoredBy: DependencyType = 'authored-by';
export const DepAssignedTo: DependencyType = 'assigned-to';
export const DepApprovedBy: DependencyType = 'approved-by';
export const DepAttests: DependencyType = 'attests';
export const DepTracks: DependencyType = 'tracks';
export const DepUntil: DependencyType = 'until';
export const DepCausedBy: DependencyType = 'caused-by';
export const DepValidates: DependencyType = 'validates';
export const DepDelegatedFrom: DependencyType = 'delegated-from';

/**
 * Dependency relationship between issues.
 * Maps to Go types.Dependency.
 */
export interface Dependency {
  issue_id: string;
  depends_on_id: string;
  type: DependencyType;
  created_at: ISODateString;
  created_by?: string;
  metadata?: string;
  thread_id?: string;
}

/**
 * Dependency counts for an issue.
 */
export interface DependencyCounts {
  dependency_count: number;
  dependent_count: number;
}
