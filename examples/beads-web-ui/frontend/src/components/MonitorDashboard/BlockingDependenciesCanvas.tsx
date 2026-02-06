/**
 * BlockingDependenciesCanvas - Rich dependency graph for Monitor Dashboard.
 *
 * Replaces MiniDependencyGraph with richer node cards, dashed red edges,
 * zoom controls, expand button, and a color-coded legend.
 */

import { ReactFlow, Controls, type NodeMouseHandler } from '@xyflow/react';
import { useMemo, useCallback } from 'react';

import '@xyflow/react/dist/style.css';
import { useAutoLayout, type UseAutoLayoutOptions } from '@/hooks/useAutoLayout';
import { useBlockedIssues } from '@/hooks/useBlockedIssues';
import { useGraphData, type UseGraphDataOptions } from '@/hooks/useGraphData';
import type { Issue, IssueNode as IssueNodeType, DependencyType } from '@/types';

import styles from './BlockingDependenciesCanvas.module.css';
import { BlockingEdge } from './BlockingEdge';
import { BlockingNode } from './BlockingNode';

/**
 * Blocking dependency types to display.
 */
const BLOCKING_DEP_TYPES: DependencyType[] = ['blocks', 'conditional-blocks', 'waits-for'];

// Register custom node and edge types
const nodeTypes = {
  issue: BlockingNode,
} as const;

const edgeTypes = {
  dependency: BlockingEdge,
} as const;

/**
 * Props for the BlockingDependenciesCanvas component.
 */
export interface BlockingDependenciesCanvasProps {
  /** Issues to display */
  issues: Issue[];
  /** Callback when a node is clicked */
  onNodeClick?: (issue: Issue) => void;
  /** Callback when expand/fullscreen button is clicked */
  onExpandClick?: () => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * BlockingDependenciesCanvas renders a rich dependency graph with
 * status badges, blocking indicators, and a color-coded legend.
 */
export function BlockingDependenciesCanvas({
  issues,
  onNodeClick,
  onExpandClick,
  className,
}: BlockingDependenciesCanvasProps): JSX.Element {
  // Fetch blocked issues for status calculation
  const { data: blockedIssues } = useBlockedIssues({ enabled: true });

  const blockedIssueIds = useMemo(() => {
    if (!blockedIssues) return new Set<string>();
    return new Set(blockedIssues.map((bi) => bi.id));
  }, [blockedIssues]);

  // Filter to non-closed issues
  const visibleIssues = useMemo(() => {
    return issues.filter((issue) => issue.status !== 'closed');
  }, [issues]);

  // Transform to nodes and edges with blocking deps only
  const graphDataOptions: UseGraphDataOptions = useMemo(
    () => ({
      blockedIssueIds,
      includeDependencyTypes: BLOCKING_DEP_TYPES,
      includeOrphanEdges: true,
    }),
    [blockedIssueIds]
  );

  const { nodes: rawNodes, edges } = useGraphData(visibleIssues, graphDataOptions);

  // Apply auto-layout with compact spacing
  const layoutOptions: UseAutoLayoutOptions = useMemo(
    () => ({
      direction: 'LR',
      nodesep: 40,
      ranksep: 80,
      nodeWidth: 240,
      nodeHeight: 120,
    }),
    []
  );

  const { nodes: layoutedNodes } = useAutoLayout(rawNodes, edges, layoutOptions);

  // Handle node click
  const handleNodeClick: NodeMouseHandler<IssueNodeType> = useCallback(
    (_event, node) => {
      if (onNodeClick && node.data?.issue) {
        onNodeClick(node.data.issue);
      }
    },
    [onNodeClick]
  );

  const rootClassName = className ? `${styles.canvas} ${className}` : styles.canvas;

  const isEmpty = layoutedNodes.length === 0;

  return (
    <div className={rootClassName} data-testid="blocking-dependencies-canvas">
      {/* Expand button */}
      {onExpandClick && (
        <button
          className={styles.expandButton}
          onClick={onExpandClick}
          aria-label="Expand to full graph view"
          title="View full dependency graph"
        >
          <span className={styles.expandIcon}>↗</span>
        </button>
      )}

      {isEmpty ? (
        <div className={styles.emptyState}>
          <span className={styles.emptyIcon}>✓</span>
          <span className={styles.emptyText}>No blocking dependencies</span>
        </div>
      ) : (
        <>
          <ReactFlow
            nodes={layoutedNodes}
            edges={edges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={true}
            fitView
            fitViewOptions={{ padding: 0.15, maxZoom: 1.2 }}
            minZoom={0.2}
            maxZoom={1.5}
            panOnScroll={true}
            zoomOnScroll={true}
            preventScrolling={false}
            proOptions={{ hideAttribution: true }}
            onNodeClick={handleNodeClick}
          >
            <Controls
              showInteractive={false}
              position="bottom-right"
              className={styles.controls ?? ''}
            />
          </ReactFlow>

          {/* Color-coded legend */}
          <div className={styles.legend} aria-label="Graph legend">
            <div className={styles.legendItem}>
              <span className={`${styles.legendDot} ${styles.legendHealthy}`} />
              <span className={styles.legendLabel}>Healthy</span>
            </div>
            <div className={styles.legendItem}>
              <span className={`${styles.legendDot} ${styles.legendBlocking}`} />
              <span className={styles.legendLabel}>Blocking</span>
            </div>
            <div className={styles.legendItem}>
              <span className={`${styles.legendDot} ${styles.legendBlocked}`} />
              <span className={styles.legendLabel}>Blocked</span>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
