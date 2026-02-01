/**
 * Unit tests for type guard functions and type constants.
 */

import { describe, it, expect } from 'vitest';

import { vi } from 'vitest';

import {
  // Status exports
  isKnownStatus,
  isValidStatus,
  KNOWN_STATUSES,
  StatusOpen,
  StatusInProgress,
  StatusBlocked,
  StatusDeferred,
  StatusClosed,
  StatusReview,
  StatusTombstone,
  StatusPinned,
  StatusHooked,
  // Issue type exports
  isKnownIssueType,
  isValidIssueType,
  KNOWN_ISSUE_TYPES,
  TypeBug,
  TypeFeature,
  TypeTask,
  TypeEpic,
  TypeChore,
  // API exports
  isApiSuccess,
  isApiError,
  // Mutation exports
  MutationCreate,
  MutationUpdate,
  MutationDelete,
  MutationComment,
  MutationStatus,
  MutationBonded,
  MutationSquashed,
  MutationBurned,
  createMutationEvent,
  isCreateMutation,
  isUpdateMutation,
  isDeleteMutation,
  isCommentMutation,
  isStatusMutation,
  isBondedMutation,
  isSquashedMutation,
  isBurnedMutation,
} from '../index';

import type { ApiResult, MutationPayload, MutationEvent } from '../index';

describe('Status type guards', () => {
  describe('isKnownStatus', () => {
    it('returns true for all known statuses', () => {
      expect(isKnownStatus('open')).toBe(true);
      expect(isKnownStatus('in_progress')).toBe(true);
      expect(isKnownStatus('blocked')).toBe(true);
      expect(isKnownStatus('deferred')).toBe(true);
      expect(isKnownStatus('closed')).toBe(true);
      expect(isKnownStatus('review')).toBe(true);
      expect(isKnownStatus('tombstone')).toBe(true);
      expect(isKnownStatus('pinned')).toBe(true);
      expect(isKnownStatus('hooked')).toBe(true);
    });

    it('returns false for custom/unknown statuses', () => {
      expect(isKnownStatus('custom_status')).toBe(false);
      expect(isKnownStatus('pending')).toBe(false);
      expect(isKnownStatus('OPEN')).toBe(false); // case sensitive
      expect(isKnownStatus('')).toBe(false);
    });
  });

  describe('isValidStatus', () => {
    it('returns true for non-empty strings', () => {
      expect(isValidStatus('open')).toBe(true);
      expect(isValidStatus('custom_status')).toBe(true);
      expect(isValidStatus('any-string')).toBe(true);
    });

    it('returns false for empty string', () => {
      expect(isValidStatus('')).toBe(false);
    });

    it('returns false for non-string values', () => {
      expect(isValidStatus(null)).toBe(false);
      expect(isValidStatus(undefined)).toBe(false);
      expect(isValidStatus(123)).toBe(false);
      expect(isValidStatus({})).toBe(false);
      expect(isValidStatus([])).toBe(false);
      expect(isValidStatus(true)).toBe(false);
    });
  });
});

describe('Status constants', () => {
  it('KNOWN_STATUSES contains all expected statuses', () => {
    expect(KNOWN_STATUSES).toEqual([
      'open',
      'in_progress',
      'blocked',
      'deferred',
      'closed',
      'review',
      'tombstone',
      'pinned',
      'hooked',
    ]);
  });

  it('KNOWN_STATUSES is readonly (has 9 items)', () => {
    expect(KNOWN_STATUSES.length).toBe(9);
  });

  it('status constants have correct values', () => {
    expect(StatusOpen).toBe('open');
    expect(StatusInProgress).toBe('in_progress');
    expect(StatusBlocked).toBe('blocked');
    expect(StatusDeferred).toBe('deferred');
    expect(StatusClosed).toBe('closed');
    expect(StatusReview).toBe('review');
    expect(StatusTombstone).toBe('tombstone');
    expect(StatusPinned).toBe('pinned');
    expect(StatusHooked).toBe('hooked');
  });
});

describe('IssueType type guards', () => {
  describe('isKnownIssueType', () => {
    it('returns true for all known issue types', () => {
      expect(isKnownIssueType('bug')).toBe(true);
      expect(isKnownIssueType('feature')).toBe(true);
      expect(isKnownIssueType('task')).toBe(true);
      expect(isKnownIssueType('epic')).toBe(true);
      expect(isKnownIssueType('chore')).toBe(true);
    });

    it('returns false for custom/unknown issue types', () => {
      expect(isKnownIssueType('story')).toBe(false);
      expect(isKnownIssueType('subtask')).toBe(false);
      expect(isKnownIssueType('BUG')).toBe(false); // case sensitive
      expect(isKnownIssueType('')).toBe(false);
    });
  });

  describe('isValidIssueType', () => {
    it('returns true for non-empty strings', () => {
      expect(isValidIssueType('bug')).toBe(true);
      expect(isValidIssueType('custom_type')).toBe(true);
      expect(isValidIssueType('any-string')).toBe(true);
    });

    it('returns false for empty string', () => {
      expect(isValidIssueType('')).toBe(false);
    });

    it('returns false for non-string values', () => {
      expect(isValidIssueType(null)).toBe(false);
      expect(isValidIssueType(undefined)).toBe(false);
      expect(isValidIssueType(123)).toBe(false);
      expect(isValidIssueType({})).toBe(false);
      expect(isValidIssueType([])).toBe(false);
      expect(isValidIssueType(true)).toBe(false);
    });
  });
});

