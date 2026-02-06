/**
 * GraphView container component for dependency graph visualization.
 *
 * Composes React Flow with useGraphData and useAutoLayout hooks to render
 * issues as nodes and dependencies as edges in an interactive DAG layout.
 */

import { ReactFlow, Background, MiniMap, Panel, type NodeMouseHandler } from '@xyflow/react';
import { useState, useMemo, useCallback, useEffect } from 'react';

import '@xyflow/react/dist/style.css';
import { IssueNode, DependencyEdge, GraphControls, GraphLegend, NodeTooltip } from '@/components';
import type { DependencyTypeGroup } from '@/components/GraphControls';
import type { TooltipPosition } from '@/components/NodeTooltip';
import { useAutoLayout, type UseAutoLayoutOptions } from '@/hooks/useAutoLayout';
import { useBlockedIssues } from '@/hooks/useBlockedIssues';
import { useGraphData, type UseGraphDataOptions } from '@/hooks/useGraphData';
import type { Issue, IssueNode as IssueNodeType, DependencyType } from '@/types';
import type { Status } from '@/types/status';

import styles from './GraphView.module.css';

const STORAGE_KEY_SHOW_CLOSED = 'graph-show-closed';
const STORAGE_KEY_STATUS_FILTER = 'graph-status-filter';
const STORAGE_KEY_DEP_TYPE_FILTER = 'graph-dep-type-filter';

/**
 * Dependency types for each filter group.
 */
const BLOCKING_DEP_TYPES: DependencyType[] = ['blocks', 'conditional-blocks', 'waits-for'];
const PARENT_CHILD_DEP_TYPES: DependencyType[] = ['parent-child'];
const NON_BLOCKING_DEP_TYPES: DependencyType[] = [
  'related',
  'discovered-from',
  'replies-to',
  'relates-to',
  'duplicates',
  'supersedes',
  'authored-by',
  'assigned-to',
  'approved-by',
  'attests',
  'tracks',
  'until',
  'caused-by',
  'validates',
  'delegated-from',
];

/**
 * Valid dependency type groups for localStorage validation.
 */
const VALID_DEP_TYPE_GROUPS: DependencyTypeGroup[] = ['blocking', 'parent-child', 'non-blocking'];

/**
 * Default dependency type filter (blocking + parent-child enabled).
 */
const DEFAULT_DEP_TYPE_FILTER = new Set<DependencyTypeGroup>(['blocking', 'parent-child']);

/**
 * Convert a Set of dependency type groups to an array of DependencyType values.
 * If all groups are unchecked, returns undefined to show all edges.
 */
function depTypeGroupsToTypes(groups: Set<DependencyTypeGroup>): DependencyType[] | undefined {
  // If no groups selected, show all edges (no filter)
  if (groups.size === 0) {
    return undefined;
  }

  const types: DependencyType[] = [];
  if (groups.has('blocking')) {
    types.push(...BLOCKING_DEP_TYPES);
  }
  if (groups.has('parent-child')) {
    types.push(...PARENT_CHILD_DEP_TYPES);
  }
  if (groups.has('non-blocking')) {
    types.push(...NON_BLOCKING_DEP_TYPES);
  }
  return types;
}

/**
 * Valid status filter values for localStorage.
 * Order matches USER_SELECTABLE_STATUSES.
 */
const VALID_STATUS_FILTERS: readonly (Status | 'all')[] = [
  'all',
  'open',
  'in_progress',
  'blocked',
  'deferred',
  'closed',
] as const;

// Register custom node and edge types
const nodeTypes = {
  issue: IssueNode,
} as const;

const edgeTypes = {
  dependency: DependencyEdge,
} as const;

/**
 * Props for the GraphView component.
 */
