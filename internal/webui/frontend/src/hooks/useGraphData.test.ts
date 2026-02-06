/**
 * @vitest-environment jsdom
 */
import { renderHook } from '@testing-library/react';
import { describe, it, expect } from 'vitest';

import type { Issue, Dependency, DependencyType } from '@/types';

import { useGraphData } from './useGraphData';

/**
 * Helper to create a test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-issue-1',
    title: 'Test Issue',
    priority: 2,
    created_at: '2025-01-23T10:00:00Z',
    updated_at: '2025-01-23T10:00:00Z',
    ...overrides,
  };
}

/**
 * Helper to create a dependency between two issues.
 */
function createDependency(
  issueId: string,
  dependsOnId: string,
  type: DependencyType = 'blocks'
): Dependency {
  return {
    issue_id: issueId,
    depends_on_id: dependsOnId,
    type,
    created_at: '2025-01-23T10:00:00Z',
  };
}

/**
 * Helper to create an issue with dependencies.
 */
function createIssueWithDependencies(
  id: string,
  dependencies: Dependency[],
  overrides: Partial<Issue> = {}
): Issue {
  return createTestIssue({
    id,
    title: `Issue ${id}`,
    dependencies,
    ...overrides,
  });
}

describe('useGraphData', () => {
  describe('Empty input', () => {
    it('returns empty arrays and zero counts for empty issues array', () => {
      const { result } = renderHook(() => useGraphData([]));

      expect(result.current.nodes).toEqual([]);
      expect(result.current.edges).toEqual([]);
      expect(result.current.issueIdToNodeId.size).toBe(0);
      expect(result.current.totalDependencies).toBe(0);
      expect(result.current.blockingDependencies).toBe(0);
    });

    it('returns expected shape with all properties', () => {
      const { result } = renderHook(() => useGraphData([]));

      expect(result.current).toHaveProperty('nodes');
      expect(result.current).toHaveProperty('edges');
      expect(result.current).toHaveProperty('issueIdToNodeId');
      expect(result.current).toHaveProperty('totalDependencies');
      expect(result.current).toHaveProperty('blockingDependencies');
    });
  });

  describe('Single node', () => {
    it('creates one node with zero edges for issue with no dependencies', () => {
      const issue = createTestIssue({ id: 'single-issue', title: 'Single Issue' });
      const { result } = renderHook(() => useGraphData([issue]));

      expect(result.current.nodes).toHaveLength(1);
      expect(result.current.edges).toHaveLength(0);
      expect(result.current.totalDependencies).toBe(0);
      expect(result.current.blockingDependencies).toBe(0);
    });

    it('node has correct structure and data', () => {
      const issue = createTestIssue({
        id: 'test-id',
        title: 'Test Title',
        status: 'open',
        priority: 1,
        issue_type: 'bug',
      });
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.id).toBe('node-test-id');
      expect(node.type).toBe('issue');
      expect(node.position).toEqual({ x: 0, y: 0 });
      expect(node.data.issue).toBe(issue);
      expect(node.data.title).toBe('Test Title');
      expect(node.data.status).toBe('open');
      expect(node.data.priority).toBe(1);
      expect(node.data.issueType).toBe('bug');
      expect(node.data.dependencyCount).toBe(0);
      expect(node.data.dependentCount).toBe(0);
    });

    it('builds issueIdToNodeId map correctly', () => {
      const issue = createTestIssue({ id: 'my-issue' });
      const { result } = renderHook(() => useGraphData([issue]));

      expect(result.current.issueIdToNodeId.get('my-issue')).toBe('node-my-issue');
    });
  });

  describe('Linear chain A -> B -> C', () => {
    it('creates 3 nodes and 2 edges', () => {
      // A depends on B, B depends on C
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B')]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'C')]);
      const issueC = createIssueWithDependencies('C', []);

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC]));

      expect(result.current.nodes).toHaveLength(3);
      expect(result.current.edges).toHaveLength(2);
      expect(result.current.totalDependencies).toBe(2);
    });

    it('edges have correct source and target', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B')]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'C')]);
      const issueC = createIssueWithDependencies('C', []);

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC]));

      // Edge IDs include dependency type (default is 'blocks')
      const edgeAB = result.current.edges.find((e) => e.id === 'edge-A-B-blocks');
      const edgeBC = result.current.edges.find((e) => e.id === 'edge-B-C-blocks');

      expect(edgeAB).toBeDefined();
      expect(edgeAB?.source).toBe('node-A');
      expect(edgeAB?.target).toBe('node-B');

      expect(edgeBC).toBeDefined();
      expect(edgeBC?.source).toBe('node-B');
      expect(edgeBC?.target).toBe('node-C');
    });

    it('dependency counts are accurate', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B')]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'C')]);
      const issueC = createIssueWithDependencies('C', []);

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC]));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      // A has 1 outgoing (to B), 0 incoming
      expect(nodeA?.data.dependencyCount).toBe(1);
      expect(nodeA?.data.dependentCount).toBe(0);

      // B has 1 outgoing (to C), 1 incoming (from A)
      expect(nodeB?.data.dependencyCount).toBe(1);
      expect(nodeB?.data.dependentCount).toBe(1);

      // C has 0 outgoing, 1 incoming (from B)
      expect(nodeC?.data.dependencyCount).toBe(0);
      expect(nodeC?.data.dependentCount).toBe(1);
    });
  });

  describe('Blocking detection', () => {
    it.each([
      ['blocks', true],
      ['parent-child', true],
      ['conditional-blocks', true],
      ['waits-for', true],
    ] as const)('%s type sets isBlocking: %s', (depType, expected) => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B', depType)]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      const edge = result.current.edges[0];
      expect(edge.data?.isBlocking).toBe(expected);
    });

    it('counts blocking dependencies correctly', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'parent-child'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC]));

      expect(result.current.totalDependencies).toBe(2);
      expect(result.current.blockingDependencies).toBe(2);
    });
  });

  describe('Non-blocking detection', () => {
    it.each([
      ['related', false],
      ['discovered-from', false],
      ['replies-to', false],
      ['relates-to', false],
      ['duplicates', false],
      ['supersedes', false],
    ] as const)('%s type sets isBlocking: %s', (depType, expected) => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B', depType)]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      const edge = result.current.edges[0];
      expect(edge.data?.isBlocking).toBe(expected);
    });

    it('mixed blocking and non-blocking dependencies counted correctly', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
        createDependency('A', 'D', 'waits-for'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC, issueD]));

      expect(result.current.totalDependencies).toBe(3);
      expect(result.current.blockingDependencies).toBe(2); // blocks + waits-for
    });
  });

  describe('Counts accuracy', () => {
    it('dependencyCount matches outgoing edges', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'C'),
        createDependency('A', 'D'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC, issueD]));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const outgoingEdges = result.current.edges.filter((e) => e.source === 'node-A');

      expect(nodeA?.data.dependencyCount).toBe(outgoingEdges.length);
      expect(nodeA?.data.dependencyCount).toBe(3);
    });

    it('dependentCount matches incoming edges', () => {
      // Multiple issues depend on D
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'D')]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'D')]);
      const issueC = createIssueWithDependencies('C', [createDependency('C', 'D')]);
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC, issueD]));

      const nodeD = result.current.nodes.find((n) => n.id === 'node-D');
      const incomingEdges = result.current.edges.filter((e) => e.target === 'node-D');

      expect(nodeD?.data.dependentCount).toBe(incomingEdges.length);
      expect(nodeD?.data.dependentCount).toBe(3);
    });

    it('totalDependencies equals number of edges', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'C'),
      ]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'C')]);
      const issueC = createTestIssue({ id: 'C' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC]));

      expect(result.current.totalDependencies).toBe(result.current.edges.length);
      expect(result.current.totalDependencies).toBe(3);
    });
  });

  describe('Missing target', () => {
    it('skips dependency to non-existent issue', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'non-existent'), // Should be skipped
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      expect(result.current.edges).toHaveLength(1);
      expect(result.current.edges[0].target).toBe('node-B');
      expect(result.current.totalDependencies).toBe(1);
    });

    it('does not count missing targets in dependency counts', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'missing-1'),
        createDependency('A', 'missing-2'),
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      expect(nodeA?.data.dependencyCount).toBe(1); // Only B is counted
    });
  });

  describe('Memoization', () => {
    it('returns same object reference for same input (referential equality)', () => {
      const issues = [
        createIssueWithDependencies('A', [createDependency('A', 'B')]),
        createTestIssue({ id: 'B' }),
      ];

      const { result, rerender } = renderHook(({ issues: issuesArg }) => useGraphData(issuesArg), {
        initialProps: { issues },
      });

      const firstResult = result.current;

      // Rerender with same array reference
      rerender({ issues });

      expect(result.current).toBe(firstResult);
      expect(result.current.nodes).toBe(firstResult.nodes);
      expect(result.current.edges).toBe(firstResult.edges);
      expect(result.current.issueIdToNodeId).toBe(firstResult.issueIdToNodeId);
    });

    it('returns new object reference when issues array changes', () => {
      const issues1 = [createTestIssue({ id: 'A' })];
      const issues2 = [createTestIssue({ id: 'B' })];

      const { result, rerender } = renderHook(({ issues }) => useGraphData(issues), {
        initialProps: { issues: issues1 },
      });

      const firstResult = result.current;

      rerender({ issues: issues2 });

      expect(result.current).not.toBe(firstResult);
    });

    it('returns new object reference when options change', () => {
      const issues = [
        createIssueWithDependencies('A', [
          createDependency('A', 'B', 'blocks'),
          createDependency('A', 'C', 'related'),
        ]),
        createTestIssue({ id: 'B' }),
        createTestIssue({ id: 'C' }),
      ];

      const { result, rerender } = renderHook(
        ({ issues: issuesArg, options }) => useGraphData(issuesArg, options),
        { initialProps: { issues, options: {} } }
      );

      const firstResult = result.current;

      rerender({ issues, options: { includeDependencyTypes: ['blocks'] } });

      expect(result.current).not.toBe(firstResult);
    });
  });

  describe('Filtering by dependency type', () => {
    it('includes only specified dependency types when filter provided', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
        createDependency('A', 'D', 'waits-for'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB, issueC, issueD], {
          includeDependencyTypes: ['blocks'],
        })
      );

      expect(result.current.edges).toHaveLength(1);
      expect(result.current.edges[0].data?.dependencyType).toBe('blocks');
      expect(result.current.totalDependencies).toBe(1);
    });

    it('includes multiple specified dependency types', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
        createDependency('A', 'D', 'waits-for'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB, issueC, issueD], {
          includeDependencyTypes: ['blocks', 'waits-for'],
        })
      );

      expect(result.current.edges).toHaveLength(2);
      expect(result.current.totalDependencies).toBe(2);
      expect(result.current.blockingDependencies).toBe(2);
    });

    it('includes all types when no filter provided', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC]));

      expect(result.current.edges).toHaveLength(2);
    });

    it('filtering affects dependency counts on nodes', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB, issueC], {
          includeDependencyTypes: ['blocks'],
        })
      );

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      expect(nodeA?.data.dependencyCount).toBe(1); // Only blocks counted
      expect(nodeB?.data.dependentCount).toBe(1);
      expect(nodeC?.data.dependentCount).toBe(0); // Related was filtered out
    });

    it('returns empty edges when filter excludes all types', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB, issueC], {
          includeDependencyTypes: ['duplicates'], // No duplicates in our data
        })
      );

      expect(result.current.edges).toHaveLength(0);
      expect(result.current.totalDependencies).toBe(0);
    });

    it('returns empty edges when empty array filter provided', () => {
      // Empty array means "include nothing"
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'related'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB, issueC], {
          includeDependencyTypes: [], // Empty array should exclude all
        })
      );

      expect(result.current.edges).toHaveLength(0);
      expect(result.current.totalDependencies).toBe(0);
      expect(result.current.blockingDependencies).toBe(0);

      // Nodes should still be created
      expect(result.current.nodes).toHaveLength(3);

      // Dependency counts should be 0
      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      expect(nodeA?.data.dependencyCount).toBe(0);
    });
  });

  describe('Edge cases', () => {
    it('handles self-referential dependency gracefully', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'A'), // Self-reference
      ]);

      const { result } = renderHook(() => useGraphData([issueA]));

      // Self-reference should create an edge
      expect(result.current.nodes).toHaveLength(1);
      expect(result.current.edges).toHaveLength(1);
      expect(result.current.edges[0].source).toBe('node-A');
      expect(result.current.edges[0].target).toBe('node-A');
    });

    it('handles issue with undefined dependencies', () => {
      const issue = createTestIssue({ id: 'A', dependencies: undefined });
      const { result } = renderHook(() => useGraphData([issue]));

      expect(result.current.nodes).toHaveLength(1);
      expect(result.current.edges).toHaveLength(0);
    });

    it('handles issue with empty dependencies array', () => {
      const issue = createTestIssue({ id: 'A', dependencies: [] });
      const { result } = renderHook(() => useGraphData([issue]));

      expect(result.current.nodes).toHaveLength(1);
      expect(result.current.edges).toHaveLength(0);
    });

    it('handles duplicate dependencies (same source-target, different types)', () => {
      // Same source-target pair with different dependency types creates unique edges
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'B', 'related'), // Same pair, different type
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      // Both should create unique edges (edge IDs include dependency type)
      expect(result.current.edges).toHaveLength(2);
      expect(result.current.totalDependencies).toBe(2);

      // Verify edge IDs are unique
      const edgeIds = result.current.edges.map((e) => e.id);
      expect(edgeIds).toContain('edge-A-B-blocks');
      expect(edgeIds).toContain('edge-A-B-related');
    });

    it('handles circular dependency A -> B -> A', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B')]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'A')]);

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      expect(result.current.nodes).toHaveLength(2);
      expect(result.current.edges).toHaveLength(2);
      expect(result.current.totalDependencies).toBe(2);

      // Both nodes should have 1 outgoing and 1 incoming
      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');

      expect(nodeA?.data.dependencyCount).toBe(1);
      expect(nodeA?.data.dependentCount).toBe(1);
      expect(nodeB?.data.dependencyCount).toBe(1);
      expect(nodeB?.data.dependentCount).toBe(1);
    });

    it('handles large graph with many nodes', () => {
      const issues: Issue[] = [];
      for (let i = 0; i < 100; i++) {
        const deps = i > 0 ? [createDependency(`issue-${i}`, `issue-${i - 1}`)] : [];
        issues.push(createIssueWithDependencies(`issue-${i}`, deps));
      }

      const { result } = renderHook(() => useGraphData(issues));

      expect(result.current.nodes).toHaveLength(100);
      expect(result.current.edges).toHaveLength(99); // Linear chain
      expect(result.current.issueIdToNodeId.size).toBe(100);
    });

    it('handles custom (non-known) dependency type', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'custom-type' as DependencyType),
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      expect(result.current.edges).toHaveLength(1);
      expect(result.current.edges[0].data?.dependencyType).toBe('custom-type');
      expect(result.current.edges[0].data?.isBlocking).toBe(false); // Unknown types are not blocking
    });
  });

  describe('issueIdToNodeId map', () => {
    it('contains all issue IDs mapped to node IDs', () => {
      const issues = [
        createTestIssue({ id: 'alpha' }),
        createTestIssue({ id: 'beta' }),
        createTestIssue({ id: 'gamma' }),
      ];

      const { result } = renderHook(() => useGraphData(issues));

      expect(result.current.issueIdToNodeId.size).toBe(3);
      expect(result.current.issueIdToNodeId.get('alpha')).toBe('node-alpha');
      expect(result.current.issueIdToNodeId.get('beta')).toBe('node-beta');
      expect(result.current.issueIdToNodeId.get('gamma')).toBe('node-gamma');
    });

    it('map is empty for empty issues array', () => {
      const { result } = renderHook(() => useGraphData([]));
      expect(result.current.issueIdToNodeId.size).toBe(0);
    });

    it('map does not contain non-existent dependency targets', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'non-existent')]);

      const { result } = renderHook(() => useGraphData([issueA]));

      expect(result.current.issueIdToNodeId.size).toBe(1);
      expect(result.current.issueIdToNodeId.has('non-existent')).toBe(false);
    });
  });

  describe('totalDependencies and blockingDependencies counts', () => {
    it('both counts are zero when no dependencies exist', () => {
      const issues = [createTestIssue({ id: 'A' }), createTestIssue({ id: 'B' })];

      const { result } = renderHook(() => useGraphData(issues));

      expect(result.current.totalDependencies).toBe(0);
      expect(result.current.blockingDependencies).toBe(0);
    });

    it('all counts are blocking when all dependencies are blocking types', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'C', 'parent-child'),
        createDependency('A', 'D', 'waits-for'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC, issueD]));

      expect(result.current.totalDependencies).toBe(3);
      expect(result.current.blockingDependencies).toBe(3);
    });

    it('zero blocking when all dependencies are non-blocking types', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'related'),
        createDependency('A', 'C', 'discovered-from'),
        createDependency('A', 'D', 'duplicates'),
      ]);
      const issueB = createTestIssue({ id: 'B' });
      const issueC = createTestIssue({ id: 'C' });
      const issueD = createTestIssue({ id: 'D' });

      const { result } = renderHook(() => useGraphData([issueA, issueB, issueC, issueD]));

      expect(result.current.totalDependencies).toBe(3);
      expect(result.current.blockingDependencies).toBe(0);
    });

    it('counts exclude dependencies to missing targets', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'missing', 'blocks'), // Should not be counted
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      expect(result.current.totalDependencies).toBe(1);
      expect(result.current.blockingDependencies).toBe(1);
    });
  });

  describe('Edge data structure', () => {
    it('edge has correct id format including dependency type', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B', 'blocks')]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      // Edge ID format: edge-{source}-{target}-{type}
      expect(result.current.edges[0].id).toBe('edge-A-B-blocks');
    });

    it('edge has correct type', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B')]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      expect(result.current.edges[0].type).toBe('dependency');
    });

    it('edge data contains all required fields', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'B', 'blocks')]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      const edgeData = result.current.edges[0].data;
      expect(edgeData).toBeDefined();
      expect(edgeData?.dependencyType).toBe('blocks');
      expect(edgeData?.isBlocking).toBe(true);
      expect(edgeData?.sourceIssueId).toBe('A');
      expect(edgeData?.targetIssueId).toBe('B');
    });
  });

  describe('isReady computation', () => {
    it('sets isReady: true when blockedIssueIds is undefined', () => {
      const issue = createTestIssue({ id: 'A', status: 'open' });
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(true);
    });

    it('sets isReady: true when issue ID not in blockedIssueIds', () => {
      const issue = createTestIssue({ id: 'A', status: 'open' });
      const blockedIssueIds = new Set(['B', 'C']); // A is not blocked

      const { result } = renderHook(() => useGraphData([issue], { blockedIssueIds }));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(true);
    });

    it('sets isReady: false when issue ID in blockedIssueIds', () => {
      const issue = createTestIssue({ id: 'A', status: 'open' });
      const blockedIssueIds = new Set(['A']); // A is blocked

      const { result } = renderHook(() => useGraphData([issue], { blockedIssueIds }));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(false);
    });

    it('sets isReady: false for closed issues regardless of blockers', () => {
      const issue = createTestIssue({ id: 'A', status: 'closed' });
      const blockedIssueIds = new Set<string>(); // Not blocked

      const { result } = renderHook(() => useGraphData([issue], { blockedIssueIds }));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(false);
    });

    it('sets isReady: false for deferred issues regardless of blockers', () => {
      const issue = createTestIssue({ id: 'A', status: 'deferred' });
      const blockedIssueIds = new Set<string>(); // Not blocked

      const { result } = renderHook(() => useGraphData([issue], { blockedIssueIds }));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(false);
    });

    it('sets isReady: true for open issues without blockers', () => {
      const issue = createTestIssue({ id: 'A', status: 'open' });
      const blockedIssueIds = new Set<string>();

      const { result } = renderHook(() => useGraphData([issue], { blockedIssueIds }));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(true);
    });

    it('sets isReady: true for in_progress issues without blockers', () => {
      const issue = createTestIssue({ id: 'A', status: 'in_progress' });
      const blockedIssueIds = new Set<string>();

      const { result } = renderHook(() => useGraphData([issue], { blockedIssueIds }));

      const node = result.current.nodes[0];
      expect(node.data.isReady).toBe(true);
    });

    it('correctly computes isReady for multiple issues', () => {
      const openNotBlocked = createTestIssue({ id: 'A', status: 'open' });
      const openBlocked = createTestIssue({ id: 'B', status: 'open' });
      const closedNotBlocked = createTestIssue({ id: 'C', status: 'closed' });
      const inProgressNotBlocked = createTestIssue({ id: 'D', status: 'in_progress' });

      const blockedIssueIds = new Set(['B']);

      const { result } = renderHook(() =>
        useGraphData([openNotBlocked, openBlocked, closedNotBlocked, inProgressNotBlocked], {
          blockedIssueIds,
        })
      );

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');
      const nodeD = result.current.nodes.find((n) => n.id === 'node-D');

      expect(nodeA?.data.isReady).toBe(true); // open, not blocked
      expect(nodeB?.data.isReady).toBe(false); // open, blocked
      expect(nodeC?.data.isReady).toBe(false); // closed
      expect(nodeD?.data.isReady).toBe(true); // in_progress, not blocked
    });

    it('memoizes correctly when blockedIssueIds changes', () => {
      const issue = createTestIssue({ id: 'A', status: 'open' });

      const { result, rerender } = renderHook(
        ({ blockedIssueIds }) => useGraphData([issue], { blockedIssueIds }),
        { initialProps: { blockedIssueIds: new Set<string>() } }
      );

      const firstResult = result.current;

      // Change blockedIssueIds
      rerender({ blockedIssueIds: new Set(['A']) });

      expect(result.current).not.toBe(firstResult);
      expect(result.current.nodes[0].data.isReady).toBe(false);
    });
  });

  describe('isClosed flag', () => {
    it('sets isClosed: true for closed issues', () => {
      const issue = createTestIssue({ id: 'A', status: 'closed' });
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.data.isClosed).toBe(true);
    });

    it('sets isClosed: false for open issues', () => {
      const issue = createTestIssue({ id: 'A', status: 'open' });
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.data.isClosed).toBe(false);
    });

    it('sets isClosed: false for deferred issues', () => {
      const issue = createTestIssue({ id: 'A', status: 'deferred' });
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.data.isClosed).toBe(false);
    });

    it('sets isClosed: false for in_progress issues', () => {
      const issue = createTestIssue({ id: 'A', status: 'in_progress' });
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.data.isClosed).toBe(false);
    });

    it('sets isClosed: false for issues without status', () => {
      const issue = createTestIssue({ id: 'A' }); // No status
      const { result } = renderHook(() => useGraphData([issue]));

      const node = result.current.nodes[0];
      expect(node.data.isClosed).toBe(false);
    });

    it('correctly sets isClosed for multiple issues with mixed statuses', () => {
      const openIssue = createTestIssue({ id: 'A', status: 'open' });
      const closedIssue = createTestIssue({ id: 'B', status: 'closed' });
      const inProgressIssue = createTestIssue({ id: 'C', status: 'in_progress' });

      const { result } = renderHook(() => useGraphData([openIssue, closedIssue, inProgressIssue]));

      const nodeA = result.current.nodes.find((n) => n.id === 'node-A');
      const nodeB = result.current.nodes.find((n) => n.id === 'node-B');
      const nodeC = result.current.nodes.find((n) => n.id === 'node-C');

      expect(nodeA?.data.isClosed).toBe(false);
      expect(nodeB?.data.isClosed).toBe(true);
      expect(nodeC?.data.isClosed).toBe(false);
    });
  });

  describe('Orphan edges', () => {
    it('skips orphan edges by default', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'non-existent'),
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() => useGraphData([issueA, issueB]));

      expect(result.current.edges).toHaveLength(1);
      expect(result.current.orphanEdgeCount).toBe(0);
      expect(result.current.missingTargetIds.size).toBe(0);
    });

    it('includes orphan edges when includeOrphanEdges: true', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'non-existent'),
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB], { includeOrphanEdges: true })
      );

      expect(result.current.edges).toHaveLength(2);
      expect(result.current.orphanEdgeCount).toBe(1);
      expect(result.current.totalDependencies).toBe(2);
    });

    it('creates ghost nodes for orphan targets', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'non-existent')]);

      const { result } = renderHook(() => useGraphData([issueA], { includeOrphanEdges: true }));

      // Should have 2 nodes: A and ghost node for 'non-existent'
      expect(result.current.nodes).toHaveLength(2);

      const ghostNode = result.current.nodes.find((n) => n.id === 'node-non-existent');
      expect(ghostNode).toBeDefined();
      expect(ghostNode?.data.title).toBe('Missing: non-existent');
    });

    it('ghost nodes have isGhostNode: true', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'ghost-target')]);

      const { result } = renderHook(() => useGraphData([issueA], { includeOrphanEdges: true }));

      const ghostNode = result.current.nodes.find((n) => n.id === 'node-ghost-target');
      expect(ghostNode?.data.isGhostNode).toBe(true);

      // Regular nodes should not have isGhostNode set (or it should be undefined)
      const regularNode = result.current.nodes.find((n) => n.id === 'node-A');
      expect(regularNode?.data.isGhostNode).toBeUndefined();
    });

    it('tracks missingTargetIds correctly', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'missing-1'),
        createDependency('A', 'missing-2'),
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB], { includeOrphanEdges: true })
      );

      expect(result.current.missingTargetIds.size).toBe(2);
      expect(result.current.missingTargetIds.has('missing-1')).toBe(true);
      expect(result.current.missingTargetIds.has('missing-2')).toBe(true);
      expect(result.current.missingTargetIds.has('B')).toBe(false);
    });

    it('orphanEdgeCount matches number of edges to ghost nodes', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B'),
        createDependency('A', 'ghost-1'),
        createDependency('A', 'ghost-2'),
        createDependency('A', 'ghost-3'),
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB], { includeOrphanEdges: true })
      );

      expect(result.current.orphanEdgeCount).toBe(3);
      expect(result.current.totalDependencies).toBe(4);
    });

    it('ghost nodes have correct default values', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'ghost-target')]);

      const { result } = renderHook(() => useGraphData([issueA], { includeOrphanEdges: true }));

      const ghostNode = result.current.nodes.find((n) => n.id === 'node-ghost-target');
      expect(ghostNode?.data.priority).toBe(4);
      expect(ghostNode?.data.status).toBeUndefined();
      expect(ghostNode?.data.issueType).toBeUndefined();
      expect(ghostNode?.data.dependencyCount).toBe(0);
      expect(ghostNode?.data.isReady).toBe(false);
      expect(ghostNode?.data.blockedCount).toBe(0);
      expect(ghostNode?.data.isRootBlocker).toBe(false);
      expect(ghostNode?.data.isClosed).toBe(false);
    });

    it('ghost nodes have correct dependentCount', () => {
      // Multiple issues depend on the same ghost target
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'ghost-target')]);
      const issueB = createIssueWithDependencies('B', [createDependency('B', 'ghost-target')]);
      const issueC = createIssueWithDependencies('C', [createDependency('C', 'ghost-target')]);

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB, issueC], { includeOrphanEdges: true })
      );

      const ghostNode = result.current.nodes.find((n) => n.id === 'node-ghost-target');
      expect(ghostNode?.data.dependentCount).toBe(3);
    });

    it('updates issueIdToNodeId map with ghost node IDs', () => {
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'ghost-target')]);

      const { result } = renderHook(() => useGraphData([issueA], { includeOrphanEdges: true }));

      expect(result.current.issueIdToNodeId.get('ghost-target')).toBe('node-ghost-target');
    });

    it('works with all orphan dependencies (no real targets)', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'ghost-1'),
        createDependency('A', 'ghost-2'),
      ]);

      const { result } = renderHook(() => useGraphData([issueA], { includeOrphanEdges: true }));

      // 1 real node + 2 ghost nodes
      expect(result.current.nodes).toHaveLength(3);
      expect(result.current.edges).toHaveLength(2);
      expect(result.current.orphanEdgeCount).toBe(2);
    });

    it('ghost nodes break cycles naturally (no outgoing edges)', () => {
      // A depends on ghost, ghost would depend on A if it were real
      // But ghost nodes have no outgoing edges, so no cycle
      const issueA = createIssueWithDependencies('A', [createDependency('A', 'ghost')]);

      const { result } = renderHook(() => useGraphData([issueA], { includeOrphanEdges: true }));

      const ghostNode = result.current.nodes.find((n) => n.id === 'node-ghost');
      expect(ghostNode?.data.dependencyCount).toBe(0);

      // Only one edge: A -> ghost
      const edgesFromGhost = result.current.edges.filter((e) => e.source === 'node-ghost');
      expect(edgesFromGhost).toHaveLength(0);
    });

    it('combines with type filtering correctly', () => {
      const issueA = createIssueWithDependencies('A', [
        createDependency('A', 'B', 'blocks'),
        createDependency('A', 'ghost-1', 'blocks'),
        createDependency('A', 'ghost-2', 'related'), // Should be filtered out
      ]);
      const issueB = createTestIssue({ id: 'B' });

      const { result } = renderHook(() =>
        useGraphData([issueA, issueB], {
          includeOrphanEdges: true,
          includeDependencyTypes: ['blocks'],
        })
      );

      // Only blocks edges: A->B and A->ghost-1
      expect(result.current.edges).toHaveLength(2);
      expect(result.current.orphanEdgeCount).toBe(1);
      expect(result.current.missingTargetIds.has('ghost-1')).toBe(true);
      expect(result.current.missingTargetIds.has('ghost-2')).toBe(false);
    });
  });

  describe('Return type includes new fields', () => {
    it('returns orphanEdgeCount and missingTargetIds', () => {
      const { result } = renderHook(() => useGraphData([]));

      expect(result.current).toHaveProperty('orphanEdgeCount');
      expect(result.current).toHaveProperty('missingTargetIds');
      expect(result.current.orphanEdgeCount).toBe(0);
      expect(result.current.missingTargetIds).toBeInstanceOf(Set);
    });
  });
});
