/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for columnConfigs – verifies the Backlog/Blocked/Review column
 * filter logic. Backlog only contains deferred issues, Blocked contains
 * dependency-blocked and status=blocked issues.
 */

import { describe, it, expect } from 'vitest';

import type { Issue } from '@/types';

import { DEFAULT_COLUMNS } from '../columnConfigs';
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

const blocked: BlockedInfo = { blockedByCount: 1, blockedBy: [] };
const notBlocked: BlockedInfo = { blockedByCount: 0, blockedBy: [] };

describe('columnConfigs', () => {
  // ---------------------------------------------------------------
  // 1. Backlog column identity
  // ---------------------------------------------------------------
  describe('Backlog column identity', () => {
    it('has id "backlog" at index 0', () => {
      expect(DEFAULT_COLUMNS[0].id).toBe('backlog');
    });

    it('has label "Backlog" at index 0', () => {
      expect(DEFAULT_COLUMNS[0].label).toBe('Backlog');
    });
  });

  // ---------------------------------------------------------------
  // 2-5. Backlog filter (only deferred status)
  // ---------------------------------------------------------------
  describe('Backlog filter', () => {
    const backlog = getColumn('backlog');

    it('matches issues with status=deferred', () => {
      const issue = createMockIssue({ status: 'deferred' });
      expect(backlog.filter(issue, notBlocked)).toBe(true);
    });

    it('rejects [Need Review] titled issues even if deferred', () => {
      const issue = createMockIssue({
        title: '[Need Review] Cleanup',
        status: 'deferred',
      });
      expect(backlog.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects open issues blocked by dependencies (now in Blocked column)', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(backlog.filter(issue, blocked)).toBe(false);
    });

    it('rejects issues with status=blocked (now in Blocked column)', () => {
      const issue = createMockIssue({ status: 'blocked' });
      expect(backlog.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects open issues with no blockers', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(backlog.filter(issue, notBlocked)).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // Blocked filter (dependency-blocked + status=blocked)
  // ---------------------------------------------------------------
  describe('Blocked filter', () => {
    const blockedCol = getColumn('blocked');

    it('matches open issues blocked by dependencies (blockedByCount > 0)', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(blockedCol.filter(issue, blocked)).toBe(true);
    });

    it('matches issues with undefined status blocked by dependencies', () => {
      const issue = createMockIssue({ status: undefined });
      expect(blockedCol.filter(issue, blocked)).toBe(true);
    });

    it('matches issues with status=blocked', () => {
      const issue = createMockIssue({ status: 'blocked' });
      expect(blockedCol.filter(issue, notBlocked)).toBe(true);
    });

    it('rejects [Need Review] titled issues even if blocked by dependencies', () => {
      const issue = createMockIssue({
        title: '[Need Review] Fix login flow',
        status: 'open',
      });
      expect(blockedCol.filter(issue, blocked)).toBe(false);
    });

    it('rejects [Need Review] titled issues even if status=blocked', () => {
      const issue = createMockIssue({
        title: '[Need Review] Blocked task',
        status: 'blocked',
      });
      expect(blockedCol.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects open issues with no blockers', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(blockedCol.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects open issues when blockedInfo is undefined', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(blockedCol.filter(issue, undefined)).toBe(false);
    });

    it('rejects deferred issues (those go to Backlog)', () => {
      const issue = createMockIssue({ status: 'deferred' });
      expect(blockedCol.filter(issue, notBlocked)).toBe(false);
    });

    it('rejects deferred issues even when they have blockers (deferred goes to Backlog)', () => {
      const issue = createMockIssue({ status: 'deferred' });
      expect(blockedCol.filter(issue, blocked)).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // 6-8. Needs Review column (id: 'review', label: 'Needs Review')
  // ---------------------------------------------------------------
  describe('Needs Review column identity', () => {
    it('has id "review" at index 4', () => {
      expect(DEFAULT_COLUMNS[4].id).toBe('review');
    });

    it('has label "Needs Review" at index 4', () => {
      expect(DEFAULT_COLUMNS[4].label).toBe('Needs Review');
    });
  });

  describe('Needs Review filter', () => {
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
  // 9. Open column (id: 'ready', label: 'Open')
  // ---------------------------------------------------------------
  describe('Open column identity', () => {
    it('has id "ready" at index 1', () => {
      expect(DEFAULT_COLUMNS[1].id).toBe('ready');
    });

    it('has label "Open" at index 1', () => {
      expect(DEFAULT_COLUMNS[1].label).toBe('Open');
    });
  });

  describe('Open filter', () => {
    const open = getColumn('ready');

    it('matches open issues with no blockers and no [Need Review]', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(open.filter(issue, notBlocked)).toBe(true);
    });

    it('matches issues with undefined status (treated as open)', () => {
      const issue = createMockIssue({ status: undefined });
      expect(open.filter(issue, notBlocked)).toBe(true);
    });

    it('rejects open issues that have blockers', () => {
      const issue = createMockIssue({ status: 'open' });
      expect(open.filter(issue, blocked)).toBe(false);
    });

    it('rejects [Need Review] titled issues', () => {
      const issue = createMockIssue({
        title: '[Need Review] Refactor API',
        status: 'open',
      });
      expect(open.filter(issue, notBlocked)).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // Column style configuration
  // ---------------------------------------------------------------
  describe('Column style configuration', () => {
    it('Needs Review column has style "normal" (not highlighted)', () => {
      const review = getColumn('review');
      expect(review.style).toBe('normal');
    });

    it('backlog column has style "muted"', () => {
      const backlog = getColumn('backlog');
      expect(backlog.style).toBe('muted');
    });

    it('blocked column has style "muted"', () => {
      const blockedCol = getColumn('blocked');
      expect(blockedCol.style).toBe('muted');
    });

    it('all non-backlog/blocked columns have style "normal"', () => {
      const actionable = DEFAULT_COLUMNS.filter((c) => c.id !== 'backlog' && c.id !== 'blocked');
      for (const col of actionable) {
        expect(col.style).toBe('normal');
      }
    });
  });

  // ---------------------------------------------------------------
  // Epic exclusion from kanban columns
  // ---------------------------------------------------------------
  describe('Epic exclusion from kanban columns', () => {
    it('excludes epics from Open', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'open' });
      expect(getColumn('ready').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Backlog (deferred status)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'deferred' });
      expect(getColumn('backlog').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Backlog even with blockers', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'deferred' });
      expect(getColumn('backlog').filter(issue, blocked)).toBe(false);
    });

    it('excludes epics from Blocked (open + blocked by deps)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'open' });
      expect(getColumn('blocked').filter(issue, blocked)).toBe(false);
    });

    it('excludes epics from Blocked (blocked status)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'blocked' });
      expect(getColumn('blocked').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from In Progress', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'in_progress' });
      expect(getColumn('in_progress').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Needs Review (status)', () => {
      const issue = createMockIssue({ issue_type: 'epic', status: 'review' });
      expect(getColumn('review').filter(issue, notBlocked)).toBe(false);
    });

    it('excludes epics from Needs Review (title)', () => {
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

    it('still includes non-epic tasks in Open (regression)', () => {
      const issue = createMockIssue({ issue_type: 'task', status: 'open' });
      expect(getColumn('ready').filter(issue, notBlocked)).toBe(true);
    });

    it('still includes undefined issue_type in Open (regression)', () => {
      const issue = createMockIssue({ issue_type: undefined, status: 'open' });
      expect(getColumn('ready').filter(issue, notBlocked)).toBe(true);
    });
  });

  // ---------------------------------------------------------------
  // Blocked column identity
  // ---------------------------------------------------------------
  describe('Blocked column identity', () => {
    it('has id "blocked" at index 2', () => {
      expect(DEFAULT_COLUMNS[2].id).toBe('blocked');
    });

    it('has label "Blocked" at index 2', () => {
      expect(DEFAULT_COLUMNS[2].label).toBe('Blocked');
    });

    it('has droppableDisabled set to true', () => {
      expect(getColumn('blocked').droppableDisabled).toBe(true);
    });

    it('only allows drops to done', () => {
      expect(getColumn('blocked').allowedDropTargets).toEqual(['done']);
    });
  });
});
