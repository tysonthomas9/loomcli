/**
 * Core Issue type and related types.
 * Issue aliased from generated OpenAPI schema (components.schemas.Issue) with extensions
 * for fields not in the spec (HOP, bonding, gate, slot, compaction, tombstone, messaging, etc.).
 * BlockerRef, BlockedIssue, TreeNode aliased from generated schemas.
 * IssueDetails, GraphIssue, GraphDependency, IssueWithDependencyMetadata, IssueWithCounts,
 * MoleculeProgressStats kept hand-written (no standalone schemas or different shapes).
 */

import type { components } from "@/types/generated/openapi";
import type { AgentState, WorkType } from "@/types/agent";
import type { Comment } from "./comment";
import type { ISODateString, Priority, Duration } from "@/types/common";
import type { Dependency, DependencyType } from "./dependency";
import type { EntityRef, Validation, BondRef } from "@/types/common";
import type { IssueType } from "./issueType";
import type { Status } from "./status";

/**
 * Fields present on the hand-written Issue type but absent from the generated schema.
 * These fields come from Go types.Issue but are not in the DTO-derived OpenAPI spec.
 */
interface IssueExtensions {
  // Status & Workflow (wider union than spec enum)
  status?: Status;
  priority: Priority;
  issue_type?: IssueType;

  // Timestamps (branded ISODateString)
  created_at: ISODateString;
  updated_at: ISODateString;
  closed_at?: ISODateString | null;
  due_at?: ISODateString | null;
  defer_until?: ISODateString | null;

  // Agent Identity (wider union than spec enum)
  agent_state?: AgentState;
  last_activity?: ISODateString | null;

  // Typed relational arrays
  dependencies?: Dependency[];
  comments?: Comment[];

  // Assignment
  closed_by_session?: string;

  // External Integration
  source_system?: string;

  // Compaction Metadata
  compaction_level?: number;
  compacted_at?: ISODateString | null;
  compacted_at_commit?: string | null;
  original_size?: number;

  // Parent display
  parent_title?: string;

  // Tombstone Fields
  deleted_at?: ISODateString | null;
  deleted_by?: string;
  delete_reason?: string;
  original_type?: string;

  // Messaging Fields
  sender?: string;
  ephemeral?: boolean;

  // Context Markers
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

  /** Repository that owns this issue (multi-repo workspaces) */
  repo?: string;

  // Work Type Fields
  work_type?: WorkType;

  // Event Fields
  event_kind?: string;
  actor?: string;
  target?: string;
  payload?: string;

  // Kanban-enriched fields (present when fetched with include_blocked=true)
  is_blocked?: boolean;
  blocked_by_count?: number;
  blocked_by?: string[];
  blocked_by_details?: BlockerRef[];
}

/**
 * Core Issue interface.
 * Base from generated OpenAPI schema, extended with fields not in the spec.
 */
export type Issue = Omit<
  components["schemas"]["Issue"],
  // Remove fields we override with wider types in IssueExtensions
  | "status"
  | "priority"
  | "issue_type"
  | "created_at"
  | "updated_at"
  | "closed_at"
  | "due_at"
  | "defer_until"
  | "agent_state"
  | "last_activity"
  | "dependencies"
  | "comments"
> &
  IssueExtensions;

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
export interface GraphIssue extends Omit<Issue, "dependencies"> {
  // Simplified dependency format from graph API
  dependencies?: GraphDependency[];
}

/**
 * Extended issue details with labels, dependencies, and comments.
 * Maps to Go types.IssueDetails.
 * Uses Omit to override the dependencies field type from Dependency[] to IssueWithDependencyMetadata[].
 */
export interface IssueDetails extends Omit<
  Issue,
  "dependencies" | "labels" | "comments" | "parent"
> {
  labels?: string[];
  dependencies?: IssueWithDependencyMetadata[];
  dependents?: IssueWithDependencyMetadata[];
  comments?: Comment[];
  parent?: string | null;
}

/**
 * Blocker reference with title and priority.
 * Aliased from generated OpenAPI schema.
 */
export type BlockerRef = components["schemas"]["BlockerRef"];

/**
 * Blocked issue with blocking information.
 * Maps to Go types.BlockedIssue.
 * Hand-written because the generated schema uses allOf with generated Issue
 * but we need our extended Issue type.
 */
export interface BlockedIssue extends Issue {
  blocked_by_count: number;
  blocked_by: string[];
  blocked_by_details?: BlockerRef[];
}

/**
 * Tree node in a dependency tree.
 * Maps to Go types.TreeNode.
 * Hand-written because the generated schema uses allOf with generated Issue
 * but we need our extended Issue type.
 */
export interface TreeNode extends Issue {
  depth: number;
  parent_id: string;
  truncated: boolean;
}

/**
 * Molecule progress statistics.
 * Maps to Go types.MoleculeProgressStats. No standalone schema in spec.
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
