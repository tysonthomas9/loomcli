/**
 * Unit tests for graph types (React Flow visualization).
 */

import { describe, it, expect } from 'vitest';

import type {
  IssueNode,
  DependencyEdge,
  IssueNodeData,
  DependencyEdgeData,
  GraphNodeType,
  GraphEdgeType,
} from '../graph';
import type { Issue } from '../issue';

describe('Graph types', () => {
  describe('IssueNode', () => {
    it('should create valid IssueNode', () => {
      const issue: Issue = {
        id: 'beads-123',
        title: 'Test Issue',
        priority: 2,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };

      const node: IssueNode = {
        id: 'node-1',
        type: 'issue',
        position: { x: 0, y: 0 },
        data: {
          issue,
          title: 'Test Issue',
          status: 'open',
          priority: 2,
          issueType: 'task',
          dependencyCount: 0,
          dependentCount: 1,
        },
      };

      expect(node.type).toBe('issue');
      expect(node.id).toBe('node-1');
      expect(node.data.title).toBe('Test Issue');
      expect(node.data.priority).toBe(2);
      expect(node.data.dependencyCount).toBe(0);
      expect(node.data.dependentCount).toBe(1);
    });

    it('should handle undefined optional fields', () => {
      const issue: Issue = {
        id: 'beads-456',
        title: 'Minimal Issue',
        priority: 1,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };

      const node: IssueNode = {
        id: 'node-2',
        type: 'issue',
        position: { x: 100, y: 200 },
        data: {
          issue,
          title: 'Minimal Issue',
          status: undefined,
          priority: 1,
          issueType: undefined,
          dependencyCount: 0,
          dependentCount: 0,
        },
      };

      expect(node.data.status).toBeUndefined();
      expect(node.data.issueType).toBeUndefined();
    });

    it('should support all priority levels', () => {
      const createNodeWithPriority = (priority: 0 | 1 | 2 | 3 | 4): IssueNode => ({
        id: `node-p${priority}`,
        type: 'issue',
        position: { x: 0, y: 0 },
        data: {
          issue: {
            id: `beads-p${priority}`,
            title: `P${priority} Issue`,
            priority,
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
          },
          title: `P${priority} Issue`,
          status: 'open',
          priority,
          issueType: 'task',
          dependencyCount: 0,
          dependentCount: 0,
        },
      });

      // Test all priority levels (P0-P4)
      expect(createNodeWithPriority(0).data.priority).toBe(0);
      expect(createNodeWithPriority(1).data.priority).toBe(1);
      expect(createNodeWithPriority(2).data.priority).toBe(2);
      expect(createNodeWithPriority(3).data.priority).toBe(3);
      expect(createNodeWithPriority(4).data.priority).toBe(4);
    });
  });

  describe('DependencyEdge', () => {
    it('should create valid DependencyEdge', () => {
      const edge: DependencyEdge = {
        id: 'edge-1',
        type: 'dependency',
        source: 'node-1',
        target: 'node-2',
        data: {
          dependencyType: 'blocks',
          isBlocking: true,
          sourceIssueId: 'beads-123',
          targetIssueId: 'beads-456',
        },
      };

      expect(edge.type).toBe('dependency');
      expect(edge.id).toBe('edge-1');
      expect(edge.source).toBe('node-1');
      expect(edge.target).toBe('node-2');
      expect(edge.data?.dependencyType).toBe('blocks');
      expect(edge.data?.isBlocking).toBe(true);
    });

    it('should support non-blocking dependency types', () => {
      const edge: DependencyEdge = {
        id: 'edge-2',
        type: 'dependency',
        source: 'node-3',
        target: 'node-4',
        data: {
          dependencyType: 'related',
          isBlocking: false,
          sourceIssueId: 'beads-789',
          targetIssueId: 'beads-012',
        },
      };

      expect(edge.data?.dependencyType).toBe('related');
      expect(edge.data?.isBlocking).toBe(false);
    });

    it('should support various dependency types', () => {
      const dependencyTypes = [
        'blocks',
        'parent-child',
        'conditional-blocks',
        'waits-for',
        'related',
        'discovered-from',
        'replies-to',
        'relates-to',
        'duplicates',
        'supersedes',
      ] as const;

      for (const depType of dependencyTypes) {
        const edge: DependencyEdge = {
          id: `edge-${depType}`,
          type: 'dependency',
          source: 'node-a',
          target: 'node-b',
          data: {
            dependencyType: depType,
            isBlocking: depType === 'blocks' || depType === 'conditional-blocks',
            sourceIssueId: 'beads-src',
            targetIssueId: 'beads-tgt',
          },
        };

        expect(edge.data?.dependencyType).toBe(depType);
      }
    });
  });

  describe('IssueNodeData', () => {
    it('should contain all required fields', () => {
      const issue: Issue = {
        id: 'beads-data-test',
        title: 'Data Test',
        priority: 3,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
        status: 'in_progress',
        issue_type: 'feature',
      };

      const nodeData: IssueNodeData = {
        issue,
        title: issue.title,
        status: issue.status,
        priority: issue.priority,
        issueType: issue.issue_type,
        dependencyCount: 2,
        dependentCount: 3,
      };

      expect(nodeData.issue).toBe(issue);
      expect(nodeData.title).toBe('Data Test');
      expect(nodeData.status).toBe('in_progress');
      expect(nodeData.priority).toBe(3);
      expect(nodeData.issueType).toBe('feature');
      expect(nodeData.dependencyCount).toBe(2);
      expect(nodeData.dependentCount).toBe(3);
    });
  });

  describe('DependencyEdgeData', () => {
    it('should contain all required fields', () => {
      const edgeData: DependencyEdgeData = {
        dependencyType: 'blocks',
        isBlocking: true,
        sourceIssueId: 'beads-src',
        targetIssueId: 'beads-tgt',
      };

      expect(edgeData.dependencyType).toBe('blocks');
      expect(edgeData.isBlocking).toBe(true);
      expect(edgeData.sourceIssueId).toBe('beads-src');
      expect(edgeData.targetIssueId).toBe('beads-tgt');
    });
  });

  describe('Union types', () => {
    it('GraphNodeType should accept IssueNode', () => {
      const node: GraphNodeType = {
        id: 'union-node',
        type: 'issue',
        position: { x: 0, y: 0 },
        data: {
          issue: {
            id: 'beads-union',
            title: 'Union Test',
            priority: 2,
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
          },
          title: 'Union Test',
          status: 'open',
          priority: 2,
          issueType: 'task',
          dependencyCount: 0,
          dependentCount: 0,
        },
      };

      expect(node.type).toBe('issue');
    });

    it('GraphEdgeType should accept DependencyEdge', () => {
      const edge: GraphEdgeType = {
        id: 'union-edge',
        type: 'dependency',
        source: 'node-a',
        target: 'node-b',
        data: {
          dependencyType: 'blocks',
          isBlocking: true,
          sourceIssueId: 'beads-a',
          targetIssueId: 'beads-b',
        },
      };

      expect(edge.type).toBe('dependency');
    });
  });

  describe('Type narrowing', () => {
    it('should support type narrowing via type discriminator', () => {
      // Simulate receiving a node and checking its type
      const nodes: GraphNodeType[] = [
        {
          id: 'narrow-1',
          type: 'issue',
          position: { x: 0, y: 0 },
          data: {
            issue: {
              id: 'beads-narrow',
              title: 'Narrowing Test',
              priority: 1,
              created_at: '2024-01-01T00:00:00Z',
              updated_at: '2024-01-01T00:00:00Z',
            },
            title: 'Narrowing Test',
            status: 'open',
            priority: 1,
            issueType: 'bug',
            dependencyCount: 0,
            dependentCount: 0,
          },
        },
      ];

      for (const node of nodes) {
        if (node.type === 'issue') {
          // TypeScript should narrow to IssueNode here
          expect(node.data.title).toBe('Narrowing Test');
          expect(node.data.issue.id).toBe('beads-narrow');
        }
      }
    });

    it('should support type narrowing for edges', () => {
      const edges: GraphEdgeType[] = [
        {
          id: 'narrow-edge',
          type: 'dependency',
          source: 'node-1',
          target: 'node-2',
          data: {
            dependencyType: 'waits-for',
            isBlocking: true,
            sourceIssueId: 'beads-1',
            targetIssueId: 'beads-2',
          },
        },
      ];

      for (const edge of edges) {
        if (edge.type === 'dependency') {
          // TypeScript should narrow to DependencyEdge here
          expect(edge.data?.dependencyType).toBe('waits-for');
        }
      }
    });
  });
});
