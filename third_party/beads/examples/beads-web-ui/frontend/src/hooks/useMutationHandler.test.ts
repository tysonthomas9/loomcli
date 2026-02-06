/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';

import { useMutationHandler } from './useMutationHandler';
import type { MutationPayload } from '../api/sse';
import type { Issue } from '../types/issue';

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
 * Helper to create a mutation payload.
 */
function createMutationPayload(overrides: Partial<MutationPayload> = {}): MutationPayload {
  return {
    type: 'create',
    issue_id: 'test-issue-1',
    timestamp: '2025-01-23T12:00:00Z',
    ...overrides,
  };
}

/**
 * Helper to extract the resulting Map from a setIssues call.
 * Handles both direct Map values and functional updates.
 */
function getResultingMap(
  mockSetIssues: ReturnType<typeof vi.fn>,
  callIndex: number,
  currentIssues: Map<string, Issue>
): Map<string, Issue> {
  const arg = mockSetIssues.mock.calls[callIndex][0];
  if (typeof arg === 'function') {
    return arg(currentIssues);
  }
  return arg as Map<string, Issue>;
}

describe('useMutationHandler', () => {
  let mockIssues: Map<string, Issue>;
  let mockSetIssues: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockIssues = new Map();
    mockSetIssues = vi.fn();
  });

  describe('Hook initialization', () => {
    it('returns expected shape with all methods', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      expect(result.current).toHaveProperty('handleMutation');
      expect(result.current).toHaveProperty('handleMutations');
      expect(result.current).toHaveProperty('mutationCount');
      expect(result.current).toHaveProperty('lastMutationAt');

      expect(typeof result.current.handleMutation).toBe('function');
      expect(typeof result.current.handleMutations).toBe('function');
    });

    it('initial state has zero mutation count and null lastMutationAt', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      expect(result.current.mutationCount).toBe(0);
      expect(result.current.lastMutationAt).toBeNull();
    });
  });

  describe('handleMutation - create type', () => {
    it('adds new issue to state', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'new-issue-1',
        title: 'New Issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      expect(newIssuesMap.has('new-issue-1')).toBe(true);

      const createdIssue = newIssuesMap.get('new-issue-1');
      expect(createdIssue?.id).toBe('new-issue-1');
      expect(createdIssue?.title).toBe('New Issue');
      expect(createdIssue?.priority).toBe(2); // Default priority
    });

    it('sets status from new_status field on create', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'new-issue-1',
        title: 'New Issue',
        new_status: 'active',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const createdIssue = newIssuesMap.get('new-issue-1');
      expect(createdIssue?.status).toBe('active');
    });

    it('sets assignee on create when provided', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'new-issue-1',
        title: 'New Issue',
        assignee: 'user@example.com',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const createdIssue = newIssuesMap.get('new-issue-1');
      expect(createdIssue?.assignee).toBe('user@example.com');
    });

    it('calls onIssueCreated callback', () => {
      const onIssueCreated = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueCreated,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'new-issue-1',
        title: 'New Issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(onIssueCreated).toHaveBeenCalledTimes(1);
      expect(onIssueCreated).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 'new-issue-1',
          title: 'New Issue',
        })
      );
    });
  });

  describe('handleMutation - create existing issue (treated as update)', () => {
    it('treats create on existing issue as update', () => {
      const existingIssue = createTestIssue({
        id: 'existing-issue',
        title: 'Original Title',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('existing-issue', existingIssue);

      const onIssueUpdated = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueUpdated,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'existing-issue',
        title: 'Updated Title',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('existing-issue');
      expect(updatedIssue?.title).toBe('Updated Title');

      expect(onIssueUpdated).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Updated Title' }),
        existingIssue
      );
    });

    it('skips stale create on existing issue', () => {
      const existingIssue = createTestIssue({
        id: 'existing-issue',
        title: 'Current Title',
        updated_at: '2025-01-23T14:00:00Z', // Newer than mutation
      });
      mockIssues.set('existing-issue', existingIssue);

      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'existing-issue',
        title: 'Old Title',
        timestamp: '2025-01-23T12:00:00Z', // Older than issue
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        expect.stringContaining('Stale create mutation')
      );
    });
  });

  describe('handleMutation - update type', () => {
    it('modifies existing issue', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'test-issue',
        title: 'Updated Title',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      expect(updatedIssue?.title).toBe('Updated Title');
      expect(updatedIssue?.updated_at).toBe('2025-01-23T12:00:00Z');
    });

    it('calls onIssueUpdated callback with previous issue', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const onIssueUpdated = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueUpdated,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'test-issue',
        title: 'Updated Title',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(onIssueUpdated).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Updated Title' }),
        existingIssue
      );
    });

    it('skips update for unknown issue and calls onMutationSkipped', () => {
      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'unknown-issue',
        title: 'Updated Title',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        'Issue not found for update mutation'
      );
    });
  });

  describe('handleMutation - delete type', () => {
    it('removes issue from state', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'To Delete',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'delete',
        issue_id: 'test-issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      expect(newIssuesMap.has('test-issue')).toBe(false);
    });

    it('calls onIssueDeleted callback with issue ID', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'To Delete',
      });
      mockIssues.set('test-issue', existingIssue);

      const onIssueDeleted = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueDeleted,
        })
      );

      const mutation = createMutationPayload({
        type: 'delete',
        issue_id: 'test-issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(onIssueDeleted).toHaveBeenCalledWith('test-issue');
    });

    it('skips delete for unknown issue and calls onMutationSkipped', () => {
      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'delete',
        issue_id: 'unknown-issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        'Issue not found for delete mutation'
      );
    });
  });

  describe('handleMutation - status type', () => {
    it('updates status field on existing issue', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        status: 'inbox',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'status',
        issue_id: 'test-issue',
        old_status: 'inbox',
        new_status: 'active',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      expect(updatedIssue?.status).toBe('active');
      expect(updatedIssue?.updated_at).toBe('2025-01-23T12:00:00Z');
    });

    it('skips status mutation for unknown issue', () => {
      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'status',
        issue_id: 'unknown-issue',
        new_status: 'active',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        'Issue not found for status mutation'
      );
    });
  });

  describe('handleMutation - comment type', () => {
    it('updates timestamp but does not change issue content in v1', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'comment',
        issue_id: 'test-issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      // Title should remain unchanged
      expect(updatedIssue?.title).toBe('Original Title');
      // Only updated_at should be changed
      expect(updatedIssue?.updated_at).toBe('2025-01-23T12:00:00Z');
    });
  });

  describe('handleMutation - bonded type', () => {
    it('updates timestamp for bonded mutation', () => {
      const existingIssue = createTestIssue({
        id: 'child-issue',
        title: 'Child Issue',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('child-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'bonded',
        issue_id: 'child-issue',
        parent_id: 'parent-issue',
        step_count: 3,
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('child-issue');
      expect(updatedIssue?.updated_at).toBe('2025-01-23T12:00:00Z');
    });
  });

  describe('handleMutation - squashed type', () => {
    it('updates timestamp for squashed mutation', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'squashed',
        issue_id: 'test-issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      expect(updatedIssue?.updated_at).toBe('2025-01-23T12:00:00Z');
    });
  });

  describe('handleMutation - burned type', () => {
    it('updates timestamp for burned mutation', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'burned',
        issue_id: 'test-issue',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      expect(updatedIssue?.updated_at).toBe('2025-01-23T12:00:00Z');
    });
  });

  describe('Edge cases - stale mutations', () => {
    it('skips stale update mutation (older than issue)', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Current Title',
        updated_at: '2025-01-23T14:00:00Z', // Newer than mutation
      });
      mockIssues.set('test-issue', existingIssue);

      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'test-issue',
        title: 'Old Title',
        timestamp: '2025-01-23T12:00:00Z', // Older than issue
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        'Stale mutation (older than current issue)'
      );
    });

    it('skips stale status mutation', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        status: 'active',
        updated_at: '2025-01-23T14:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'status',
        issue_id: 'test-issue',
        new_status: 'inbox',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        'Stale mutation (older than current issue)'
      );
    });
  });

  describe('Edge cases - partial update data', () => {
    it('only updates title when only title is provided', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        assignee: 'original@example.com',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'test-issue',
        title: 'Updated Title',
        // No assignee field - should preserve original
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      expect(updatedIssue?.title).toBe('Updated Title');
      expect(updatedIssue?.assignee).toBe('original@example.com'); // Preserved
    });

    it('only updates assignee when only assignee is provided', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        assignee: 'original@example.com',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'test-issue',
        assignee: 'new@example.com',
        // No title field - should preserve original
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      const updatedIssue = newIssuesMap.get('test-issue');
      expect(updatedIssue?.title).toBe('Original Title'); // Preserved
      expect(updatedIssue?.assignee).toBe('new@example.com');
    });
  });

  describe('Multiple mutations - handleMutations', () => {
    it('processes batch of mutations correctly', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const mutations: MutationPayload[] = [
        createMutationPayload({
          type: 'create',
          issue_id: 'issue-1',
          title: 'Issue 1',
          timestamp: '2025-01-23T12:00:00Z',
        }),
        createMutationPayload({
          type: 'create',
          issue_id: 'issue-2',
          title: 'Issue 2',
          timestamp: '2025-01-23T12:01:00Z',
        }),
        createMutationPayload({
          type: 'create',
          issue_id: 'issue-3',
          title: 'Issue 3',
          timestamp: '2025-01-23T12:02:00Z',
        }),
      ];

      act(() => {
        result.current.handleMutations(mutations);
      });

      // Should have called setIssues 3 times (once per mutation)
      expect(mockSetIssues).toHaveBeenCalledTimes(3);
    });

    it('processes mutations in order', () => {
      const onIssueCreated = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueCreated,
        })
      );

      const mutations: MutationPayload[] = [
        createMutationPayload({
          type: 'create',
          issue_id: 'issue-1',
          title: 'First',
          timestamp: '2025-01-23T12:00:00Z',
        }),
        createMutationPayload({
          type: 'create',
          issue_id: 'issue-2',
          title: 'Second',
          timestamp: '2025-01-23T12:01:00Z',
        }),
      ];

      act(() => {
        result.current.handleMutations(mutations);
      });

      expect(onIssueCreated).toHaveBeenNthCalledWith(
        1,
        expect.objectContaining({ title: 'First' })
      );
      expect(onIssueCreated).toHaveBeenNthCalledWith(
        2,
        expect.objectContaining({ title: 'Second' })
      );
    });

    it('handles empty mutation array', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutations([]);
      });

      expect(mockSetIssues).not.toHaveBeenCalled();
    });
  });

  describe('Counter tracking - mutationCount', () => {
    it('increments correctly for create mutations', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      expect(result.current.mutationCount).toBe(0);

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'issue-1',
            title: 'Issue 1',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(result.current.mutationCount).toBe(1);

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'issue-2',
            title: 'Issue 2',
            timestamp: '2025-01-23T12:01:00Z',
          })
        );
      });

      expect(result.current.mutationCount).toBe(2);
    });

    it('increments for update mutations', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'test-issue',
            title: 'Updated',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(result.current.mutationCount).toBe(1);
    });

    it('increments for delete mutations', () => {
      const existingIssue = createTestIssue({ id: 'test-issue' });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'delete',
            issue_id: 'test-issue',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(result.current.mutationCount).toBe(1);
    });

    it('does not increment for skipped mutations', () => {
      // No issues in map - update will be skipped
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'unknown-issue',
            title: 'Updated',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(result.current.mutationCount).toBe(0);
    });
  });

  describe('Timestamp tracking - lastMutationAt', () => {
    it('updates correctly after mutation', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      expect(result.current.lastMutationAt).toBeNull();

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'issue-1',
            title: 'Issue 1',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(result.current.lastMutationAt).toBe('2025-01-23T12:00:00Z');

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'issue-2',
            title: 'Issue 2',
            timestamp: '2025-01-23T14:00:00Z',
          })
        );
      });

      expect(result.current.lastMutationAt).toBe('2025-01-23T14:00:00Z');
    });

    it('does not update for skipped mutations', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'unknown-issue',
            title: 'Updated',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(result.current.lastMutationAt).toBeNull();
    });
  });

  describe('Callback handling', () => {
    it('onIssueCreated receives the created issue', () => {
      const onIssueCreated = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueCreated,
        })
      );

      const mutation = createMutationPayload({
        type: 'create',
        issue_id: 'new-issue',
        title: 'New Issue',
        assignee: 'user@example.com',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(onIssueCreated).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 'new-issue',
          title: 'New Issue',
          assignee: 'user@example.com',
        })
      );
    });

    it('onIssueUpdated receives both updated and previous issue', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const onIssueUpdated = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueUpdated,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'test-issue',
            title: 'New Title',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(onIssueUpdated).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'New Title' }),
        expect.objectContaining({ title: 'Original Title' })
      );
    });

    it('onIssueDeleted receives the issue ID', () => {
      const existingIssue = createTestIssue({ id: 'test-issue' });
      mockIssues.set('test-issue', existingIssue);

      const onIssueDeleted = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onIssueDeleted,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'delete',
            issue_id: 'test-issue',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(onIssueDeleted).toHaveBeenCalledWith('test-issue');
    });

    it('onMutationSkipped receives mutation and reason', () => {
      const onMutationSkipped = vi.fn();
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onMutationSkipped,
        })
      );

      const mutation = createMutationPayload({
        type: 'update',
        issue_id: 'unknown-issue',
        title: 'Updated',
        timestamp: '2025-01-23T12:00:00Z',
      });

      act(() => {
        result.current.handleMutation(mutation);
      });

      expect(onMutationSkipped).toHaveBeenCalledWith(
        mutation,
        'Issue not found for update mutation'
      );
    });

    it('callbacks are optional and do not throw when not provided', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
          // No callbacks provided
        })
      );

      // Should not throw
      expect(() => {
        act(() => {
          result.current.handleMutation(
            createMutationPayload({
              type: 'create',
              issue_id: 'new-issue',
              title: 'New Issue',
              timestamp: '2025-01-23T12:00:00Z',
            })
          );
        });
      }).not.toThrow();

      expect(() => {
        act(() => {
          result.current.handleMutation(
            createMutationPayload({
              type: 'update',
              issue_id: 'unknown-issue',
              title: 'Updated',
              timestamp: '2025-01-23T12:00:00Z',
            })
          );
        });
      }).not.toThrow();
    });
  });

  describe('Method stability', () => {
    it('handleMutation and handleMutations are stable across renders when issues do not change', () => {
      const { result, rerender } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const initialHandleMutation = result.current.handleMutation;
      const initialHandleMutations = result.current.handleMutations;

      rerender();

      expect(result.current.handleMutation).toBe(initialHandleMutation);
      expect(result.current.handleMutations).toBe(initialHandleMutations);
    });

    it('handleMutation updates when issues change', () => {
      const { result, rerender } = renderHook(
        ({ issues }: { issues: Map<string, Issue> }) =>
          useMutationHandler({
            issues,
            setIssues: mockSetIssues,
          }),
        { initialProps: { issues: mockIssues } }
      );

      const initialHandleMutation = result.current.handleMutation;

      // Create new issues map
      const newIssues = new Map<string, Issue>();
      newIssues.set('issue-1', createTestIssue({ id: 'issue-1' }));

      rerender({ issues: newIssues });

      // handleMutation should be a new reference due to dependency on issues
      expect(result.current.handleMutation).not.toBe(initialHandleMutation);
    });
  });

  describe('Immutability', () => {
    it('does not mutate the original issues map', () => {
      const originalIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', originalIssue);

      const originalMap = new Map(mockIssues);
      const originalIssueClone = { ...originalIssue };

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'test-issue',
            title: 'Updated',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      // Original map should be unchanged
      expect(mockIssues.size).toBe(originalMap.size);
      expect(mockIssues.get('test-issue')?.title).toBe(originalIssueClone.title);
    });

    it('creates new Map instance for setIssues', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'test-issue',
            title: 'Updated',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      const newIssuesMap = getResultingMap(mockSetIssues, 0, mockIssues);
      expect(newIssuesMap).not.toBe(mockIssues);
    });
  });

  describe('Race condition fix - functional updates', () => {
    it('setIssues receives a function, not a direct Map (for race condition safety)', () => {
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'new-issue',
            title: 'New Issue',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      // The argument should be a function, not a direct Map
      const arg = mockSetIssues.mock.calls[0][0];
      expect(typeof arg).toBe('function');
    });

    it('functional update correctly uses previous state (simulates race condition scenario)', () => {
      // This test verifies that functional updates operate on the PREVIOUS state,
      // not the closure-captured state, which prevents race conditions.
      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      // Simulate two rapid mutations
      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'issue-1',
            title: 'First Issue',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'issue-2',
            title: 'Second Issue',
            timestamp: '2025-01-23T12:00:01Z',
          })
        );
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(2);

      // Both calls should be functions
      expect(typeof mockSetIssues.mock.calls[0][0]).toBe('function');
      expect(typeof mockSetIssues.mock.calls[1][0]).toBe('function');

      // Simulate React's batched update by chaining the functional updates
      // This mimics what React does: apply each update function to the previous result
      const emptyMap = new Map<string, Issue>();
      const afterFirst = mockSetIssues.mock.calls[0][0](emptyMap);
      const afterSecond = mockSetIssues.mock.calls[1][0](afterFirst);

      // Both issues should be present in the final state
      expect(afterSecond.has('issue-1')).toBe(true);
      expect(afterSecond.has('issue-2')).toBe(true);
      expect(afterSecond.get('issue-1')?.title).toBe('First Issue');
      expect(afterSecond.get('issue-2')?.title).toBe('Second Issue');
    });

    it('functional update preserves existing issues when adding new ones', () => {
      const existingIssue = createTestIssue({
        id: 'existing-issue',
        title: 'Existing Issue',
        updated_at: '2025-01-23T09:00:00Z',
      });
      mockIssues.set('existing-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'create',
            issue_id: 'new-issue',
            title: 'New Issue',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      // Get the functional update
      const updateFn = mockSetIssues.mock.calls[0][0] as (
        prev: Map<string, Issue>
      ) => Map<string, Issue>;

      // Apply it to a state that includes the existing issue
      const previousState = new Map<string, Issue>();
      previousState.set('existing-issue', existingIssue);

      const newState = updateFn(previousState);

      // Both issues should be in the new state
      expect(newState.has('existing-issue')).toBe(true);
      expect(newState.has('new-issue')).toBe(true);
      expect(newState.get('existing-issue')?.title).toBe('Existing Issue');
      expect(newState.get('new-issue')?.title).toBe('New Issue');
    });

    it('delete mutation uses functional update', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Test Issue',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'delete',
            issue_id: 'test-issue',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      expect(typeof mockSetIssues.mock.calls[0][0]).toBe('function');

      // Apply the update function
      const updateFn = mockSetIssues.mock.calls[0][0] as (
        prev: Map<string, Issue>
      ) => Map<string, Issue>;
      const previousState = new Map<string, Issue>();
      previousState.set('test-issue', existingIssue);
      previousState.set('other-issue', createTestIssue({ id: 'other-issue' }));

      const newState = updateFn(previousState);

      // Only the deleted issue should be removed
      expect(newState.has('test-issue')).toBe(false);
      expect(newState.has('other-issue')).toBe(true);
    });

    it('update mutation uses functional update', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        title: 'Original Title',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'update',
            issue_id: 'test-issue',
            title: 'Updated Title',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      expect(typeof mockSetIssues.mock.calls[0][0]).toBe('function');
    });

    it('status mutation uses functional update', () => {
      const existingIssue = createTestIssue({
        id: 'test-issue',
        status: 'open',
        updated_at: '2025-01-23T10:00:00Z',
      });
      mockIssues.set('test-issue', existingIssue);

      const { result } = renderHook(() =>
        useMutationHandler({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      act(() => {
        result.current.handleMutation(
          createMutationPayload({
            type: 'status',
            issue_id: 'test-issue',
            new_status: 'active',
            timestamp: '2025-01-23T12:00:00Z',
          })
        );
      });

      expect(mockSetIssues).toHaveBeenCalledTimes(1);
      expect(typeof mockSetIssues.mock.calls[0][0]).toBe('function');
    });
  });
});
