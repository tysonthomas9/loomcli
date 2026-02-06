/**
 * Hook for computing blocking dependency chains.
 *
 * Given an issue ID and the graph edges, computes:
 * - All issues transitively blocking this issue (upstream blockers)
 * - All issues transitively blocked by this issue (downstream)
 * - The count of blocked descendants
 */

import { useMemo } from 'react';

import type { DependencyEdge } from '@/types';

/**
 * Result of computing a blocked chain for an issue.
 */
export interface BlockedChainResult {
  /** Issue IDs that are blocking this one (upstream) */
  blockers: Set<string>;
  /** Issue IDs that this one blocks (downstream) */
  blockedBy: Set<string>;
  /** Total count of issues transitively blocked by this issue */
  blockedCount: number;
}

/**
 * Maximum depth to traverse to prevent infinite loops in circular dependencies.
 */
const MAX_DEPTH = 20;

/**
 * Compute all transitive blockers for an issue (issues that must complete first).
 * Traverses upstream through blocking edges.
 *
 * @param issueId - The issue to find blockers for
 * @param edgeMap - Map of target issue ID to edges pointing at it
 * @param visited - Set to track visited nodes and prevent cycles
 * @param depth - Current traversal depth
 * @returns Set of issue IDs that block this issue
 */
function computeBlockersRecursive(
  issueId: string,
  edgeMap: Map<string, DependencyEdge[]>,
  visited: Set<string>,
  depth: number
): Set<string> {
  if (depth > MAX_DEPTH || visited.has(issueId)) {
    return new Set();
  }
  visited.add(issueId);

  const blockers = new Set<string>();
  const incomingEdges = edgeMap.get(issueId) ?? [];

  for (const edge of incomingEdges) {
    if (!edge.data?.isBlocking) continue;

    const blockerId = edge.data.targetIssueId;
    if (blockerId && !visited.has(blockerId)) {
      blockers.add(blockerId);
      // Recursively find transitive blockers
      const transitiveBlockers = computeBlockersRecursive(
        blockerId,
        edgeMap,
        new Set(visited),
        depth + 1
      );
      for (const id of transitiveBlockers) {
        blockers.add(id);
      }
    }
  }

  return blockers;
}

/**
 * Compute all issues transitively blocked by this issue (downstream).
 * Traverses downstream through blocking edges.
 *
 * @param issueId - The issue to find blocked issues for
 * @param reverseEdgeMap - Map of target issue ID to edges where this issue is the target
 * @param visited - Set to track visited nodes and prevent cycles
 * @param depth - Current traversal depth
 * @returns Set of issue IDs blocked by this issue
 */
function computeBlockedByRecursive(
  issueId: string,
  reverseEdgeMap: Map<string, DependencyEdge[]>,
  visited: Set<string>,
  depth: number
): Set<string> {
  if (depth > MAX_DEPTH || visited.has(issueId)) {
    return new Set();
  }
  visited.add(issueId);

  const blockedBy = new Set<string>();
  const outgoingEdges = reverseEdgeMap.get(issueId) ?? [];

  for (const edge of outgoingEdges) {
    if (!edge.data?.isBlocking) continue;

    const blockedId = edge.data.sourceIssueId;
    if (blockedId && !visited.has(blockedId)) {
      blockedBy.add(blockedId);
      // Recursively find transitively blocked
      const transitiveBlocked = computeBlockedByRecursive(
        blockedId,
        reverseEdgeMap,
        new Set(visited),
        depth + 1
      );
      for (const id of transitiveBlocked) {
        blockedBy.add(id);
      }
    }
  }

  return blockedBy;
}

/**
 * Build edge maps for efficient traversal.
 * - edgeMap: target issue ID -> edges where this issue depends on the target
 * - reverseEdgeMap: target issue ID -> edges where this issue IS the target (is depended upon)
 */
