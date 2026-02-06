/**
 * @vitest-environment jsdom
 */
import { renderHook } from '@testing-library/react';
import { Position } from '@xyflow/react';
import { describe, it, expect } from 'vitest';

import type { IssueNode, DependencyEdge, Issue, DependencyType } from '@/types';

import { useAutoLayout } from './useAutoLayout';

/**
 * Helper to create a test issue node with required fields.
 */
function createTestNode(id: string, overrides: Partial<IssueNode> = {}): IssueNode {
  const issue: Issue = {
    id,
    title: `Issue ${id}`,
    priority: 2,
    created_at: '2025-01-23T10:00:00Z',
    updated_at: '2025-01-23T10:00:00Z',
  };

  return {
    id: `node-${id}`,
    type: 'issue',
    position: { x: 0, y: 0 },
    data: {
      issue,
      title: issue.title,
      status: undefined,
      priority: issue.priority,
      issueType: undefined,
      dependencyCount: 0,
      dependentCount: 0,
    },
    ...overrides,
  };
}

/**
 * Helper to create a dependency edge between two nodes.
 */
function createTestEdge(
  sourceId: string,
  targetId: string,
  type: DependencyType = 'blocks'
): DependencyEdge {
  return {
    id: `edge-${sourceId}-${targetId}-${type}`,
    source: `node-${sourceId}`,
    target: `node-${targetId}`,
    type: 'dependency',
    data: {
      dependencyType: type,
      isBlocking: type === 'blocks',
      sourceIssueId: sourceId,
      targetIssueId: targetId,
    },
  };
}

