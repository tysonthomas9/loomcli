/**
 * GraphViewContainer component.
 * Wraps GraphView with IssueDetailPanel integration for node click handling.
 */

import { useState, useCallback } from 'react';
import type { Issue } from '@/types';
import { IssueDetailPanel } from '@/components';
import { GraphView, type GraphViewProps } from '@/components/GraphView';
import { useIssueDetail } from '@/hooks/useIssueDetail';
import styles from './GraphViewContainer.module.css';

/**
 * Props for the GraphViewContainer component.
 */
export interface GraphViewContainerProps {
  /** Issues to display in the graph */
  issues: Issue[];
  /** Additional CSS class name */
  className?: string;
  /** Whether nodes can be manually dragged (default: false) */
  nodesDraggable?: GraphViewProps['nodesDraggable'];
  /** Layout direction (default: 'LR') */
  layoutDirection?: GraphViewProps['layoutDirection'];
  /** Whether to show the MiniMap (default: true) */
  showMiniMap?: GraphViewProps['showMiniMap'];
  /** Whether to show the GraphControls (default: true) */
  showControls?: GraphViewProps['showControls'];
}

/**
 * GraphViewContainer wraps GraphView with IssueDetailPanel integration.
 *
 * Features:
 * - Click a node to open the detail panel with full issue information
 * - Fetches full IssueDetails (with dependents) on click
 * - Shows loading state while fetching
 * - Handles fetch errors gracefully
 *
 * @example
 * ```tsx
 * function DependencyGraphPage() {
 *   const { issues } = useIssues();
 *
 *   return <GraphViewContainer issues={issues} />;
 * }
 * ```
 */
export function GraphViewContainer({
  issues,
  className,
  nodesDraggable,
  layoutDirection,
  showMiniMap,
  showControls,
}: GraphViewContainerProps): JSX.Element {
  const [isPanelOpen, setIsPanelOpen] = useState(false);
  const [selectedIssueId, setSelectedIssueId] = useState<string | null>(null);
  const { issueDetails, isLoading, error, fetchIssue, clearIssue } = useIssueDetail();

  // Handle node click - fetch full details and open panel
  const handleNodeClick = useCallback(
    (issue: Issue) => {
      // If clicking the same issue that's already selected, just ensure panel is open
      if (issue.id === selectedIssueId && isPanelOpen) {
        return;
      }

      setSelectedIssueId(issue.id);
      setIsPanelOpen(true);
      fetchIssue(issue.id);
    },
    [selectedIssueId, isPanelOpen, fetchIssue]
  );

  // Handle panel close
  const handleClose = useCallback(() => {
    setIsPanelOpen(false);
    // Clear issue details after animation completes
    setTimeout(() => {
      clearIssue();
      setSelectedIssueId(null);
    }, 300); // Match CSS transition duration
  }, [clearIssue]);

  const rootClassName = className
    ? `${styles.container} ${className}`
    : styles.container;

  // Build GraphView props, only including optional props when defined
  const graphViewProps: GraphViewProps = {
    issues,
    onNodeClick: handleNodeClick,
  };
  if (nodesDraggable !== undefined) graphViewProps.nodesDraggable = nodesDraggable;
  if (layoutDirection !== undefined) graphViewProps.layoutDirection = layoutDirection;
  if (showMiniMap !== undefined) graphViewProps.showMiniMap = showMiniMap;
  if (showControls !== undefined) graphViewProps.showControls = showControls;

  return (
    <div className={rootClassName}>
      <GraphView {...graphViewProps} />
      <IssueDetailPanel
        isOpen={isPanelOpen}
        issue={issueDetails}
        isLoading={isLoading}
        error={error}
        onClose={handleClose}
      />
    </div>
  );
}