describe('IssueType constants', () => {
  it('KNOWN_ISSUE_TYPES contains all expected types', () => {
    expect(KNOWN_ISSUE_TYPES).toEqual([
      'bug',
      'feature',
      'task',
      'epic',
      'chore',
    ]);
  });

  it('KNOWN_ISSUE_TYPES is readonly (has 5 items)', () => {
    expect(KNOWN_ISSUE_TYPES.length).toBe(5);
  });

  it('issue type constants have correct values', () => {
    expect(TypeBug).toBe('bug');
    expect(TypeFeature).toBe('feature');
    expect(TypeTask).toBe('task');
    expect(TypeEpic).toBe('epic');
    expect(TypeChore).toBe('chore');
  });
});

describe('API type guards', () => {
  describe('isApiSuccess', () => {
    it('returns true for success responses', () => {
      const successResult: ApiResult<string> = {
        success: true,
        data: 'test data',
      };
      expect(isApiSuccess(successResult)).toBe(true);
    });

    it('returns false for error responses', () => {
      const errorResult: ApiResult<string> = {
        success: false,
        error: 'Something went wrong',
      };
      expect(isApiSuccess(errorResult)).toBe(false);
    });

    it('correctly narrows type to access data', () => {
      const result: ApiResult<{ id: number }> = {
        success: true,
        data: { id: 42 },
      };

      if (isApiSuccess(result)) {
        // TypeScript should allow accessing data here
        expect(result.data.id).toBe(42);
      }
    });
  });

  describe('isApiError', () => {
    it('returns true for error responses', () => {
      const errorResult: ApiResult<string> = {
        success: false,
        error: 'Something went wrong',
      };
      expect(isApiError(errorResult)).toBe(true);
    });

    it('returns false for success responses', () => {
      const successResult: ApiResult<string> = {
        success: true,
        data: 'test data',
      };
      expect(isApiError(successResult)).toBe(false);
    });

    it('correctly narrows type to access error properties', () => {
      const result: ApiResult<string> = {
        success: false,
        error: 'Not found',
        code: 'NOT_FOUND',
        details: { resource: 'user' },
      };

      if (isApiError(result)) {
        // TypeScript should allow accessing error properties here
        expect(result.error).toBe('Not found');
        expect(result.code).toBe('NOT_FOUND');
        expect(result.details).toEqual({ resource: 'user' });
      }
    });
  });

  describe('isApiSuccess and isApiError are mutually exclusive', () => {
    it('success response: isApiSuccess=true, isApiError=false', () => {
      const result: ApiResult<number> = { success: true, data: 123 };
      expect(isApiSuccess(result)).toBe(true);
      expect(isApiError(result)).toBe(false);
    });

    it('error response: isApiSuccess=false, isApiError=true', () => {
      const result: ApiResult<number> = { success: false, error: 'Error' };
      expect(isApiSuccess(result)).toBe(false);
      expect(isApiError(result)).toBe(true);
    });
  });
});

describe('Mutation type constants', () => {
  it('mutation type constants have correct values', () => {
    expect(MutationCreate).toBe('create');
    expect(MutationUpdate).toBe('update');
    expect(MutationDelete).toBe('delete');
    expect(MutationComment).toBe('comment');
    expect(MutationStatus).toBe('status');
    expect(MutationBonded).toBe('bonded');
    expect(MutationSquashed).toBe('squashed');
    expect(MutationBurned).toBe('burned');
  });
});

describe('createMutationEvent', () => {
  it('wraps payload with received_at timestamp', () => {
    const now = new Date('2024-01-15T12:00:00Z');
    vi.setSystemTime(now);

    const payload: MutationPayload = {
      type: 'create',
      issue_id: 'bd-123',
      timestamp: '2024-01-15T10:00:00Z',
    };

    const event = createMutationEvent(payload);
    expect(event.mutation).toBe(payload);
    expect(event.received_at).toBe('2024-01-15T12:00:00.000Z');

    vi.useRealTimers();
  });

  it('preserves payload unchanged', () => {
    const payload: MutationPayload = {
      type: 'status',
      issue_id: 'bd-456',
      timestamp: '2024-01-15T10:00:00Z',
      old_status: 'open',
      new_status: 'in_progress',
      actor: 'user@example.com',
    };

    const event = createMutationEvent(payload);
    expect(event.mutation).toBe(payload);
    expect(event.mutation.type).toBe('status');
    expect(event.mutation.old_status).toBe('open');
    expect(event.mutation.new_status).toBe('in_progress');
    expect(event.mutation.actor).toBe('user@example.com');
  });

  it('does not set sequence by default', () => {
    const payload: MutationPayload = {
      type: 'create',
      issue_id: 'bd-789',
      timestamp: '2024-01-15T10:00:00Z',
    };

    const event = createMutationEvent(payload);
    expect(event.sequence).toBeUndefined();
  });
});

