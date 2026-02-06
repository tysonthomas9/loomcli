/**
 * Graph types for React Flow dependency visualization.
 */

import type { Node, Edge } from '@xyflow/react';

import type { Priority } from './common';
import type { DependencyType } from './dependency';
import type { Issue } from './issue';
import type { Status } from './status';

/**
 * Data attached to IssueNode in the graph.
 * Contains the essential issue fields needed for rendering.
 */
export interface IssueNodeData extends Record<string, unknown> {
  /** Full issue object for access to all fields */
  issue: Issue;
  /** Pre-extracted fields for quick access in node renderer */
  title: string;
  status: Status | undefined;
  priority: Priority;
  issueType: string | undefined;
  /** Number of dependencies (outgoing edges) */
  dependencyCount: number;
  /** Number of dependents (incoming edges) */
  dependentCount: number;
  /** Whether this issue is ready (no blocking open dependencies) */
  isReady: boolean;
  /** Number of issues this issue transitively blocks */
  blockedCount: number;
  /** Whether this is a root blocker (blocks others but not blocked itself) */
  isRootBlocker: boolean;
  /** Whether this issue is closed */
  isClosed: boolean;
  /** Whether this is a ghost/placeholder node for an orphan target */
  isGhostNode?: boolean;
}

/**
 * Custom node type for displaying issues in the graph.
 * Uses React Flow Node generic with IssueNodeData and 'issue' type.
 */
export type IssueNode = Node<IssueNodeData, 'issue'>;

/**
 * Data attached to DependencyEdge in the graph.
 * Contains dependency metadata for edge rendering.
 */
export interface DependencyEdgeData extends Record<string, unknown> {
  /** Type of dependency relationship */
  dependencyType: DependencyType;
  /** Whether this is a blocking relationship */
  isBlocking: boolean;
  /** Source issue ID (for reference) */
  sourceIssueId: string;
  /** Target issue ID (for reference) */
  targetIssueId: string;
  /** Whether this edge is part of a highlighted chain */
  isHighlighted?: boolean;
}

/**
 * Custom edge type for displaying dependencies in the graph.
 * Uses React Flow Edge generic with DependencyEdgeData and 'dependency' type.
 */
export type DependencyEdge = Edge<DependencyEdgeData, 'dependency'>;

/**
 * Union type of all node types in the graph.
 * Extensible for future node types (e.g., groups, labels).
 */
export type GraphNodeType = IssueNode;

/**
 * Union type of all edge types in the graph.
 * Extensible for future edge types.
 */
export type GraphEdgeType = DependencyEdge;

/**
 * Props for the IssueNode component.
 * Extends React Flow NodeProps with our custom data.
 */
export type { NodeProps } from '@xyflow/react';

/**
 * Props for the DependencyEdge component.
 * Extends React Flow EdgeProps with our custom data.
 */
export type { EdgeProps } from '@xyflow/react';