describe('useAutoLayout', () => {
  describe('Empty input', () => {
    it('returns empty nodes array and zero bounds for empty inputs', () => {
      const { result } = renderHook(() => useAutoLayout([], []));

      expect(result.current.nodes).toEqual([]);
      expect(result.current.bounds).toEqual({ width: 0, height: 0 });
    });

    it('returns expected shape with all properties', () => {
      const { result } = renderHook(() => useAutoLayout([], []));

      expect(result.current).toHaveProperty('nodes');
      expect(result.current).toHaveProperty('bounds');
    });
  });

  describe('Single node', () => {
    it('positions single node with positive x/y (after margins)', () => {
      const nodes = [createTestNode('A')];
      const { result } = renderHook(() => useAutoLayout(nodes, []));

      expect(result.current.nodes).toHaveLength(1);
      const positionedNode = result.current.nodes[0];

      // Node should have positive position after margin is applied
      expect(positionedNode.position.x).toBeGreaterThan(0);
      expect(positionedNode.position.y).toBeGreaterThan(0);
    });

    it('preserves original node data', () => {
      const nodes = [createTestNode('A')];
      const { result } = renderHook(() => useAutoLayout(nodes, []));

      const positionedNode = result.current.nodes[0];
      expect(positionedNode.id).toBe('node-A');
      expect(positionedNode.type).toBe('issue');
      expect(positionedNode.data.title).toBe('Issue A');
    });

    it('returns non-zero bounds for single node', () => {
      const nodes = [createTestNode('A')];
      const { result } = renderHook(() => useAutoLayout(nodes, []));

      expect(result.current.bounds.width).toBeGreaterThan(0);
      expect(result.current.bounds.height).toBeGreaterThan(0);
    });
  });

  describe('Linear chain TB (top-to-bottom)', () => {
    it('positions A -> B -> C with increasing y values', () => {
      const nodes = [createTestNode('A'), createTestNode('B'), createTestNode('C')];
      const edges = [createTestEdge('A', 'B'), createTestEdge('B', 'C')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'TB' }));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      // In TB direction, y should increase down the chain
      expect(nodeA?.position.y).toBeLessThan(nodeB?.position.y ?? 0);
      expect(nodeB?.position.y).toBeLessThan(nodeC?.position.y ?? 0);
    });

    it('positions A -> B -> C with similar x values (aligned vertically)', () => {
      const nodes = [createTestNode('A'), createTestNode('B'), createTestNode('C')];
      const edges = [createTestEdge('A', 'B'), createTestEdge('B', 'C')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'TB' }));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      // In a linear chain, x values should be the same (vertically aligned)
      expect(nodeA?.position.x).toBe(nodeB?.position.x);
      expect(nodeB?.position.x).toBe(nodeC?.position.x);
    });
  });

  describe('Linear chain LR (left-to-right)', () => {
    it('positions A -> B -> C with increasing x values', () => {
      const nodes = [createTestNode('A'), createTestNode('B'), createTestNode('C')];
      const edges = [createTestEdge('A', 'B'), createTestEdge('B', 'C')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'LR' }));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      // In LR direction, x should increase along the chain
      expect(nodeA?.position.x).toBeLessThan(nodeB?.position.x ?? 0);
      expect(nodeB?.position.x).toBeLessThan(nodeC?.position.x ?? 0);
    });

    it('positions A -> B -> C with similar y values (aligned horizontally)', () => {
      const nodes = [createTestNode('A'), createTestNode('B'), createTestNode('C')];
      const edges = [createTestEdge('A', 'B'), createTestEdge('B', 'C')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'LR' }));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      // In a linear chain, y values should be the same (horizontally aligned)
      expect(nodeA?.position.y).toBe(nodeB?.position.y);
      expect(nodeB?.position.y).toBe(nodeC?.position.y);
    });
  });

  describe('Handle positions', () => {
    it('TB direction sets sourcePosition=Bottom, targetPosition=Top', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'TB' }));

      for (const node of result.current.nodes) {
        expect(node.sourcePosition).toBe(Position.Bottom);
        expect(node.targetPosition).toBe(Position.Top);
      }
    });

    it('BT direction sets sourcePosition=Top, targetPosition=Bottom', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'BT' }));

      for (const node of result.current.nodes) {
        expect(node.sourcePosition).toBe(Position.Top);
        expect(node.targetPosition).toBe(Position.Bottom);
      }
    });

    it('LR direction sets sourcePosition=Right, targetPosition=Left', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'LR' }));

      for (const node of result.current.nodes) {
        expect(node.sourcePosition).toBe(Position.Right);
        expect(node.targetPosition).toBe(Position.Left);
      }
    });

    it('RL direction sets sourcePosition=Left, targetPosition=Right', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'RL' }));

      for (const node of result.current.nodes) {
        expect(node.sourcePosition).toBe(Position.Left);
        expect(node.targetPosition).toBe(Position.Right);
      }
    });

    it('defaults to TB handle positions when no direction specified', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges));

      for (const node of result.current.nodes) {
        expect(node.sourcePosition).toBe(Position.Bottom);
        expect(node.targetPosition).toBe(Position.Top);
      }
    });
  });

  describe('Custom spacing', () => {
    it('nodesep option affects horizontal spacing between parallel nodes', () => {
      // Create two nodes at the same rank (both depend on a common root)
      const nodes = [createTestNode('Root'), createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('Root', 'A'), createTestEdge('Root', 'B')];

      const { result: smallSep } = renderHook(() => useAutoLayout(nodes, edges, { nodesep: 50 }));
      const { result: largeSep } = renderHook(() => useAutoLayout(nodes, edges, { nodesep: 200 }));

      // Find nodes A and B in both results
      const smallA = smallSep.current.nodes.find((n) => n.id === 'node-A');
      const smallB = smallSep.current.nodes.find((n) => n.id === 'node-B');
      const largeA = largeSep.current.nodes.find((n) => n.id === 'node-A');
      const largeB = largeSep.current.nodes.find((n) => n.id === 'node-B');

      // Horizontal distance between A and B should be larger with larger nodesep
      const smallDistance = Math.abs((smallA?.position.x ?? 0) - (smallB?.position.x ?? 0));
      const largeDistance = Math.abs((largeA?.position.x ?? 0) - (largeB?.position.x ?? 0));

      expect(largeDistance).toBeGreaterThan(smallDistance);
    });

    it('ranksep option affects vertical spacing between ranks', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result: smallSep } = renderHook(() => useAutoLayout(nodes, edges, { ranksep: 50 }));
      const { result: largeSep } = renderHook(() => useAutoLayout(nodes, edges, { ranksep: 300 }));

      const smallA = smallSep.current.nodes.find((n) => n.id === 'node-A');
      const smallB = smallSep.current.nodes.find((n) => n.id === 'node-B');
      const largeA = largeSep.current.nodes.find((n) => n.id === 'node-A');
      const largeB = largeSep.current.nodes.find((n) => n.id === 'node-B');

      // Vertical distance between A and B should be larger with larger ranksep
      const smallDistance = Math.abs((smallA?.position.y ?? 0) - (smallB?.position.y ?? 0));
      const largeDistance = Math.abs((largeA?.position.y ?? 0) - (largeB?.position.y ?? 0));

      expect(largeDistance).toBeGreaterThan(smallDistance);
    });

    it('nodeWidth and nodeHeight options affect layout calculations', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result: smallNodes } = renderHook(() =>
        useAutoLayout(nodes, edges, { nodeWidth: 100, nodeHeight: 50 })
      );
      const { result: largeNodes } = renderHook(() =>
        useAutoLayout(nodes, edges, { nodeWidth: 400, nodeHeight: 200 })
      );

      // Larger nodes should result in larger bounds
      expect(largeNodes.current.bounds.width).toBeGreaterThan(smallNodes.current.bounds.width);
      expect(largeNodes.current.bounds.height).toBeGreaterThan(smallNodes.current.bounds.height);
    });
  });

  describe('Memoization', () => {
    it('returns same object reference for same input (referential equality)', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result, rerender } = renderHook(({ n, e }) => useAutoLayout(n, e), {
        initialProps: { n: nodes, e: edges },
      });

      const firstResult = result.current;

      // Rerender with same array references
      rerender({ n: nodes, e: edges });

      expect(result.current).toBe(firstResult);
      expect(result.current.nodes).toBe(firstResult.nodes);
      expect(result.current.bounds).toBe(firstResult.bounds);
    });

    it('returns new object reference when nodes array changes', () => {
      const nodes1 = [createTestNode('A')];
      const nodes2 = [createTestNode('B')];
      const edges: DependencyEdge[] = [];

      const { result, rerender } = renderHook(({ nodes, edges: e }) => useAutoLayout(nodes, e), {
        initialProps: { nodes: nodes1, edges },
      });

      const firstResult = result.current;

      rerender({ nodes: nodes2, edges });

      expect(result.current).not.toBe(firstResult);
    });

    it('returns new object reference when edges array changes', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges1 = [createTestEdge('A', 'B')];
      const edges2 = [createTestEdge('A', 'B', 'related')];

      const { result, rerender } = renderHook(({ n, edges }) => useAutoLayout(n, edges), {
        initialProps: { n: nodes, edges: edges1 },
      });

      const firstResult = result.current;

      rerender({ n: nodes, edges: edges2 });

      expect(result.current).not.toBe(firstResult);
    });

    it('returns new object reference when options change', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result, rerender } = renderHook(({ n, e, opts }) => useAutoLayout(n, e, opts), {
        initialProps: { n: nodes, e: edges, opts: { direction: 'TB' as const } },
      });

      const firstResult = result.current;

      rerender({ n: nodes, e: edges, opts: { direction: 'LR' as const } });

      expect(result.current).not.toBe(firstResult);
    });
  });

  describe('Position conversion (center-to-top-left)', () => {
    it('converts dagre center coordinates to React Flow top-left anchor', () => {
      const nodes = [createTestNode('A')];
      const _defaultNodeWidth = 200;
      const _defaultNodeHeight = 100;

      const { result } = renderHook(() => useAutoLayout(nodes, []));

      const positionedNode = result.current.nodes[0];

      // The position should be offset from center by half the node dimensions
      // With default margin of 20, center would be at (20 + width/2, 20 + height/2)
      // Top-left should be at margin (20, 20)
      expect(positionedNode.position.x).toBe(20);
      expect(positionedNode.position.y).toBe(20);
    });

    it('center-to-top-left conversion works with custom node dimensions', () => {
      const nodes = [createTestNode('A')];
      const customWidth = 300;
      const customHeight = 150;

      const { result } = renderHook(() =>
        useAutoLayout(nodes, [], { nodeWidth: customWidth, nodeHeight: customHeight })
      );

      const positionedNode = result.current.nodes[0];

      // With margin of 20, the top-left corner should still be at (20, 20)
      expect(positionedNode.position.x).toBe(20);
      expect(positionedNode.position.y).toBe(20);
    });

    it('multiple nodes have correct relative positions after conversion', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];
      const nodeWidth = 200;
      const nodeHeight = 100;
      const ranksep = 100;

      const { result } = renderHook(() =>
        useAutoLayout(nodes, edges, { nodeWidth, nodeHeight, ranksep })
      );

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');

      // Both nodes should have the same x (vertically aligned in TB)
      expect(nodeA?.position.x).toBe(nodeB?.position.x);

      // The vertical distance should account for node height and ranksep
      const yDistance = (nodeB?.position.y ?? 0) - (nodeA?.position.y ?? 0);
      expect(yDistance).toBe(nodeHeight + ranksep);
    });
  });

  describe('Edge cases', () => {
    it('handles disconnected nodes', () => {
      const nodes = [createTestNode('A'), createTestNode('B'), createTestNode('C')];
      // No edges - all nodes are disconnected

      const { result } = renderHook(() => useAutoLayout(nodes, []));

      expect(result.current.nodes).toHaveLength(3);
      // All nodes should have valid positions
      for (const node of result.current.nodes) {
        expect(node.position.x).toBeGreaterThanOrEqual(0);
        expect(node.position.y).toBeGreaterThanOrEqual(0);
      }
    });

    it('handles diamond dependency pattern', () => {
      // A -> B, A -> C, B -> D, C -> D (diamond shape)
      const nodes = [
        createTestNode('A'),
        createTestNode('B'),
        createTestNode('C'),
        createTestNode('D'),
      ];
      const edges = [
        createTestEdge('A', 'B'),
        createTestEdge('A', 'C'),
        createTestEdge('B', 'D'),
        createTestEdge('C', 'D'),
      ];

      const { result } = renderHook(() => useAutoLayout(nodes, edges));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');
      const nodeD = result.current.nodes.find((n) => n.id === 'node-D');

      // A should be at top, D at bottom
      expect(nodeA?.position.y).toBeLessThan(nodeB?.position.y ?? 0);
      expect(nodeA?.position.y).toBeLessThan(nodeC?.position.y ?? 0);
      expect(nodeB?.position.y).toBeLessThan(nodeD?.position.y ?? 0);
      expect(nodeC?.position.y).toBeLessThan(nodeD?.position.y ?? 0);

      // B and C should be at same rank (same y)
      expect(nodeB?.position.y).toBe(nodeC?.position.y);
    });

    it('handles large graph with many nodes', () => {
      const nodes: IssueNode[] = [];
      const edges: DependencyEdge[] = [];

      for (let i = 0; i < 50; i++) {
        nodes.push(createTestNode(`issue-${i}`));
        if (i > 0) {
          edges.push(createTestEdge(`issue-${i - 1}`, `issue-${i}`));
        }
      }

      const { result } = renderHook(() => useAutoLayout(nodes, edges));

      expect(result.current.nodes).toHaveLength(50);
      expect(result.current.bounds.width).toBeGreaterThan(0);
      expect(result.current.bounds.height).toBeGreaterThan(0);

      // All nodes should have valid positions
      for (const node of result.current.nodes) {
        expect(typeof node.position.x).toBe('number');
        expect(typeof node.position.y).toBe('number');
        expect(Number.isFinite(node.position.x)).toBe(true);
        expect(Number.isFinite(node.position.y)).toBe(true);
      }
    });

    it('handles circular dependency A -> B -> A', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B'), createTestEdge('B', 'A')];

      // Dagre should handle cycles gracefully
      const { result } = renderHook(() => useAutoLayout(nodes, edges));

      expect(result.current.nodes).toHaveLength(2);
      // Both nodes should have valid positions
      for (const node of result.current.nodes) {
        expect(node.position.x).toBeGreaterThanOrEqual(0);
        expect(node.position.y).toBeGreaterThanOrEqual(0);
      }
    });

    it('handles self-referential edge', () => {
      const nodes = [createTestNode('A')];
      const edges = [createTestEdge('A', 'A')];

      // Dagre should handle self-loops gracefully
      const { result } = renderHook(() => useAutoLayout(nodes, edges));

      expect(result.current.nodes).toHaveLength(1);
      expect(result.current.nodes[0].position.x).toBeGreaterThan(0);
      expect(result.current.nodes[0].position.y).toBeGreaterThan(0);
    });
  });

  describe('Direction variations', () => {
    it('BT direction positions nodes with decreasing y (bottom to top)', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'BT' }));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');

      // In BT, target (B) should be above source (A)
      expect(nodeB?.position.y).toBeLessThan(nodeA?.position.y ?? 0);
    });

    it('RL direction positions nodes with decreasing x (right to left)', () => {
      const nodes = [createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('A', 'B')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges, { direction: 'RL' }));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');

      // In RL, target (B) should be to the left of source (A)
      expect(nodeB?.position.x).toBeLessThan(nodeA?.position.x ?? 0);
    });
  });

  describe('Bounds calculation', () => {
    it('bounds include all nodes', () => {
      const nodes = [createTestNode('A'), createTestNode('B'), createTestNode('C')];
      const edges = [createTestEdge('A', 'B'), createTestEdge('B', 'C')];

      const { result } = renderHook(() => useAutoLayout(nodes, edges));

      // All node positions should be within bounds
      for (const node of result.current.nodes) {
        // Position is top-left corner, so position + nodeWidth/Height should be <= bounds
        expect(node.position.x).toBeLessThan(result.current.bounds.width);
        expect(node.position.y).toBeLessThan(result.current.bounds.height);
      }
    });

    it('bounds grow with more nodes', () => {
      const smallNodes = [createTestNode('A'), createTestNode('B')];
      const smallEdges = [createTestEdge('A', 'B')];

      const largeNodes = [
        createTestNode('A'),
        createTestNode('B'),
        createTestNode('C'),
        createTestNode('D'),
        createTestNode('E'),
      ];
      const largeEdges = [
        createTestEdge('A', 'B'),
        createTestEdge('B', 'C'),
        createTestEdge('C', 'D'),
        createTestEdge('D', 'E'),
      ];

      const { result: smallResult } = renderHook(() => useAutoLayout(smallNodes, smallEdges));
      const { result: largeResult } = renderHook(() => useAutoLayout(largeNodes, largeEdges));

      expect(largeResult.current.bounds.height).toBeGreaterThan(smallResult.current.bounds.height);
    });
  });

  describe('Alignment options', () => {
    it('accepts align option without error', () => {
      const nodes = [createTestNode('Root'), createTestNode('A'), createTestNode('B')];
      const edges = [createTestEdge('Root', 'A'), createTestEdge('Root', 'B')];

      // Should not throw with any valid align option
      expect(() => {
        renderHook(() => useAutoLayout(nodes, edges, { align: 'UL' }));
      }).not.toThrow();

      expect(() => {
        renderHook(() => useAutoLayout(nodes, edges, { align: 'UR' }));
      }).not.toThrow();

      expect(() => {
        renderHook(() => useAutoLayout(nodes, edges, { align: 'DL' }));
      }).not.toThrow();

      expect(() => {
        renderHook(() => useAutoLayout(nodes, edges, { align: 'DR' }));
      }).not.toThrow();

      expect(() => {
        renderHook(() => useAutoLayout(nodes, edges, { align: undefined }));
      }).not.toThrow();
    });
  });
});