export interface GraphViewProps {
  /** Issues to display in the graph */
  issues: Issue[];
  /** Callback when a node is clicked */
  onNodeClick?: (issue: Issue) => void;
  /** Callback when a node is hovered */
  onNodeMouseEnter?: (issue: Issue, event: React.MouseEvent) => void;
  /** Callback when mouse leaves a node */
  onNodeMouseLeave?: () => void;
  /** Whether nodes can be manually dragged (default: false) */
  nodesDraggable?: boolean;
  /** Layout direction (default: 'LR') */
  layoutDirection?: UseAutoLayoutOptions['direction'];
  /** Whether to show the MiniMap (default: true) */
  showMiniMap?: boolean;
  /** Whether to show the GraphControls (default: true) */
  showControls?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * GraphView renders an interactive dependency graph using React Flow.
 *
 * @example
 * ```tsx
 * function DependencyGraphPage() {
 *   const { issues } = useIssues();
 *
 *   return (
 *     <GraphView
 *       issues={issues}
 *       onNodeClick={(issue) => console.log('Clicked:', issue.id)}
 *     />
 *   );
 * }
 * ```
 */
export function GraphView({
  issues,
  onNodeClick,
  onNodeMouseEnter,
  onNodeMouseLeave,
  nodesDraggable = false,
  layoutDirection = 'LR',
  showMiniMap = true,
  showControls = true,
  className,
}: GraphViewProps): JSX.Element {
  const [highlightReady, setHighlightReady] = useState(false);
  const [showBlockedOnly, setShowBlockedOnly] = useState(false);
  const [legendCollapsed, setLegendCollapsed] = useState(true);

  // Initialize showClosed from localStorage, default to true
  const [showClosed, setShowClosed] = useState(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY_SHOW_CLOSED);
      return stored === null ? true : stored === 'true';
    } catch {
      // Silently fail if localStorage is unavailable (private browsing, quota exceeded)
      return true;
    }
  });

  // Persist showClosed preference to localStorage
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY_SHOW_CLOSED, String(showClosed));
    } catch {
      // Silently fail if localStorage is unavailable (private browsing, quota exceeded)
    }
  }, [showClosed]);

  // Initialize statusFilter from localStorage, default to 'all'
  const [statusFilter, setStatusFilter] = useState<Status | 'all'>(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY_STATUS_FILTER);
      // Validate stored value is a valid status filter
      if (stored && VALID_STATUS_FILTERS.includes(stored as Status | 'all')) {
        return stored as Status | 'all';
      }
      return 'all';
    } catch {
      // Silently fail if localStorage is unavailable (private browsing, quota exceeded)
      return 'all';
    }
  });

  // Persist statusFilter preference to localStorage
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY_STATUS_FILTER, statusFilter);
    } catch {
      // Silently fail if localStorage is unavailable (private browsing, quota exceeded)
    }
  }, [statusFilter]);

  // Initialize dependencyTypeFilter from localStorage, default to blocking + parent-child
  const [dependencyTypeFilter, setDependencyTypeFilter] = useState<Set<DependencyTypeGroup>>(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY_DEP_TYPE_FILTER);
      if (stored) {
        const parsed = JSON.parse(stored);
        // Validate parsed is an array
        if (!Array.isArray(parsed)) {
          return DEFAULT_DEP_TYPE_FILTER;
        }
        // Validate all values are strings and valid groups
        const validGroups = parsed.filter(
          (g): g is DependencyTypeGroup =>
            typeof g === 'string' && VALID_DEP_TYPE_GROUPS.includes(g as DependencyTypeGroup)
        );
        return new Set(validGroups);
      }
      return DEFAULT_DEP_TYPE_FILTER;
    } catch {
      // Silently fail if localStorage is unavailable or invalid JSON
      return DEFAULT_DEP_TYPE_FILTER;
    }
  });

  // Persist dependencyTypeFilter preference to localStorage
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY_DEP_TYPE_FILTER, JSON.stringify([...dependencyTypeFilter]));
    } catch {
      // Silently fail if localStorage is unavailable (private browsing, quota exceeded)
    }
  }, [dependencyTypeFilter]);

  // Convert dependency type filter groups to DependencyType array for useGraphData
  const includeDependencyTypes = useMemo(
    () => depTypeGroupsToTypes(dependencyTypeFilter),
    [dependencyTypeFilter]
  );

  // Tooltip state for hover preview
  const [hoveredIssue, setHoveredIssue] = useState<Issue | null>(null);
  const [tooltipPosition, setTooltipPosition] = useState<TooltipPosition | null>(null);

  // Fetch blocked issues for ready state calculation
  const { data: blockedIssues } = useBlockedIssues({ enabled: true });
  const blockedIssueIds = useMemo(() => {
    if (!blockedIssues) return new Set<string>();
    return new Set(blockedIssues.map((bi) => bi.id));
  }, [blockedIssues]);

  // Filter issues based on statusFilter and showClosed
  // Status filter takes precedence: when a specific status is selected, only show that status
  const visibleIssues = useMemo(() => {
    let filtered = issues;

    // If a specific status is selected, filter to only that status
    if (statusFilter !== 'all') {
      filtered = filtered.filter((issue) => issue.status === statusFilter);
    } else {
      // When 'all' is selected, respect the showClosed toggle
      if (!showClosed) {
        filtered = filtered.filter((issue) => issue.status !== 'closed');
      }
    }

    return filtered;
  }, [issues, statusFilter, showClosed]);

  // Transform issues to nodes and edges
  const graphDataOptions: UseGraphDataOptions = useMemo(() => {
    const opts: UseGraphDataOptions = { blockedIssueIds };
    if (includeDependencyTypes) {
      opts.includeDependencyTypes = includeDependencyTypes;
    }
    return opts;
  }, [blockedIssueIds, includeDependencyTypes]);
  const { nodes: rawNodes, edges } = useGraphData(visibleIssues, graphDataOptions);

  // Apply auto-layout
  const layoutOptions: UseAutoLayoutOptions = useMemo(
    () => ({ direction: layoutDirection }),
    [layoutDirection]
  );
  const { nodes: layoutedNodes } = useAutoLayout(rawNodes, edges, layoutOptions);

  // Handle node click - extract issue from node data
  const handleNodeClick: NodeMouseHandler<IssueNodeType> = useCallback(
    (_event, node) => {
      if (onNodeClick && node.data?.issue) {
        onNodeClick(node.data.issue);
      }
    },
    [onNodeClick]
  );

  // Handle node mouse enter - sets tooltip state and calls external callback
  const handleNodeMouseEnter: NodeMouseHandler<IssueNodeType> = useCallback(
    (event, node) => {
      if (node.data?.issue) {
        // Set tooltip position from mouse coordinates
        const mouseEvent = event as unknown as React.MouseEvent;
        setHoveredIssue(node.data.issue);
        setTooltipPosition({ x: mouseEvent.clientX, y: mouseEvent.clientY });

        // Call external callback if provided
        if (onNodeMouseEnter) {
          onNodeMouseEnter(node.data.issue, event);
        }
      }
    },
    [onNodeMouseEnter]
  );

  // Handle node mouse leave - clears tooltip state and calls external callback
  const handleNodeMouseLeave: NodeMouseHandler<IssueNodeType> = useCallback(() => {
    setHoveredIssue(null);
    setTooltipPosition(null);
    onNodeMouseLeave?.();
  }, [onNodeMouseLeave]);

  const rootClassName = className ? `${styles.graphView} ${className}` : styles.graphView;

  // Build ReactFlow props conditionally to avoid passing undefined
  const reactFlowProps: Record<string, unknown> = {
    nodes: layoutedNodes,
    edges,
    nodeTypes,
    edgeTypes,
    nodesDraggable,
    nodesConnectable: false,
    elementsSelectable: true,
    fitView: true,
    fitViewOptions: { padding: 0.2, maxZoom: 1.5 },
    minZoom: 0.1,
    maxZoom: 2,
    attributionPosition: 'bottom-left',
  };

  if (onNodeClick) {
    reactFlowProps.onNodeClick = handleNodeClick;
  }
  // Always add mouse handlers for tooltip functionality
  reactFlowProps.onNodeMouseEnter = handleNodeMouseEnter;
  reactFlowProps.onNodeMouseLeave = handleNodeMouseLeave;

  // Build MiniMap props conditionally
  const miniMapProps: Record<string, unknown> = {
    maskColor: 'rgba(0, 0, 0, 0.1)',
    position: 'bottom-right',
  };
  if (styles.miniMapNode) {
    miniMapProps.nodeClassName = styles.miniMapNode;
  }

  return (
    <div
      className={rootClassName}
      data-highlight-ready={highlightReady}
      data-show-blocked-only={showBlockedOnly}
      data-show-closed={showClosed}
      data-status-filter={statusFilter}
      data-testid="graph-view"
    >
      <ReactFlow {...(reactFlowProps as Record<string, never>)}>
        <Background gap={16} size={1} />
        {showMiniMap && <MiniMap {...(miniMapProps as Record<string, never>)} />}
        {showControls && (
          <Panel position="top-right">
            <GraphControls
              highlightReady={highlightReady}
              onHighlightReadyChange={setHighlightReady}
              showBlockedOnly={showBlockedOnly}
              onShowBlockedOnlyChange={setShowBlockedOnly}
              showClosed={showClosed}
              onShowClosedChange={setShowClosed}
              statusFilter={statusFilter}
              onStatusFilterChange={setStatusFilter}
              dependencyTypeFilter={dependencyTypeFilter}
              onDependencyTypeFilterChange={setDependencyTypeFilter}
              {...(styles.controls ? { className: styles.controls } : {})}
            />
          </Panel>
        )}
      </ReactFlow>
      <NodeTooltip issue={hoveredIssue} position={tooltipPosition} />
      <GraphLegend
        collapsed={legendCollapsed}
        onToggle={() => setLegendCollapsed(!legendCollapsed)}
        {...(styles.legend ? { className: styles.legend } : {})}
      />
    </div>
  );
}
