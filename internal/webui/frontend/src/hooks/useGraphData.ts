/**
 * React hook for transforming issue data into React Flow nodes and edges.
 *
 * This hook serves as the data transformation layer between the issue data
 * (from useIssues) and the graph visualization (React Flow). It extracts
 * dependency relationships, computes counts, and creates properly typed
 * IssueNode and DependencyEdge objects.
 */

import { MarkerType } from '@xyflow/react';
import { useMemo } from 'react';

import type { Issue, Dependency, DependencyType, IssueNode, DependencyEdge } from '@/types';

import { computeAllBlockedCounts } from './useBlockedChain';

/**
 * Blocking dependency types that affect ready work calculation.
 * These dependencies prevent an issue from being "ready" to work on.
 */
const BLOCKING_TYPES = new Set<DependencyType>([
  'blocks',
  'parent-child',
  'conditional-blocks',
  'waits-for',
]);

/**
 * Check if a dependency type is blocking.
 */
function isBlockingType(type: DependencyType): boolean {
  return BLOCKING_TYPES.has(type);
}

/**
 * Options for the useGraphData hook.
 */
export interface UseGraphDataOptions {
  /** Filter to include only certain dependency types in edges (default: all) */
  includeDependencyTypes?: DependencyType[];
  /** Set of issue IDs that are blocked by open dependencies */
  blockedIssueIds?: Set<string>;
  /** Include edges to issues not in the input set as orphan edges (default: false) */
  includeOrphanEdges?: boolean;
}

/**
 * Return type for the useGraphData hook.
 */
export interface UseGraphDataReturn {
  /** React Flow nodes for rendering */
  nodes: IssueNode[];
  /** React Flow edges for rendering */
  edges: DependencyEdge[];
  /** Map from issue ID to node ID for lookups */
  issueIdToNodeId: Map<string, string>;
  /** Total number of dependencies found */
  totalDependencies: number;
  /** Number of blocking dependencies */
  blockingDependencies: number;
  /** Number of orphan edges (edges to non-existent nodes) */
  orphanEdgeCount: number;
  /** IDs of issues that are targets of orphan edges (missing from input) */
  missingTargetIds: Set<string>;
}

/**
 * Create a node ID from an issue ID.
 */
function createNodeId(issueId: string): string {
  return `node-${issueId}`;
}

/**
 * Create an edge ID from source and target issue IDs and dependency type.
 * Includes dependency type to ensure unique IDs when the same pair has multiple dependencies.
 */
function createEdgeId(
  sourceIssueId: string,
  targetIssueId: string,
  depType: DependencyType
): string {
  return `edge-${sourceIssueId}-${targetIssueId}-${depType}`;
}

/**
 * Timestamp for ghost issues - created once to avoid repeated Date operations.
 */
const GHOST_TIMESTAMP = '1970-01-01T00:00:00.000Z';

/**
 * Create a minimal ghost issue for orphan edge targets.
 */
function createGhostIssue(id: string): Issue {
  return {
    id,
    title: `Missing: ${id}`,
    priority: 4,
    created_at: GHOST_TIMESTAMP,
    updated_at: GHOST_TIMESTAMP,
  };
}

/**
 * Result type for shouldIncludeDependency function.
 */
interface DependencyInclusionResult {
  include: boolean;
  isOrphan: boolean;
}

/**
 * Check if a dependency should be included based on existence and type filtering.
 * Consolidates filter logic to ensure consistent behavior across passes.
 * Returns both inclusion decision and orphan status.
 */
function shouldIncludeDependency(
  dep: Dependency,
  issueIdSet: Set<string>,
  includeDependencyTypes?: DependencyType[],
  allowOrphan?: boolean
): DependencyInclusionResult {
  const targetExists = issueIdSet.has(dep.depends_on_id);

  // Check type filter first
  if (includeDependencyTypes) {
    if (includeDependencyTypes.length === 0) {
      return { include: false, isOrphan: false };
    }
    if (!includeDependencyTypes.includes(dep.type)) {
      return { include: false, isOrphan: false };
    }
  }

  // If target doesn't exist
  if (!targetExists) {
    return { include: allowOrphan ?? false, isOrphan: true };
  }

  return { include: true, isOrphan: false };
}

/**
 * React hook for transforming issue data into React Flow nodes and edges.
 *
 * @example
 * ```tsx
 * function DependencyGraph() {
 *   const { issues } = useIssues()
 *   const { nodes, edges, totalDependencies } = useGraphData(issues)
 *
 *   return (
 *     <ReactFlow nodes={nodes} edges={edges}>
 *       <Background />
 *       <Controls />
 *     </ReactFlow>
 *   )
 * }
 * ```
 */
/**
 * Non-ready statuses - issues with these statuses are never "ready".
 */
const NON_READY_STATUSES = new Set(['closed', 'deferred']);

/**
 * Check if an issue is ready based on status and blocked state.
 * An issue is ready if:
 * - It's not closed or deferred
 * - It's not in the blockedIssueIds set
 */
function computeIsReady(
  issueId: string,
  status: string | undefined,
  blockedIssueIds: Set<string> | undefined
): boolean {
  // Closed and deferred issues are never ready
  if (status && NON_READY_STATUSES.has(status)) {
    return false;
  }
  // If we have blocked info, check if this issue is blocked
  if (blockedIssueIds && blockedIssueIds.has(issueId)) {
    return false;
  }
  // Default to ready if not blocked and not closed/deferred
  return true;
}

