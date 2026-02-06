/**
 * @vitest-environment jsdom
 */
import { renderHook } from '@testing-library/react';
import { describe, it, expect } from 'vitest';

import type { Issue, Status, Priority, IssueType } from '@/types';

import { useIssueFilter } from './useIssueFilter';

/**
 * Helper to create a minimal valid Issue for testing.
 * Only id, title, priority, created_at, and updated_at are required.
 */
function createIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: overrides.id ?? 'issue-1',
    title: overrides.title ?? 'Test Issue',
    priority: overrides.priority ?? 2,
    created_at: overrides.created_at ?? '2024-01-01T00:00:00Z',
    updated_at: overrides.updated_at ?? '2024-01-01T00:00:00Z',
    ...overrides,
  };
}

/**
 * Helper to create a set of test issues with various properties.
 */
function createTestIssues(): Issue[] {
  return [
    createIssue({
      id: 'issue-1',
      title: 'Fix login bug',
      description: 'Users cannot log in with special characters',
      notes: 'Check password validation',
      status: 'open',
      priority: 0,
      issue_type: 'bug',
      assignee: 'alice',
      labels: ['frontend', 'urgent'],
    }),
    createIssue({
      id: 'issue-2',
      title: 'Add dark mode feature',
      description: 'Implement theme switching',
      notes: 'Use CSS variables',
      status: 'in_progress',
      priority: 1,
      issue_type: 'feature',
      assignee: 'bob',
      labels: ['frontend', 'ui'],
    }),
    createIssue({
      id: 'issue-3',
      title: 'Database migration task',
      description: 'Migrate to PostgreSQL',
      status: 'open',
      priority: 2,
      issue_type: 'task',
      assignee: 'charlie',
      labels: ['backend', 'database'],
    }),
    createIssue({
      id: 'issue-4',
      title: 'Update documentation',
      description: 'Add API documentation',
      status: 'closed',
      priority: 3,
      issue_type: 'chore',
      // No assignee - unassigned issue
      labels: ['docs'],
    }),
    createIssue({
      id: 'issue-5',
      title: 'Epic: User management overhaul',
      description: 'Complete redesign of user management',
      status: 'open',
      priority: 4,
      issue_type: 'epic',
      assignee: '',
      labels: ['epic', 'backend'],
    }),
  ];
}

