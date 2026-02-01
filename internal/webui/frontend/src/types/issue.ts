/**
 * Core Issue type and related types.
 */

import type { ISODateString, Priority, Duration } from './common';
import type { Status } from './status';
import type { IssueType } from './issueType';
import type { Dependency, DependencyType } from './dependency';
import type { Comment } from './comment';
import type { EntityRef, Validation, BondRef } from './entity';
import type { AgentState, MolType, WorkType } from './agent';

/**
 * Core Issue interface.
 * Maps to Go types.Issue.
 * Field names match Go JSON tags for direct API compatibility.
 */
export interface Issue {
  // Core Identification
  id: string;

  // Issue Content
  title: string;
  description?: string;
  design?: string;
  acceptance_criteria?: string;
  notes?: string;

  // Status & Workflow
  status?: Status;
  priority: Priority;
  issue_type?: IssueType;

  // Assignment
  assignee?: string;
  owner?: string;
  estimated_minutes?: number | null;

  // Timestamps
  created_at: ISODateString;
  created_by?: string;
  updated_at: ISODateString;
  closed_at?: ISODateString | null;
  close_reason?: string;
  closed_by_session?: string;

  // Time-Based Scheduling
  due_at?: ISODateString | null;
  defer_until?: ISODateString | null;

  // External Integration
  external_ref?: string | null;
  source_system?: string;

  // Compaction Metadata
  compaction_level?: number;
  compacted_at?: ISODateString | null;
  compacted_at_commit?: string | null;
  original_size?: number;

  // Relational Data
  labels?: string[];
  dependencies?: Dependency[];
  comments?: Comment[];

  // Parent-child hierarchy
  parent?: string;        // Parent issue ID
  parent_title?: string;  // Parent title for display

  // Tombstone Fields
  deleted_at?: ISODateString | null;
  deleted_by?: string;
  delete_reason?: string;
  original_type?: string;

  // Messaging Fields
  sender?: string;
  ephemeral?: boolean;

  // Context Markers
  pinned?: boolean;
  is_template?: boolean;

  // Bonding Fields
  bonded_from?: BondRef[];

  // HOP Fields
  creator?: EntityRef;
  validations?: Validation[];
  quality_score?: number | null;
  crystallizes?: boolean;

  // Gate Fields
  await_type?: string;
  await_id?: string;
  timeout?: Duration;
  waiters?: string[];

  // Slot Fields
  holder?: string;

  // Source Tracing Fields
  source_formula?: string;
  source_location?: string;

  // Agent Identity Fields
  hook_bead?: string;
  role_bead?: string;
  agent_state?: AgentState;
  last_activity?: ISODateString | null;
  role_type?: string;
  rig?: string;

  // Molecule Type Fields
  mol_type?: MolType;

  // Work Type Fields
  work_type?: WorkType;

  // Event Fields
  event_kind?: string;
  actor?: string;
  target?: string;
  payload?: string;
}

/**
 * Issue with dependency metadata.
 * Maps to Go types.IssueWithDependencyMetadata.
 */
export interface IssueWithDependencyMetadata extends Issue {
  dependency_type: DependencyType;
}

/**
 * Issue with dependency counts.
 * Maps to Go types.IssueWithCounts.
 */
export interface IssueWithCounts extends Issue {
  dependency_count: number;
  dependent_count: number;
}

/**
 * Simplified dependency for graph visualization.
 * Maps to Go GraphDependency struct from /api/issues/graph.
 */
export interface GraphDependency {
  depends_on_id: string;
  type: DependencyType;
}

/**
 * Issue with full dependency data for graph visualization.
 * Maps to Go GraphIssue struct from /api/issues/graph.
 * Uses Omit to override the dependencies field type.
 */
export interface GraphIssue extends Omit<Issue, 'dependencies'> {
  // Simplified dependency format from graph API
  dependencies?: GraphDependency[];
}

/**
 * Extended issue details with labels, dependencies, and comments.
 * Maps to Go types.IssueDetails.
 * Uses Omit to override the dependencies field type from Dependency[] to IssueWithDependencyMetadata[].
 */
export interface IssueDetails extends Omit<Issue, 'dependencies' | 'labels' | 'comments' | 'parent'> {
  labels?: string[];
  dependencies?: IssueWithDependencyMetadata[];
  dependents?: IssueWithDependencyMetadata[];
  comments?: Comment[];
  parent?: string | null;
}

/**
 * Blocked issue with blocking information.
 * Maps to Go types.BlockedIssue.
 */
export interface BlockedIssue extends Issue {
  blocked_by_count: number;
  blocked_by: string[];
}

/**
 * Tree node in a dependency tree.
 * Maps to Go types.TreeNode.
 */
export interface TreeNode extends Issue {
  depth: number;
  parent_id: string;
  truncated: boolean;
}

/**
 * Molecule progress statistics.
 * Maps to Go types.MoleculeProgressStats.
 */
export interface MoleculeProgressStats {
  molecule_id: string;
  molecule_title: string;
  total: number;
  completed: number;
  in_progress: number;
  current_step_id: string;
  first_closed?: ISODateString | null;
  last_closed?: ISODateString | null;
}
