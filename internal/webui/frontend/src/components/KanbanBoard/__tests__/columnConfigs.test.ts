/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for columnConfigs – verifies the Backlog/Review column
 * filter logic after renaming Pending → Backlog and expanding its filter
 * to include status=blocked and status=deferred issues.
 */

import { describe, it, expect } from 'vitest';

import { DEFAULT_COLUMNS } from '../columnConfigs';
import type { Issue } from '@/types';
import type { BlockedInfo } from '../KanbanBoard';

/**
 * Create a mock issue for testing column filters.
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

/**
 * Convenience helpers to look up column configs by id.
 */
function getColumn(id: string) {
  const col = DEFAULT_COLUMNS.find((c) => c.id === id);
  if (!col) throw new Error(`Column "${id}" not found`);
  return col;
}

const blocked: BlockedInfo = { blockedByCount: 1, blocksCount: 0 };
const notBlocked: BlockedInfo = { blockedByCount: 0, blocksCount: 0 };

describe('columnConfigs', () => {
  // ---------------------------------------------------------------
  // 1. Backlog column identity
  // ---------------------------------------------------------------
  describe('Backlog column identity', () => {
    it('has id "backlog" at index 1', () => {
      expect(DEFAULT_COLUMNS[1].id).toBe('backlog');
    });

    it('has label "Backlog" at index 1', () => {
      expect(DEFAULT_COLUMNS[1].label).toBe('Backlog');
    });
  });

  // ---------------------------------------------------------------
  // 2-5. Backlog filter
  // ---------------------------------------------------------------
  describe('Backlog filter', () => {
    const backlog = getColumn('backlog');

    it('matches open issues blocked by dependencies (blockedByCount > 0)', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(backlog.filter(issue, blocked)).toBe(true);
    });

    it('matches issues with status=blocked', () => {
      const issue = createMockIssue({ status: 'blocked' });
      expect(backlog.filter(issue, notBlocked)).toBe(true);
    });

    it('matches issues with status=deferred', () => {
      const issue = createMockIssue({ status: 'deferred' });
      expect(backlog.filter(issue, notBlocked)).toBe(true);
    });

    it('rejects [Need Review] titled issues even if blocked', () => {
      const issue = createMockIssue({
        title: '[Need Review] Fix login flow',
        status: 'open',
      });
      expect(backlog.filter(issue, blocked)).toBe(false);
    });

    it('rejects [Need Review] titled issues even if deferred', () => {
      const issue = createMockIssue({
        title: '[Need Review] Cleanup',
        status: 'deferred',
      });
      expect(backlog.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects open issues with no blockers', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(backlog.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects open issues when blockedInfo is undefined', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(backlog.filter(issue, undefined)).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // 6-8. Review filter
  // ---------------------------------------------------------------
  describe('Review filter', () => {
    const review = getColumn('review');

    it('does NOT match status=blocked issues', () => {
      const issue = createMockIssue({ status: 'blocked' });
      expect(review.filter(issue, notBlocked)).toBe(false);
    });

    it('matches status=review issues', () => {
      const issue = createMockIssue({ status: 'review' });
      expect(review.filter(issue, notBlocked)).toBe(true);
    });

    it('matches [Need Review] titled issues', () => {
      const issue = createMockIssue({
        title: '[Need Review] Update docs',
        status: 'open',
      });
      expect(review.filter(issue, notBlocked)).toBe(true);
    });
  });

  // ---------------------------------------------------------------
  // 9. Ready column still works
  // ---------------------------------------------------------------
  describe('Ready filter', () => {
    const ready = getColumn('ready');

    it('matches open issues with no blockers and no [Need Review]', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(ready.filter(issue, notBlocked)).toBe(true);
    });

    it('matches issues with undefined status (treated as open)', () => {
      const issue = createMockIssue({ status: undefined });
      expect(ready.filter(issue, notBlocked)).toBe(true);
    });

    it('rejects open issues that have blockers', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(ready.filter(issue, blocked)).toBe(false);
    });

    it('rejects [Need Review] titled issues', () => {
      const issue = createMockIssue({
        title: '[Need Review] Refactor API',
        status: 'open',
      });
      expect(ready.filter(issue, notBlocked)).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // Epic exclusion from kanban columns
  // ---------------------------------------------------------------
  describe('Epic exclusion from kanban columns', () => {
    it('excludes epics from Ready', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'open' });
      expect(getColumn('ready').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Backlog (open + blocked)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'open' });
      expect(getColumn('backlog').filter(issue, blocked)).toBe(false);
    });

    it('excludes epics from Backlog (blocked status)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'blocked' });
      expect(getColumn('backlog').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Backlog (deferred status)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'deferred' });
      expect(getColumn('backlog').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from In Progress', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'in_progress' });
      expect(getColumn('in_progress').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Review (status)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'review' });
      expect(getColumn('review').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Review (title)', () => {
      const issue = createMockIssue({
        issue_type: 'epic',
        title: '[Need Review] Epic cleanup',
      });
      expect(getColumn('review').filter(issue, notBlocked)).toBe(false);
    });

    it('includes epics in Done', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'closed' });
      expect(getColumn('done').filter(issue, notBlocked)).toBe(true);
    });

    it('still includes non-epic tasks in Ready (regression)', () => {
      const issue = createMockIssue({ issue_type: 'task', status: 'open' });
      expect(getColumn('ready').filter(issue, notBlocked)).toBe(true);
    });

    it('still includes undefined issue_type in Ready (regression)', () => {
      const issue = createMockIssue({ issue_type: undefined, status: 'open' });
      expect(getColumn('ready').filter(issue, notBlocked)).toBe(true);
    });
  });
});
