/**
 * React hook for auto-layout of graph nodes using dagre.
 *
 * This hook takes React Flow nodes and edges from useGraphData and
 * calculates optimal positions for a hierarchical DAG layout.
 * It produces a clean, readable visualization where parent nodes
 * flow into child nodes in a consistent direction.
 */

import { useMemo } from 'react'
import dagre from '@dagrejs/dagre'
import { Position } from '@xyflow/react'
import type { IssueNode, DependencyEdge } from '@/types'

/**
 * Layout direction for the graph.
 * TB = top-to-bottom, BT = bottom-to-top, LR = left-to-right, RL = right-to-left.
 */
export type LayoutDirection = 'TB' | 'BT' | 'LR' | 'RL'

/**
 * Alignment within ranks (rows/columns).
 * UL = upper left, UR = upper right, DL = down left, DR = down right.
 */
export type RankAlignment = 'UL' | 'UR' | 'DL' | 'DR' | undefined

/**
 * Default node dimensions for layout calculation.
 * These match the IssueNode component styling.
 */
const DEFAULT_NODE_WIDTH = 200
const DEFAULT_NODE_HEIGHT = 100

/**
 * Default spacing between nodes.
 */
const DEFAULT_NODESEP = 50
const DEFAULT_RANKSEP = 100

/**
 * Graph margin for padding around the layout.
 */
const DEFAULT_MARGIN = 20

/**
 * Options for the useAutoLayout hook.
 */
export interface UseAutoLayoutOptions {
  /** Layout direction: TB (top-bottom), LR (left-right), etc. Default: 'TB' */
  direction?: LayoutDirection
  /** Horizontal pixel spacing between nodes. Default: 50 */
  nodesep?: number
  /** Vertical pixel spacing between ranks. Default: 100 */
  ranksep?: number
  /** Alignment for rank nodes. Default: undefined (center) */
  align?: RankAlignment
  /** Node width for layout calculation. Default: 200 */
  nodeWidth?: number
  /** Node height for layout calculation. Default: 100 */
  nodeHeight?: number
}

/**
 * Return type for the useAutoLayout hook.
 */
export interface UseAutoLayoutReturn {
  /** Nodes with calculated positions */
  nodes: IssueNode[]
  /** Bounding box of the laid out graph */
  bounds: { width: number; height: number }
}

/**
 * Determine source/target handle positions based on layout direction.
 */
function getHandlePositions(direction: LayoutDirection): {
  sourcePosition: Position
  targetPosition: Position
} {
  switch (direction) {
    case 'TB':
      return { sourcePosition: Position.Bottom, targetPosition: Position.Top }
    case 'BT':
      return { sourcePosition: Position.Top, targetPosition: Position.Bottom }
    case 'LR':
      return { sourcePosition: Position.Right, targetPosition: Position.Left }
    case 'RL':
      return { sourcePosition: Position.Left, targetPosition: Position.Right }
    default:
      return { sourcePosition: Position.Bottom, targetPosition: Position.Top }
  }
}

/**
 * Internal type for resolved options (all required).
 */
interface ResolvedOptions {
  direction: LayoutDirection
  nodesep: number
  ranksep: number
  align: RankAlignment
  nodeWidth: number
  nodeHeight: number
}

/**
 * Calculate node positions using dagre layout algorithm.
 */
function getLayoutedNodes(
  nodes: IssueNode[],
  edges: DependencyEdge[],
  options: ResolvedOptions
): { nodes: IssueNode[]; bounds: { width: number; height: number } } {
  const { direction, nodesep, ranksep, align, nodeWidth, nodeHeight } = options

  // Create dagre graph
  const g = new dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}))

  // Configure graph
  g.setGraph({
    rankdir: direction,
    nodesep,
    ranksep,
    align,
    marginx: DEFAULT_MARGIN,
    marginy: DEFAULT_MARGIN,
  })

  // Add nodes to dagre and build a set for edge validation
  const nodeIds = new Set<string>()
  for (const node of nodes) {
    g.setNode(node.id, { width: nodeWidth, height: nodeHeight })
    nodeIds.add(node.id)
  }

  // Add edges to dagre (only if both endpoints exist)
  for (const edge of edges) {
    if (nodeIds.has(edge.source) && nodeIds.has(edge.target)) {
      g.setEdge(edge.source, edge.target)
    }
  }

  // Calculate layout
  dagre.layout(g)

  // Get handle positions for this direction
  const { sourcePosition, targetPosition } = getHandlePositions(direction)

  // Map positions back to React Flow nodes (convert center → top-left anchor)
  const positionedNodes = nodes.map((node) => {
    const nodeWithPosition = g.node(node.id)
    return {
      ...node,
      sourcePosition,
      targetPosition,
      position: {
        x: nodeWithPosition.x - nodeWidth / 2,
        y: nodeWithPosition.y - nodeHeight / 2,
      },
    }
  })

  // Calculate bounds from graph
  const graph = g.graph()
  const bounds = {
    width: graph.width ?? 0,
    height: graph.height ?? 0,
  }

  return { nodes: positionedNodes, bounds }
}

/**
 * React hook for applying dagre auto-layout to graph nodes.
 *
 * Takes nodes and edges from useGraphData and calculates optimal positions
 * for a hierarchical DAG layout. The hook memoizes the layout calculation
 * to avoid expensive recalculations when inputs haven't changed.
 *
 * @example
 * ```tsx
 * function DependencyGraph() {
 *   const { issues } = useIssues()
 *   const { nodes, edges } = useGraphData(issues)
 *   const { nodes: layoutedNodes, bounds } = useAutoLayout(nodes, edges)
 *
 *   return (
 *     <ReactFlow nodes={layoutedNodes} edges={edges}>
 *       <Background />
 *       <Controls />
 *     </ReactFlow>
 *   )
 * }
 * ```
 *
 * @param nodes - React Flow nodes from useGraphData
 * @param edges - React Flow edges from useGraphData
 * @param options - Layout configuration options
 * @returns Positioned nodes and graph bounds
 */
export function useAutoLayout(
  nodes: IssueNode[],
  edges: DependencyEdge[],
  options: UseAutoLayoutOptions = {}
): UseAutoLayoutReturn {
  const {
    direction = 'TB',
    nodesep = DEFAULT_NODESEP,
    ranksep = DEFAULT_RANKSEP,
    align,
    nodeWidth = DEFAULT_NODE_WIDTH,
    nodeHeight = DEFAULT_NODE_HEIGHT,
  } = options

  return useMemo(() => {
    if (nodes.length === 0) {
      return { nodes: [], bounds: { width: 0, height: 0 } }
    }

    return getLayoutedNodes(nodes, edges, {
      direction,
      nodesep,
      ranksep,
      align,
      nodeWidth,
      nodeHeight,
    })
  }, [nodes, edges, direction, nodesep, ranksep, align, nodeWidth, nodeHeight])
}
