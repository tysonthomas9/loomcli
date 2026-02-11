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

import { DEFAULT_COLUMNS, createColumns } from '../columnConfigs';
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

    it('does NOT match open issues with [Need Review] in title (legacy prefix removed)', () => {
      const issue = createMockIssue({
        title: '[Need Review] Update docs',
        status: 'open',
      });
      expect(review.filter(issue, notBlocked)).toBe(false);
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

    it('matches open issues with no blockers', () => {
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

    it('matches open issues even with [Need Review] in title (legacy prefix no longer used)', () => {
      const issue = createMockIssue({
        title: '[Need Review] Refactor API',
        status: 'open',
      });
      expect(open.filter(issue, notBlocked)).toBe(true);
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
  // Done column defaultLimit
  // ---------------------------------------------------------------
  describe('Done column defaultLimit', () => {
    it('has defaultLimit of 10', () => {
      expect(getColumn('done').defaultLimit).toBe(10);
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

  // ---------------------------------------------------------------
  // createColumns() factory function
  // ---------------------------------------------------------------
  describe('createColumns()', () => {
    /**
     * Helper to look up a column by id from a given column array.
     */
    function getColumnFrom(columns: ReturnType<typeof createColumns>, id: string) {
      const col = columns.find((c) => c.id === id);
      if (!col) throw new Error(`Column "${id}" not found`);
      return col;
    }

    describe('no arguments (default behavior)', () => {
      it('returns the same column structure as DEFAULT_COLUMNS', () => {
        const columns = createColumns();
        expect(columns.map((c) => c.id)).toEqual(DEFAULT_COLUMNS.map((c) => c.id));
        expect(columns.map((c) => c.label)).toEqual(DEFAULT_COLUMNS.map((c) => c.label));
      });

      it('filters out epics from Backlog', () => {
        const columns = createColumns();
        const backlog = getColumnFrom(columns, 'backlog');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'deferred' });
        expect(backlog.filter(epicIssue, notBlocked)).toBe(false);
      });

      it('filters out epics from Open', () => {
        const columns = createColumns();
        const open = getColumnFrom(columns, 'ready');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'open' });
        expect(open.filter(epicIssue, notBlocked)).toBe(false);
      });

      it('filters out epics from Blocked', () => {
        const columns = createColumns();
        const blockedCol = getColumnFrom(columns, 'blocked');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'open' });
        expect(blockedCol.filter(epicIssue, blocked)).toBe(false);
      });

      it('filters out epics from In Progress', () => {
        const columns = createColumns();
        const inProgress = getColumnFrom(columns, 'in_progress');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'in_progress' });
        expect(inProgress.filter(epicIssue, notBlocked)).toBe(false);
      });

      it('filters out epics from Needs Review', () => {
        const columns = createColumns();
        const review = getColumnFrom(columns, 'review');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'review' });
        expect(review.filter(epicIssue, notBlocked)).toBe(false);
      });

      it('allows epics in Done', () => {
        const columns = createColumns();
        const done = getColumnFrom(columns, 'done');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'closed' });
        expect(done.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('still allows non-epic issues through all columns', () => {
        const columns = createColumns();
        const taskOpen = createMockIssue({ issue_type: 'task', status: 'open' });
        const taskDeferred = createMockIssue({ issue_type: 'task', status: 'deferred' });
        const taskBlocked = createMockIssue({ issue_type: 'task', status: 'open' });
        const taskInProgress = createMockIssue({ issue_type: 'task', status: 'in_progress' });
        const taskReview = createMockIssue({ issue_type: 'task', status: 'review' });
        const taskClosed = createMockIssue({ issue_type: 'task', status: 'closed' });

        expect(getColumnFrom(columns, 'ready').filter(taskOpen, notBlocked)).toBe(true);
        expect(getColumnFrom(columns, 'backlog').filter(taskDeferred, notBlocked)).toBe(true);
        expect(getColumnFrom(columns, 'blocked').filter(taskBlocked, blocked)).toBe(true);
        expect(getColumnFrom(columns, 'in_progress').filter(taskInProgress, notBlocked)).toBe(true);
        expect(getColumnFrom(columns, 'review').filter(taskReview, notBlocked)).toBe(true);
        expect(getColumnFrom(columns, 'done').filter(taskClosed, notBlocked)).toBe(true);
      });
    });

    describe('with includeEpics: true', () => {
      it('allows epics in Backlog (deferred)', () => {
        const columns = createColumns({ includeEpics: true });
        const backlog = getColumnFrom(columns, 'backlog');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'deferred' });
        expect(backlog.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('allows epics in Open', () => {
        const columns = createColumns({ includeEpics: true });
        const open = getColumnFrom(columns, 'ready');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'open' });
        expect(open.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('allows epics in Blocked (open + blocked by deps)', () => {
        const columns = createColumns({ includeEpics: true });
        const blockedCol = getColumnFrom(columns, 'blocked');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'open' });
        expect(blockedCol.filter(epicIssue, blocked)).toBe(true);
      });

      it('allows epics in Blocked (blocked status)', () => {
        const columns = createColumns({ includeEpics: true });
        const blockedCol = getColumnFrom(columns, 'blocked');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'blocked' });
        expect(blockedCol.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('allows epics in In Progress', () => {
        const columns = createColumns({ includeEpics: true });
        const inProgress = getColumnFrom(columns, 'in_progress');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'in_progress' });
        expect(inProgress.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('allows epics in Needs Review (status)', () => {
        const columns = createColumns({ includeEpics: true });
        const review = getColumnFrom(columns, 'review');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'review' });
        expect(review.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('allows epics in Done', () => {
        const columns = createColumns({ includeEpics: true });
        const done = getColumnFrom(columns, 'done');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'closed' });
        expect(done.filter(epicIssue, notBlocked)).toBe(true);
      });

      it('still respects other filter logic (status matching)', () => {
        const columns = createColumns({ includeEpics: true });
        // An epic with status=open should NOT match Backlog (which requires deferred)
        const epicOpen = createMockIssue({ issue_type: 'epic', status: 'open' });
        expect(getColumnFrom(columns, 'backlog').filter(epicOpen, notBlocked)).toBe(false);
      });

      it('still allows non-epic issues through all columns', () => {
        const columns = createColumns({ includeEpics: true });
        const taskOpen = createMockIssue({ issue_type: 'task', status: 'open' });
        expect(getColumnFrom(columns, 'ready').filter(taskOpen, notBlocked)).toBe(true);
      });
    });

    describe('with includeEpics: false (explicit)', () => {
      it('filters out epics from Open (same as default)', () => {
        const columns = createColumns({ includeEpics: false });
        const open = getColumnFrom(columns, 'ready');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'open' });
        expect(open.filter(epicIssue, notBlocked)).toBe(false);
      });

      it('filters out epics from In Progress (same as default)', () => {
        const columns = createColumns({ includeEpics: false });
        const inProgress = getColumnFrom(columns, 'in_progress');
        const epicIssue = createMockIssue({ issue_type: 'epic', status: 'in_progress' });
        expect(inProgress.filter(epicIssue, notBlocked)).toBe(false);
      });
    });

    describe('column structure consistency', () => {
      it('returns 6 columns regardless of includeEpics', () => {
        expect(createColumns().length).toBe(6);
        expect(createColumns({ includeEpics: true }).length).toBe(6);
        expect(createColumns({ includeEpics: false }).length).toBe(6);
      });

      it('returns columns with the same IDs regardless of includeEpics', () => {
        const defaultIds = createColumns().map((c) => c.id);
        const withEpicsIds = createColumns({ includeEpics: true }).map((c) => c.id);
        const withoutEpicsIds = createColumns({ includeEpics: false }).map((c) => c.id);

        expect(defaultIds).toEqual(['backlog', 'ready', 'blocked', 'in_progress', 'review', 'done']);
        expect(withEpicsIds).toEqual(defaultIds);
        expect(withoutEpicsIds).toEqual(defaultIds);
      });

      it('preserves column metadata (style, droppableDisabled, allowedDropTargets)', () => {
        const withEpics = createColumns({ includeEpics: true });
        const backlog = getColumnFrom(withEpics, 'backlog');
        const blockedCol = getColumnFrom(withEpics, 'blocked');
        const done = getColumnFrom(withEpics, 'done');

        expect(backlog.style).toBe('muted');
        expect(backlog.droppableDisabled).toBe(true);
        expect(backlog.allowedDropTargets).toEqual(['done']);

        expect(blockedCol.style).toBe('muted');
        expect(blockedCol.droppableDisabled).toBe(true);
        expect(blockedCol.allowedDropTargets).toEqual(['done']);

        expect(done.defaultLimit).toBe(10);
      });

      it('returns independent column arrays (no shared references)', () => {
        const columns1 = createColumns();
        const columns2 = createColumns({ includeEpics: true });

        expect(columns1).not.toBe(columns2);
        expect(columns1[0]).not.toBe(columns2[0]);
      });
    });
  });

  // ---------------------------------------------------------------
  // DEFAULT_COLUMNS backward compatibility
  // ---------------------------------------------------------------
  describe('DEFAULT_COLUMNS backward compatibility', () => {
    it('DEFAULT_COLUMNS is defined as createColumns() with no args', () => {
      // DEFAULT_COLUMNS should produce the same column IDs and labels
      const freshDefault = createColumns();
      expect(DEFAULT_COLUMNS.map((c) => c.id)).toEqual(freshDefault.map((c) => c.id));
      expect(DEFAULT_COLUMNS.map((c) => c.label)).toEqual(freshDefault.map((c) => c.label));
    });

    it('DEFAULT_COLUMNS filters epics from all columns except Done', () => {
      const epicOpen = createMockIssue({ issue_type: 'epic', status: 'open' });
      const epicDeferred = createMockIssue({ issue_type: 'epic', status: 'deferred' });
      const epicInProgress = createMockIssue({ issue_type: 'epic', status: 'in_progress' });
      const epicReview = createMockIssue({ issue_type: 'epic', status: 'review' });
      const epicClosed = createMockIssue({ issue_type: 'epic', status: 'closed' });

      expect(getColumn('backlog').filter(epicDeferred, notBlocked)).toBe(false);
      expect(getColumn('ready').filter(epicOpen, notBlocked)).toBe(false);
      expect(getColumn('blocked').filter(epicOpen, blocked)).toBe(false);
      expect(getColumn('in_progress').filter(epicInProgress, notBlocked)).toBe(false);
      expect(getColumn('review').filter(epicReview, notBlocked)).toBe(false);
      expect(getColumn('done').filter(epicClosed, notBlocked)).toBe(true);
    });
  });
});
