/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for groupingUtils functions.
 */

import { describe, it, expect } from 'vitest';

import type { Issue } from '@/types';

import { groupIssuesByField, sortLanes, type GroupByField, type LaneGroup } from '../groupingUtils';

/**
 * Create a mock issue for testing.
 */
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: `issue-${Math.random().toString(36).slice(2, 9)}`,
    title: 'Test Issue',
    priority: 2,
    status: 'open',
    issue_type: 'task',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('groupIssuesByField', () => {
  describe('groupBy=none', () => {
    it('returns single group containing all issues', () => {
      const issues = [
        createMockIssue({ id: 'issue-1' }),
        createMockIssue({ id: 'issue-2' }),
        createMockIssue({ id: 'issue-3' }),
      ];

      const result = groupIssuesByField(issues, 'none');

      expect(result).toHaveLength(1);
      expect(result[0].id).toBe('lane-all');
      expect(result[0].title).toBe('All Issues');
      expect(result[0].issues).toHaveLength(3);
    });

    it('returns single empty group when issues array is empty', () => {
      const result = groupIssuesByField([], 'none');

      expect(result).toHaveLength(1);
      expect(result[0].id).toBe('lane-all');
      expect(result[0].title).toBe('All Issues');
      expect(result[0].issues).toHaveLength(0);
    });
  });

  describe('groupBy=epic', () => {
    it('groups issues by parent (epic)', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', parent: 'epic-1', parent_title: 'Epic One' }),
        createMockIssue({ id: 'issue-2', parent: 'epic-1', parent_title: 'Epic One' }),
        createMockIssue({ id: 'issue-3', parent: 'epic-2', parent_title: 'Epic Two' }),
      ];

      const result = groupIssuesByField(issues, 'epic');

      expect(result).toHaveLength(2);

      const epic1Lane = result.find((lane) => lane.title === 'Epic One');
      const epic2Lane = result.find((lane) => lane.title === 'Epic Two');

      expect(epic1Lane).toBeDefined();
      expect(epic1Lane?.issues).toHaveLength(2);
      expect(epic1Lane?.id).toBe('lane-epic-epic-1');

      expect(epic2Lane).toBeDefined();
      expect(epic2Lane?.issues).toHaveLength(1);
      expect(epic2Lane?.id).toBe('lane-epic-epic-2');
    });

    it('creates Ungrouped lane for issues without parent', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', parent: 'epic-1', parent_title: 'Epic One' }),
        createMockIssue({ id: 'issue-2', parent: undefined }),
        createMockIssue({ id: 'issue-3', parent: undefined }),
      ];

      const result = groupIssuesByField(issues, 'epic');

      expect(result).toHaveLength(2);

      const ungroupedLane = result.find((lane) => lane.title === 'Ungrouped');
      expect(ungroupedLane).toBeDefined();
      expect(ungroupedLane?.issues).toHaveLength(2);
      expect(ungroupedLane?.id).toBe('lane-epic-__ungrouped__');
    });

    it('uses parent ID as title when parent_title is not available', () => {
      const issues = [createMockIssue({ id: 'issue-1', parent: 'epic-id-123' })];

      const result = groupIssuesByField(issues, 'epic');

      expect(result).toHaveLength(1);
      expect(result[0].title).toBe('epic-id-123');
    });
  });

  describe('groupBy=assignee', () => {
    it('groups issues by assignee', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice' }),
        createMockIssue({ id: 'issue-2', assignee: 'alice' }),
        createMockIssue({ id: 'issue-3', assignee: 'bob' }),
      ];

      const result = groupIssuesByField(issues, 'assignee');

      expect(result).toHaveLength(2);

      const aliceLane = result.find((lane) => lane.title === 'alice');
      const bobLane = result.find((lane) => lane.title === 'bob');

      expect(aliceLane).toBeDefined();
      expect(aliceLane?.issues).toHaveLength(2);
      expect(aliceLane?.id).toBe('lane-assignee-alice');

      expect(bobLane).toBeDefined();
      expect(bobLane?.issues).toHaveLength(1);
      expect(bobLane?.id).toBe('lane-assignee-bob');
    });

    it('creates Unassigned lane for issues without assignee', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice' }),
        createMockIssue({ id: 'issue-2', assignee: undefined }),
      ];

      const result = groupIssuesByField(issues, 'assignee');

      expect(result).toHaveLength(2);

      const unassignedLane = result.find((lane) => lane.title === 'Unassigned');
      expect(unassignedLane).toBeDefined();
      expect(unassignedLane?.issues).toHaveLength(1);
      expect(unassignedLane?.id).toBe('lane-assignee-__unassigned__');
    });
  });

  describe('groupBy=priority', () => {
    it('groups issues by priority with correct display names', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', priority: 0 }),
        createMockIssue({ id: 'issue-2', priority: 1 }),
        createMockIssue({ id: 'issue-3', priority: 1 }),
        createMockIssue({ id: 'issue-4', priority: 2 }),
        createMockIssue({ id: 'issue-5', priority: 3 }),
        createMockIssue({ id: 'issue-6', priority: 4 }),
      ];

      const result = groupIssuesByField(issues, 'priority');

      expect(result).toHaveLength(5);

      const p0Lane = result.find((lane) => lane.title === 'P0 (Critical)');
      const p1Lane = result.find((lane) => lane.title === 'P1 (High)');
      const p2Lane = result.find((lane) => lane.title === 'P2 (Medium)');
      const p3Lane = result.find((lane) => lane.title === 'P3 (Normal)');
      const p4Lane = result.find((lane) => lane.title === 'P4 (Backlog)');

      expect(p0Lane).toBeDefined();
      expect(p0Lane?.issues).toHaveLength(1);

      expect(p1Lane).toBeDefined();
      expect(p1Lane?.issues).toHaveLength(2);

      expect(p2Lane).toBeDefined();
      expect(p2Lane?.issues).toHaveLength(1);

      expect(p3Lane).toBeDefined();
      expect(p3Lane?.issues).toHaveLength(1);

      expect(p4Lane).toBeDefined();
      expect(p4Lane?.issues).toHaveLength(1);
    });

    it('creates No Priority lane for issues without priority', () => {
      const issueWithoutPriority: Issue = {
        id: 'no-priority',
        title: 'No Priority Issue',
        priority: undefined as unknown as number,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };

      const issues = [createMockIssue({ id: 'issue-1', priority: 1 }), issueWithoutPriority];

      const result = groupIssuesByField(issues, 'priority');

      const noPriorityLane = result.find((lane) => lane.title === 'No Priority');
      expect(noPriorityLane).toBeDefined();
      expect(noPriorityLane?.issues).toHaveLength(1);
      expect(noPriorityLane?.id).toBe('lane-priority-__no_priority__');
    });

    it('handles unknown priority values gracefully', () => {
      const issues = [createMockIssue({ id: 'issue-1', priority: 99 })];

      const result = groupIssuesByField(issues, 'priority');

      expect(result).toHaveLength(1);
      expect(result[0].title).toBe('P99');
    });
  });

  describe('groupBy=type', () => {
    it('groups issues by issue_type with capitalized titles', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', issue_type: 'bug' }),
        createMockIssue({ id: 'issue-2', issue_type: 'bug' }),
        createMockIssue({ id: 'issue-3', issue_type: 'feature' }),
        createMockIssue({ id: 'issue-4', issue_type: 'task' }),
      ];

      const result = groupIssuesByField(issues, 'type');

      expect(result).toHaveLength(3);

      const bugLane = result.find((lane) => lane.title === 'Bug');
      const featureLane = result.find((lane) => lane.title === 'Feature');
      const taskLane = result.find((lane) => lane.title === 'Task');

      expect(bugLane).toBeDefined();
      expect(bugLane?.issues).toHaveLength(2);
      expect(bugLane?.id).toBe('lane-type-bug');

      expect(featureLane).toBeDefined();
      expect(featureLane?.issues).toHaveLength(1);

      expect(taskLane).toBeDefined();
      expect(taskLane?.issues).toHaveLength(1);
    });

    it('creates No Type lane for issues without issue_type', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', issue_type: 'bug' }),
        createMockIssue({ id: 'issue-2', issue_type: undefined }),
      ];

      const result = groupIssuesByField(issues, 'type');

      const noTypeLane = result.find((lane) => lane.title === 'No Type');
      expect(noTypeLane).toBeDefined();
      expect(noTypeLane?.issues).toHaveLength(1);
      expect(noTypeLane?.id).toBe('lane-type-__no_type__');
    });
  });

  describe('groupBy=label', () => {
    it('groups issues by labels', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', labels: ['frontend'] }),
        createMockIssue({ id: 'issue-2', labels: ['backend'] }),
        createMockIssue({ id: 'issue-3', labels: ['frontend'] }),
      ];

      const result = groupIssuesByField(issues, 'label');

      expect(result).toHaveLength(2);

      const frontendLane = result.find((lane) => lane.title === 'frontend');
      const backendLane = result.find((lane) => lane.title === 'backend');

      expect(frontendLane).toBeDefined();
      expect(frontendLane?.issues).toHaveLength(2);
      expect(frontendLane?.id).toBe('lane-label-frontend');

      expect(backendLane).toBeDefined();
      expect(backendLane?.issues).toHaveLength(1);
    });

    it('duplicates issues across multiple label lanes', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', labels: ['frontend', 'urgent'] }),
        createMockIssue({ id: 'issue-2', labels: ['backend'] }),
      ];

      const result = groupIssuesByField(issues, 'label');

      expect(result).toHaveLength(3);

      const frontendLane = result.find((lane) => lane.title === 'frontend');
      const urgentLane = result.find((lane) => lane.title === 'urgent');
      const backendLane = result.find((lane) => lane.title === 'backend');

      expect(frontendLane?.issues).toHaveLength(1);
      expect(frontendLane?.issues[0].id).toBe('issue-1');

      expect(urgentLane?.issues).toHaveLength(1);
      expect(urgentLane?.issues[0].id).toBe('issue-1');

      expect(backendLane?.issues).toHaveLength(1);
      expect(backendLane?.issues[0].id).toBe('issue-2');
    });

    it('creates No Labels lane for issues without labels', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', labels: ['frontend'] }),
        createMockIssue({ id: 'issue-2', labels: undefined }),
        createMockIssue({ id: 'issue-3', labels: [] }),
      ];

      const result = groupIssuesByField(issues, 'label');

      const noLabelsLane = result.find((lane) => lane.title === 'No Labels');
      expect(noLabelsLane).toBeDefined();
      expect(noLabelsLane?.issues).toHaveLength(2);
      expect(noLabelsLane?.id).toBe('lane-label-__no_labels__');
    });
  });

  describe('empty issues', () => {
    it.each<GroupByField>(['epic', 'assignee', 'priority', 'type', 'label'])(
      'returns empty array when grouping empty issues by %s',
      (groupBy) => {
        const result = groupIssuesByField([], groupBy);
        expect(result).toHaveLength(0);
      }
    );
  });
});