export function useGraphData(
  issues: Issue[],
  options: UseGraphDataOptions = {}
): UseGraphDataReturn {
  const { includeDependencyTypes, blockedIssueIds, includeOrphanEdges } = options;

  return useMemo(() => {
    // Handle empty input
    if (issues.length === 0) {
      return {
        nodes: [],
        edges: [],
        issueIdToNodeId: new Map(),
        totalDependencies: 0,
        blockingDependencies: 0,
        orphanEdgeCount: 0,
        missingTargetIds: new Set<string>(),
      };
    }

    // Build set of issue IDs for O(1) existence checks
    const issueIdSet = new Set<string>(issues.map((issue) => issue.id));

    // Build issueIdToNodeId map and count incoming/outgoing dependencies
    const issueIdToNodeId = new Map<string, string>();
    const outgoingCounts = new Map<string, number>();
    const incomingCounts = new Map<string, number>();

    // Track missing targets for orphan edges
    const missingTargetIds = new Set<string>();

    // Initialize counts
    for (const issue of issues) {
      const nodeId = createNodeId(issue.id);
      issueIdToNodeId.set(issue.id, nodeId);
      outgoingCounts.set(issue.id, 0);
      incomingCounts.set(issue.id, 0);
    }

    // First pass: count dependencies (need both directions)
    for (const issue of issues) {
      const deps = issue.dependencies ?? [];
      for (const dep of deps) {
        const result = shouldIncludeDependency(
          dep,
          issueIdSet,
          includeDependencyTypes,
          includeOrphanEdges
        );
        if (!result.include) {
          continue;
        }

        // Track orphan targets
        if (result.isOrphan) {
          missingTargetIds.add(dep.depends_on_id);
        }

        // dep.issue_id is the source (has dependency)
        // dep.depends_on_id is the target (is depended upon)
        outgoingCounts.set(dep.issue_id, (outgoingCounts.get(dep.issue_id) ?? 0) + 1);
        incomingCounts.set(dep.depends_on_id, (incomingCounts.get(dep.depends_on_id) ?? 0) + 1);
      }
    }

    // Create edges first (needed for computing transitive blocked counts)
    const edges: DependencyEdge[] = [];
    let totalDependencies = 0;
    let blockingDependencies = 0;
    let orphanEdgeCount = 0;

    for (const issue of issues) {
      const deps = issue.dependencies ?? [];
      for (const dep of deps) {
        const result = shouldIncludeDependency(
          dep,
          issueIdSet,
          includeDependencyTypes,
          includeOrphanEdges
        );
        if (!result.include) {
          continue;
        }

        const isBlocking = isBlockingType(dep.type);

        edges.push({
          id: createEdgeId(dep.issue_id, dep.depends_on_id, dep.type),
          type: 'dependency',
          source: createNodeId(dep.issue_id),
          target: createNodeId(dep.depends_on_id),
          markerEnd: {
            type: MarkerType.ArrowClosed,
            color: isBlocking ? '#ef4444' : '#64748b',
          },
          data: {
            dependencyType: dep.type,
            isBlocking,
            sourceIssueId: dep.issue_id,
            targetIssueId: dep.depends_on_id,
          },
        });

        totalDependencies++;
        if (isBlocking) {
          blockingDependencies++;
        }
        if (result.isOrphan) {
          orphanEdgeCount++;
        }
      }
    }

    // Create ghost nodes for missing targets
    if (includeOrphanEdges) {
      for (const missingId of missingTargetIds) {
        const nodeId = createNodeId(missingId);
        issueIdToNodeId.set(missingId, nodeId);
        // Initialize counts for ghost nodes (outgoing is 0, incoming was already counted)
        outgoingCounts.set(missingId, 0);
        // incomingCounts was already set during first pass
      }
    }

    // Compute transitive blocked counts using the edges
    // Ghost nodes are excluded from computation as they represent external
    // dependencies with unknown blocking relationships (per design doc)
    const transitiveBlockedCounts = computeAllBlockedCounts(
      issues.map((issue) => issue.id),
      edges
    );

    // Create nodes with blocked count and root blocker flag
    const nodes: IssueNode[] = issues.map((issue) => {
      const isBlocked = blockedIssueIds?.has(issue.id) ?? false;
      const blockedCount = transitiveBlockedCounts.get(issue.id) ?? 0;
      const isRootBlocker = blockedCount > 0 && !isBlocked;

      return {
        id: createNodeId(issue.id),
        type: 'issue',
        position: { x: 0, y: 0 }, // Layout handled by dagre in T061
        data: {
          issue,
          title: issue.title,
          status: issue.status,
          priority: issue.priority,
          issueType: issue.issue_type,
          dependencyCount: outgoingCounts.get(issue.id) ?? 0,
          dependentCount: incomingCounts.get(issue.id) ?? 0,
          isReady: computeIsReady(issue.id, issue.status, blockedIssueIds),
          blockedCount,
          isRootBlocker,
          isClosed: issue.status === 'closed',
        },
      };
    });

    // Create ghost nodes for orphan targets
    if (includeOrphanEdges) {
      for (const missingId of missingTargetIds) {
        const ghostIssue = createGhostIssue(missingId);
        nodes.push({
          id: createNodeId(missingId),
          type: 'issue',
          position: { x: 0, y: 0 },
          data: {
            issue: ghostIssue,
            title: ghostIssue.title,
            status: undefined,
            priority: 4,
            issueType: undefined,
            dependencyCount: 0,
            dependentCount: incomingCounts.get(missingId) ?? 0,
            isReady: false,
            blockedCount: 0,
            isRootBlocker: false,
            isClosed: false,
            isGhostNode: true,
          },
        });
      }
    }

    return {
      nodes,
      edges,
      issueIdToNodeId,
      totalDependencies,
      blockingDependencies,
      orphanEdgeCount,
      missingTargetIds,
    };
  }, [issues, includeDependencyTypes, blockedIssueIds, includeOrphanEdges]);
}
