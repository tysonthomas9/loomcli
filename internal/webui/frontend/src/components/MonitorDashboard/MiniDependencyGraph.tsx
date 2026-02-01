/**
 * MiniDependencyGraph - Compact dependency graph for Monitor Dashboard.
 *
 * Shows only blocking relationships in a simplified React Flow view.
 * Designed to fit within the MonitorDashboard's bottom-right panel.
 */

import { useMemo, useCallback } from 'react';
import {
  ReactFlow,
  type NodeMouseHandler,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { Issue, IssueNode as IssueNodeType, DependencyType } from '@/types';
import { useGraphData, type UseGraphDataOptions } from '@/hooks/useGraphData';
import { useAutoLayout, type UseAutoLayoutOptions } from '@/hooks/useAutoLayout';
import { useBlockedIssues } from '@/hooks/useBlockedIssues';
import { IssueNode, DependencyEdge } from '@/components';
import styles from './MiniDependencyGraph.module.css';

/**
 * Blocking dependency types to display.
 * Same as GraphView's BLOCKING_DEP_TYPES.
 */
const BLOCKING_DEP_TYPES: DependencyType[] = [
  'blocks',
  'conditional-blocks',
  'waits-for',
];

// Register custom node and edge types
const nodeTypes = {
  issue: IssueNode,
} as const;

const edgeTypes = {
  dependency: DependencyEdge,
} as const;

/**
 * Props for the MiniDependencyGraph component.
 */
export interface MiniDependencyGraphProps {
  /** Issues to display (should be filtered to relevant subset) */
  issues: Issue[];
  /** Callback when a node is clicked (opens IssueDetailPanel) */
  onNodeClick?: (issue: Issue) => void;
  /** Callback when "Expand" button is clicked (navigate to Graph view) */
  onExpandClick?: () => void;
  /** Layout direction (default: 'LR' for left-to-right) */
  layoutDirection?: 'TB' | 'LR';
  /** Additional CSS class name */
  className?: string;
}

/**
 * MiniDependencyGraph renders a simplified dependency graph.
 * Shows only blocking relationships with root blockers highlighted.
 */
export function MiniDependencyGraph({
  issues,
  onNodeClick,
  onExpandClick,
  layoutDirection = 'LR',
  className,
}: MiniDependencyGraphProps): JSX.Element {
  // Fetch blocked issues for root blocker calculation
  const { data: blockedIssues } = useBlockedIssues({ enabled: true });

  const blockedIssueIds = useMemo(() => {
    if (!blockedIssues) return new Set<string>();
    return new Set(blockedIssues.map((bi) => bi.id));
  }, [blockedIssues]);

  // Filter to only non-closed issues for cleaner visualization
  const visibleIssues = useMemo(() => {
    return issues.filter((issue) => issue.status !== 'closed');
  }, [issues]);

  // Transform to nodes and edges with blocking deps only
  const graphDataOptions: UseGraphDataOptions = useMemo(
    () => ({
      blockedIssueIds,
      includeDependencyTypes: BLOCKING_DEP_TYPES,
    }),
    [blockedIssueIds]
  );

  const { nodes: rawNodes, edges } = useGraphData(visibleIssues, graphDataOptions);

  // Apply auto-layout with compact spacing for mini view
  const layoutOptions: UseAutoLayoutOptions = useMemo(
    () => ({
      direction: layoutDirection,
      nodesep: 30, // Tighter spacing for mini view
      ranksep: 60, // Tighter rank spacing
    }),
    [layoutDirection]
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

  const rootClassName = className
    ? `${styles.miniGraph} ${className}`
    : styles.miniGraph;

  const isEmpty = layoutedNodes.length === 0;

  return (
    <div className={rootClassName} data-testid="mini-dependency-graph">
      {/* Expand button in top-right corner */}
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
          minZoom={0.3}
          maxZoom={1.5}
          panOnScroll={true}
          zoomOnScroll={true}
          preventScrolling={false}
          proOptions={{ hideAttribution: true }}
          onNodeClick={handleNodeClick}
        />
      )}
    </div>
  );
}