describe('sortLanes', () => {
  describe('sortBy=title', () => {
    it('sorts lanes alphabetically by title', () => {
      const lanes: LaneGroup[] = [
        { id: 'c', title: 'Charlie', issues: [] },
        { id: 'a', title: 'Alice', issues: [] },
        { id: 'b', title: 'Bob', issues: [] },
      ];

      const result = sortLanes(lanes, 'title');

      expect(result[0].title).toBe('Alice');
      expect(result[1].title).toBe('Bob');
      expect(result[2].title).toBe('Charlie');
    });

    it('places special lanes at the end', () => {
      const lanes: LaneGroup[] = [
        { id: '1', title: 'Unassigned', issues: [] },
        { id: '2', title: 'Alice', issues: [] },
        { id: '3', title: 'No Priority', issues: [] },
        { id: '4', title: 'Bob', issues: [] },
      ];

      const result = sortLanes(lanes, 'title');

      expect(result[0].title).toBe('Alice');
      expect(result[1].title).toBe('Bob');
      expect(result[2].title).toBe('Unassigned');
      expect(result[3].title).toBe('No Priority');
    });

    it('identifies all special lane titles', () => {
      const specialTitles = ['Ungrouped', 'Unassigned', 'No Priority', 'No Type', 'No Labels'];
      const lanes: LaneGroup[] = [
        { id: 'regular', title: 'Regular Lane', issues: [] },
        ...specialTitles.map((title, i) => ({
          id: `special-${i}`,
          title,
          issues: [],
        })),
      ];

      const result = sortLanes(lanes, 'title');

      // Regular lane should be first
      expect(result[0].title).toBe('Regular Lane');

      // All special lanes should be after
      for (let i = 1; i < result.length; i++) {
        expect(specialTitles).toContain(result[i].title);
      }
    });
  });

  describe('sortBy=count', () => {
    it('sorts lanes by issue count descending', () => {
      const lanes: LaneGroup[] = [
        { id: '1', title: 'Small', issues: [createMockIssue()] },
        {
          id: '2',
          title: 'Large',
          issues: [createMockIssue(), createMockIssue(), createMockIssue()],
        },
        { id: '3', title: 'Medium', issues: [createMockIssue(), createMockIssue()] },
      ];

      const result = sortLanes(lanes, 'count');

      expect(result[0].title).toBe('Large');
      expect(result[0].issues).toHaveLength(3);

      expect(result[1].title).toBe('Medium');
      expect(result[1].issues).toHaveLength(2);

      expect(result[2].title).toBe('Small');
      expect(result[2].issues).toHaveLength(1);
    });

    it('places special lanes at the end regardless of count', () => {
      const lanes: LaneGroup[] = [
        { id: '1', title: 'Small', issues: [createMockIssue()] },
        {
          id: '2',
          title: 'Unassigned',
          issues: Array(10)
            .fill(null)
            .map(() => createMockIssue()),
        },
        { id: '3', title: 'Medium', issues: [createMockIssue(), createMockIssue()] },
      ];

      const result = sortLanes(lanes, 'count');

      // Regular lanes sorted by count
      expect(result[0].title).toBe('Medium');
      expect(result[1].title).toBe('Small');

      // Special lane at the end even though it has most issues
      expect(result[2].title).toBe('Unassigned');
    });
  });

  describe('immutability', () => {
    it('does not mutate the original array', () => {
      const lanes: LaneGroup[] = [
        { id: '2', title: 'B', issues: [] },
        { id: '1', title: 'A', issues: [] },
      ];
      const original = [...lanes];

      sortLanes(lanes, 'title');

      expect(lanes[0].title).toBe(original[0].title);
      expect(lanes[1].title).toBe(original[1].title);
    });

    it('returns a new array', () => {
      const lanes: LaneGroup[] = [{ id: '1', title: 'A', issues: [] }];

      const result = sortLanes(lanes, 'title');

      expect(result).not.toBe(lanes);
    });
  });

  describe('edge cases', () => {
    it('handles empty lanes array', () => {
      const result = sortLanes([], 'title');
      expect(result).toHaveLength(0);
    });

    it('handles single lane', () => {
      const lanes: LaneGroup[] = [{ id: '1', title: 'Only Lane', issues: [] }];

      const result = sortLanes(lanes, 'title');

      expect(result).toHaveLength(1);
      expect(result[0].title).toBe('Only Lane');
    });

    it('handles all special lanes', () => {
      const lanes: LaneGroup[] = [
        { id: '1', title: 'Unassigned', issues: [] },
        { id: '2', title: 'No Priority', issues: [] },
      ];

      const result = sortLanes(lanes, 'title');

      expect(result).toHaveLength(2);
      // Both are special, so they remain in original order relative to each other
    });

    it('handles equal issue counts', () => {
      const lanes: LaneGroup[] = [
        { id: '1', title: 'Z Lane', issues: [createMockIssue()] },
        { id: '2', title: 'A Lane', issues: [createMockIssue()] },
        { id: '3', title: 'M Lane', issues: [createMockIssue()] },
      ];

      const result = sortLanes(lanes, 'count');

      // With equal counts, the relative order depends on the stable sort
      expect(result).toHaveLength(3);
      // All should have 1 issue
      result.forEach((lane) => {
        expect(lane.issues).toHaveLength(1);
      });
    });
  });
});