function buildEdgeMaps(edges: DependencyEdge[]): {
  edgeMap: Map<string, DependencyEdge[]>;
  reverseEdgeMap: Map<string, DependencyEdge[]>;
} {
  const edgeMap = new Map<string, DependencyEdge[]>();
  const reverseEdgeMap = new Map<string, DependencyEdge[]>();

  for (const edge of edges) {
    const sourceId = edge.data?.sourceIssueId;
    const targetId = edge.data?.targetIssueId;

    if (!sourceId || !targetId) continue;

    // edgeMap: keyed by source (the issue that has dependencies)
    // Used to find blockers: given an issue, find edges where it's the source
    const existingSource = edgeMap.get(sourceId);
    if (existingSource) {
      existingSource.push(edge);
    } else {
      edgeMap.set(sourceId, [edge]);
    }

    // reverseEdgeMap: keyed by target (the issue being depended upon)
    // Used to find blocked issues: given an issue, find edges where it's the target
    const existingTarget = reverseEdgeMap.get(targetId);
    if (existingTarget) {
      existingTarget.push(edge);
    } else {
      reverseEdgeMap.set(targetId, [edge]);
    }
  }

  return { edgeMap, reverseEdgeMap };
}

/**
 * Compute the full blocking chain for a single issue.
 *
 * @param issueId - The issue to compute the chain for
 * @param edges - All dependency edges in the graph
 * @returns BlockedChainResult with blockers, blockedBy, and blockedCount
 */
export function getBlockedChain(issueId: string, edges: DependencyEdge[]): BlockedChainResult {
  const { edgeMap, reverseEdgeMap } = buildEdgeMaps(edges);

  const blockers = computeBlockersRecursive(issueId, edgeMap, new Set(), 0);
  const blockedBy = computeBlockedByRecursive(issueId, reverseEdgeMap, new Set(), 0);

  return {
    blockers,
    blockedBy,
    blockedCount: blockedBy.size,
  };
}

/**
 * Compute blocked counts for all issues in the graph.
 * Returns a map of issue ID -> count of issues it transitively blocks.
 *
 * @param issueIds - All issue IDs in the graph
 * @param edges - All dependency edges in the graph
 * @returns Map of issue ID to blocked count
 */
export function computeAllBlockedCounts(
  issueIds: string[],
  edges: DependencyEdge[]
): Map<string, number> {
  const { reverseEdgeMap } = buildEdgeMaps(edges);
  const counts = new Map<string, number>();

  for (const issueId of issueIds) {
    const blockedBy = computeBlockedByRecursive(issueId, reverseEdgeMap, new Set(), 0);
    counts.set(issueId, blockedBy.size);
  }

  return counts;
}

/**
 * Hook options for useBlockedChain.
 */
export interface UseBlockedChainOptions {
  /** All edges in the graph */
  edges: DependencyEdge[];
  /** Whether the hook is enabled */
  enabled?: boolean;
}

/**
 * Hook return type for useBlockedChain.
 */
export interface UseBlockedChainReturn {
  /** Get the blocked chain for a specific issue */
  getChain: (issueId: string) => BlockedChainResult;
  /** Get the set of all issue IDs in a chain (blockers + blocked + the issue itself) */
  getChainIds: (issueId: string) => Set<string>;
}

/**
 * Hook for computing blocking chains on demand.
 *
 * @example
 * ```tsx
 * function GraphView({ edges }) {
 *   const { getChain, getChainIds } = useBlockedChain({ edges });
 *
 *   const handleNodeHover = (issueId) => {
 *     const chainIds = getChainIds(issueId);
 *     // Highlight all nodes in the chain
 *   };
 * }
 * ```
 */
export function useBlockedChain({
  edges,
  enabled = true,
}: UseBlockedChainOptions): UseBlockedChainReturn {
  // Memoize edge maps for efficient lookups
  const { edgeMap, reverseEdgeMap } = useMemo(
    () => (enabled ? buildEdgeMaps(edges) : { edgeMap: new Map(), reverseEdgeMap: new Map() }),
    [edges, enabled]
  );

  const getChain = useMemo(
    () =>
      (issueId: string): BlockedChainResult => {
        if (!enabled) {
          return { blockers: new Set(), blockedBy: new Set(), blockedCount: 0 };
        }

        const blockers = computeBlockersRecursive(issueId, edgeMap, new Set(), 0);
        const blockedBy = computeBlockedByRecursive(issueId, reverseEdgeMap, new Set(), 0);

        return {
          blockers,
          blockedBy,
          blockedCount: blockedBy.size,
        };
      },
    [edgeMap, reverseEdgeMap, enabled]
  );

  const getChainIds = useMemo(
    () =>
      (issueId: string): Set<string> => {
        const chain = getChain(issueId);
        const ids = new Set<string>([issueId]);
        for (const id of chain.blockers) ids.add(id);
        for (const id of chain.blockedBy) ids.add(id);
        return ids;
      },
    [getChain]
  );

  return { getChain, getChainIds };
}