describe('Mutation type guards', () => {
  const createTestEvent = (type: MutationPayload['type']): MutationEvent => ({
    mutation: {
      type,
      issue_id: 'bd-test',
      timestamp: '2024-01-15T10:00:00Z',
    },
    received_at: '2024-01-15T12:00:00Z',
  });

  describe('isCreateMutation', () => {
    it('returns true for create type', () => {
      const event = createTestEvent('create');
      expect(isCreateMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isCreateMutation(createTestEvent('update'))).toBe(false);
      expect(isCreateMutation(createTestEvent('delete'))).toBe(false);
      expect(isCreateMutation(createTestEvent('status'))).toBe(false);
    });
  });

  describe('isUpdateMutation', () => {
    it('returns true for update type', () => {
      const event = createTestEvent('update');
      expect(isUpdateMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isUpdateMutation(createTestEvent('create'))).toBe(false);
      expect(isUpdateMutation(createTestEvent('delete'))).toBe(false);
    });
  });

  describe('isDeleteMutation', () => {
    it('returns true for delete type', () => {
      const event = createTestEvent('delete');
      expect(isDeleteMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isDeleteMutation(createTestEvent('create'))).toBe(false);
      expect(isDeleteMutation(createTestEvent('update'))).toBe(false);
    });
  });

  describe('isCommentMutation', () => {
    it('returns true for comment type', () => {
      const event = createTestEvent('comment');
      expect(isCommentMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isCommentMutation(createTestEvent('create'))).toBe(false);
      expect(isCommentMutation(createTestEvent('status'))).toBe(false);
    });
  });

  describe('isStatusMutation', () => {
    it('returns true for status type', () => {
      const event: MutationEvent = {
        mutation: {
          type: 'status',
          issue_id: 'bd-test',
          timestamp: '2024-01-15T10:00:00Z',
          old_status: 'open',
          new_status: 'in_progress',
        },
        received_at: '2024-01-15T12:00:00Z',
      };
      expect(isStatusMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isStatusMutation(createTestEvent('create'))).toBe(false);
      expect(isStatusMutation(createTestEvent('update'))).toBe(false);
    });
  });

  describe('isBondedMutation', () => {
    it('returns true for bonded type', () => {
      const event: MutationEvent = {
        mutation: {
          type: 'bonded',
          issue_id: 'bd-test',
          timestamp: '2024-01-15T10:00:00Z',
          parent_id: 'bd-parent',
          step_count: 3,
        },
        received_at: '2024-01-15T12:00:00Z',
      };
      expect(isBondedMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isBondedMutation(createTestEvent('create'))).toBe(false);
      expect(isBondedMutation(createTestEvent('status'))).toBe(false);
    });
  });

  describe('isSquashedMutation', () => {
    it('returns true for squashed type', () => {
      const event = createTestEvent('squashed');
      expect(isSquashedMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isSquashedMutation(createTestEvent('create'))).toBe(false);
      expect(isSquashedMutation(createTestEvent('bonded'))).toBe(false);
    });
  });

  describe('isBurnedMutation', () => {
    it('returns true for burned type', () => {
      const event = createTestEvent('burned');
      expect(isBurnedMutation(event)).toBe(true);
    });

    it('returns false for other types', () => {
      expect(isBurnedMutation(createTestEvent('create'))).toBe(false);
      expect(isBurnedMutation(createTestEvent('squashed'))).toBe(false);
    });
  });

  describe('type guards are mutually exclusive', () => {
    it('only one type guard returns true for each mutation type', () => {
      const types: MutationPayload['type'][] = [
        'create',
        'update',
        'delete',
        'comment',
        'status',
        'bonded',
        'squashed',
        'burned',
      ];

      const guards = [
        isCreateMutation,
        isUpdateMutation,
        isDeleteMutation,
        isCommentMutation,
        isStatusMutation,
        isBondedMutation,
        isSquashedMutation,
        isBurnedMutation,
      ];

      for (const type of types) {
        const event = createTestEvent(type);
        const trueCount = guards.filter((guard) => guard(event)).length;
        expect(trueCount).toBe(1);
      }
    });
  });
});