describe('useIssueFilter', () => {
  describe('search term tests', () => {
    it('filters by title containing search term', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'login' }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('filters by description containing search term', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'PostgreSQL' }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-3');
    });

    it('filters by notes containing search term', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { searchTerm: 'password validation' })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('performs case-insensitive matching', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'LOGIN' }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('performs partial string matching', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'doc' }));

      // Should match "Update documentation" and "Add API documentation"
      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-4');
    });

    it('returns all issues with empty search term', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: '' }));

      expect(result.current.filteredIssues).toHaveLength(5);
    });

    it('returns all issues with whitespace-only search term', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: '   ' }));

      expect(result.current.filteredIssues).toHaveLength(5);
    });

    it('matches across multiple fields', () => {
      const issues = createTestIssues();
      // "theme" appears in description of issue-2
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'theme' }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-2');
    });
  });

  describe('status filter tests', () => {
    it('filters by exact status', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { status: 'open' as Status }));

      expect(result.current.filteredIssues).toHaveLength(3);
      expect(result.current.filteredIssues.every((i) => i.status === 'open')).toBe(true);
    });

    it('filters by in_progress status', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { status: 'in_progress' as Status })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-2');
    });

    it('filters by closed status', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { status: 'closed' as Status }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-4');
    });

    it('returns all issues with undefined status', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { status: undefined }));

      expect(result.current.filteredIssues).toHaveLength(5);
    });
  });

  describe('priority filter tests', () => {
    it('filters by exact priority', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { priority: 0 as Priority }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('handles priority=0 correctly (P0 is valid)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { priority: 0 as Priority }));

      // P0 is a valid priority, not "unset"
      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].priority).toBe(0);
      expect(result.current.hasActiveFilters).toBe(true);
      expect(result.current.activeFilters).toContain('priority');
    });

    it('filters by priority range (min only)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { priorityMin: 2 as Priority }));

      // Should include priorities 2, 3, 4
      expect(result.current.filteredIssues).toHaveLength(3);
      expect(result.current.filteredIssues.every((i) => i.priority >= 2)).toBe(true);
    });

    it('filters by priority range (max only)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { priorityMax: 1 as Priority }));

      // Should include priorities 0, 1
      expect(result.current.filteredIssues).toHaveLength(2);
      expect(result.current.filteredIssues.every((i) => i.priority <= 1)).toBe(true);
    });

    it('filters by priority range (min and max)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          priorityMin: 1 as Priority,
          priorityMax: 3 as Priority,
        })
      );

      // Should include priorities 1, 2, 3
      expect(result.current.filteredIssues).toHaveLength(3);
      expect(result.current.filteredIssues.every((i) => i.priority >= 1 && i.priority <= 3)).toBe(
        true
      );
    });

    it('returns empty when min > max priority', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          priorityMin: 3 as Priority,
          priorityMax: 1 as Priority,
        })
      );

      // No issue can have priority >= 3 AND <= 1
      expect(result.current.filteredIssues).toHaveLength(0);
    });
  });

  describe('issue type filter tests', () => {
    it('filters by exact issue type - bug', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { issueType: 'bug' as IssueType })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('filters by exact issue type - feature', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { issueType: 'feature' as IssueType })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-2');
    });

    it('filters by exact issue type - epic', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { issueType: 'epic' as IssueType })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-5');
    });

    it('returns all issues with undefined issue type', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { issueType: undefined }));

      expect(result.current.filteredIssues).toHaveLength(5);
    });
  });

  describe('assignee filter tests', () => {
    it('filters by exact assignee', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { assignee: 'alice' }));

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('unassigned filter works correctly', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { unassigned: true }));

      // Issues without assignee or with empty string assignee
      expect(result.current.filteredIssues).toHaveLength(2);
      const ids = result.current.filteredIssues.map((i) => i.id);
      expect(ids).toContain('issue-4'); // undefined assignee
      expect(ids).toContain('issue-5'); // empty string assignee
    });

    it('assignee takes precedence over unassigned', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { assignee: 'bob', unassigned: true })
      );

      // When assignee is set, unassigned is ignored
      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-2');
      expect(result.current.filteredIssues[0].assignee).toBe('bob');
    });

    it('returns all issues with undefined assignee filter', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { assignee: undefined, unassigned: false })
      );

      expect(result.current.filteredIssues).toHaveLength(5);
    });
  });

  describe('label filter tests', () => {
    it('filters by all labels (AND logic)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { labels: ['frontend', 'urgent'] })
      );

      // Only issue-1 has both 'frontend' AND 'urgent'
      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('filters by any labels (OR logic)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { labelsAny: ['urgent', 'database'] })
      );

      // issue-1 has 'urgent', issue-3 has 'database'
      expect(result.current.filteredIssues).toHaveLength(2);
      const ids = result.current.filteredIssues.map((i) => i.id);
      expect(ids).toContain('issue-1');
      expect(ids).toContain('issue-3');
    });

    it('returns all issues with empty labels array', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { labels: [] }));

      expect(result.current.filteredIssues).toHaveLength(5);
    });

    it('returns all issues with empty labelsAny array', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { labelsAny: [] }));

      expect(result.current.filteredIssues).toHaveLength(5);
    });

    it('filters with single label (AND logic)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { labels: ['frontend'] }));

      // issue-1 and issue-2 have 'frontend'
      expect(result.current.filteredIssues).toHaveLength(2);
      const ids = result.current.filteredIssues.map((i) => i.id);
      expect(ids).toContain('issue-1');
      expect(ids).toContain('issue-2');
    });

    it('can combine labels and labelsAny filters', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          labels: ['frontend'], // Must have 'frontend'
          labelsAny: ['urgent', 'ui'], // Must have 'urgent' OR 'ui'
        })
      );

      // issue-1 has frontend + urgent, issue-2 has frontend + ui
      expect(result.current.filteredIssues).toHaveLength(2);
    });
  });

  describe('combined filter tests', () => {
    it('multiple filters work together (AND logic)', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          status: 'open' as Status,
          priority: 0 as Priority,
        })
      );

      // Only issue-1 is open with priority 0
      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('combines search term with status filter', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          searchTerm: 'bug',
          status: 'open' as Status,
        })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });

    it('combines status, priority, and assignee filters', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          status: 'in_progress' as Status,
          priority: 1 as Priority,
          assignee: 'bob',
        })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-2');
    });

    it('returns empty when combined filters match nothing', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          status: 'closed' as Status,
          priority: 0 as Priority, // No closed issue has P0
        })
      );

      expect(result.current.filteredIssues).toHaveLength(0);
    });

    it('combines all filter types', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          searchTerm: 'login',
          status: 'open' as Status,
          priority: 0 as Priority,
          issueType: 'bug' as IssueType,
          assignee: 'alice',
          labels: ['frontend'],
        })
      );

      expect(result.current.filteredIssues).toHaveLength(1);
      expect(result.current.filteredIssues[0].id).toBe('issue-1');
    });
  });

  describe('return value tests', () => {
    it('count matches filteredIssues.length', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { status: 'open' as Status }));

      expect(result.current.count).toBe(result.current.filteredIssues.length);
      expect(result.current.count).toBe(3);
    });

    it('totalCount matches input issues.length', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { status: 'open' as Status }));

      expect(result.current.totalCount).toBe(issues.length);
      expect(result.current.totalCount).toBe(5);
    });

    it('hasActiveFilters is true when filters applied', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { status: 'open' as Status }));

      expect(result.current.hasActiveFilters).toBe(true);
    });

    it('hasActiveFilters is false when no filters', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, {}));

      expect(result.current.hasActiveFilters).toBe(false);
    });

    it('hasActiveFilters is false with empty options object', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          searchTerm: undefined,
          status: undefined,
          priority: undefined,
        })
      );

      expect(result.current.hasActiveFilters).toBe(false);
    });

    it('activeFilters lists applied filter names', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, {
          searchTerm: 'test',
          status: 'open' as Status,
          priority: 1 as Priority,
        })
      );

      expect(result.current.activeFilters).toContain('search');
      expect(result.current.activeFilters).toContain('status');
      expect(result.current.activeFilters).toContain('priority');
      expect(result.current.activeFilters).toHaveLength(3);
    });

    it('activeFilters includes priorityRange when min or max set', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { priorityMin: 1 as Priority }));

      expect(result.current.activeFilters).toContain('priorityRange');
    });

    it('activeFilters includes type when issueType set', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() =>
        useIssueFilter(issues, { issueType: 'bug' as IssueType })
      );

      expect(result.current.activeFilters).toContain('type');
    });

    it('activeFilters includes unassigned when unassigned is true', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { unassigned: true }));

      expect(result.current.activeFilters).toContain('unassigned');
    });

    it('activeFilters includes labels when labels array has items', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { labels: ['frontend'] }));

      expect(result.current.activeFilters).toContain('labels');
    });

    it('activeFilters includes labelsAny when labelsAny array has items', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, { labelsAny: ['frontend'] }));

      expect(result.current.activeFilters).toContain('labelsAny');
    });

    it('activeFilters is empty array when no filters', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, {}));

      expect(result.current.activeFilters).toEqual([]);
    });
  });

  describe('edge case tests', () => {
    it('handles empty issues array', () => {
      const { result } = renderHook(() => useIssueFilter([], { status: 'open' as Status }));

      expect(result.current.filteredIssues).toHaveLength(0);
      expect(result.current.count).toBe(0);
      expect(result.current.totalCount).toBe(0);
      expect(result.current.hasActiveFilters).toBe(true);
    });

    it('handles issues with undefined title', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: undefined as unknown as string,
          description: 'Some description',
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'test' }));

      // Should not throw, should not match
      expect(result.current.filteredIssues).toHaveLength(0);
    });

    it('handles issues with undefined description', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test title',
          description: undefined,
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'title' }));

      // Should match on title
      expect(result.current.filteredIssues).toHaveLength(1);
    });

    it('handles issues with undefined notes', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test issue',
          notes: undefined,
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'Test' }));

      // Should match on title
      expect(result.current.filteredIssues).toHaveLength(1);
    });

    it('handles issues with null fields', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test issue',
          description: null as unknown as string,
          notes: null as unknown as string,
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: 'Test' }));

      // Should not throw and should match on title
      expect(result.current.filteredIssues).toHaveLength(1);
    });

    it('handles issues with undefined labels', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test issue',
          labels: undefined,
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { labels: ['frontend'] }));

      // Should not match - no labels
      expect(result.current.filteredIssues).toHaveLength(0);
    });

    it('handles issues with undefined status', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test issue',
          status: undefined,
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { status: 'open' as Status }));

      // Should not match - undefined !== 'open'
      expect(result.current.filteredIssues).toHaveLength(0);
    });

    it('handles issues with undefined issue_type', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test issue',
          issue_type: undefined,
        }),
      ];
      const { result } = renderHook(() =>
        useIssueFilter(issues, { issueType: 'bug' as IssueType })
      );

      // Should not match - undefined !== 'bug'
      expect(result.current.filteredIssues).toHaveLength(0);
    });

    it('returns original array reference when no filters active', () => {
      const issues = createTestIssues();
      const { result } = renderHook(() => useIssueFilter(issues, {}));

      // When no filters, should return same array reference for optimization
      expect(result.current.filteredIssues).toBe(issues);
    });

    it('handles very long search terms', () => {
      const issues = createTestIssues();
      const longSearchTerm = 'a'.repeat(1000);
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: longSearchTerm }));

      // Should not throw, should not match anything
      expect(result.current.filteredIssues).toHaveLength(0);
    });

    it('handles special regex characters in search term', () => {
      const issues = [
        createIssue({
          id: 'issue-1',
          title: 'Test [brackets] and (parens)',
        }),
      ];
      const { result } = renderHook(() => useIssueFilter(issues, { searchTerm: '[brackets]' }));

      // Should match literally, not as regex
      expect(result.current.filteredIssues).toHaveLength(1);
    });
  });

  describe('hook reactivity', () => {
    it('updates when issues change', () => {
      const initialIssues = [createIssue({ id: 'issue-1', status: 'open' })];
      const { result, rerender } = renderHook(
        ({ issues, options }) => useIssueFilter(issues, options),
        {
          initialProps: {
            issues: initialIssues,
            options: { status: 'open' as Status },
          },
        }
      );

      expect(result.current.count).toBe(1);

      const newIssues = [
        createIssue({ id: 'issue-1', status: 'open' }),
        createIssue({ id: 'issue-2', status: 'open' }),
      ];

      rerender({
        issues: newIssues,
        options: { status: 'open' as Status },
      });

      expect(result.current.count).toBe(2);
    });

    it('updates when options change', () => {
      const issues = createTestIssues();
      const { result, rerender } = renderHook(
        ({ issues, options }) => useIssueFilter(issues, options),
        {
          initialProps: {
            issues,
            options: { status: 'open' as Status },
          },
        }
      );

      expect(result.current.count).toBe(3); // 3 open issues

      rerender({
        issues,
        options: { status: 'closed' as Status },
      });

      expect(result.current.count).toBe(1); // 1 closed issue
    });
  });
});
