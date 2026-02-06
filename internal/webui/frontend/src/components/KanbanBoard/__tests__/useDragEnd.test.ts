/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useDragEnd handler factory.
 */

import type { DragEndEvent, Active, Over } from '@dnd-kit/core';
import { describe, it, expect, vi, beforeEach } from 'vitest';

import * as api from '@/api';
import type { Issue, Status } from '@/types';

import { createDragEndHandler, isDraggableData, isDroppableData } from '../useDragEnd';

// Mock the API module
vi.mock('@/api', () => ({
  updateIssue: vi.fn(),
}));

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

/**
 * Create a mock DragEndEvent for testing.
 */
function createMockDragEvent(
  issue: Issue | null,
  newStatus: Status | null,
  opts?: { activeType?: string; hasOverData?: boolean }
): DragEndEvent {
  const active: Active = {
    id: issue?.id ?? 'test-id',
    data: {
      current: issue ? { issue, type: opts?.activeType ?? 'issue' } : undefined,
    },
    rect: { current: { initial: null, translated: null } },
  };

  const over: Over | null = newStatus
    ? {
        id: newStatus,
        data: {
          current: opts?.hasOverData === false ? undefined : { status: newStatus },
        },
        disabled: false,
        rect: { width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0 },
      }
    : null;

  return {
    active,
    over,
    activatorEvent: null as unknown as Event,
    collisions: null,
    delta: { x: 0, y: 0 },
  };
}

describe('useDragEnd', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.updateIssue).mockResolvedValue(createMockIssue());
  });

  describe('isDraggableData type guard', () => {
    it('returns false for null', () => {
      expect(isDraggableData(null)).toBe(false);
    });

    it('returns false for undefined', () => {
      expect(isDraggableData(undefined)).toBe(false);
    });

    it('returns false for non-object', () => {
      expect(isDraggableData('string')).toBe(false);
      expect(isDraggableData(123)).toBe(false);
      expect(isDraggableData(true)).toBe(false);
    });

    it('returns false for object without issue', () => {
      expect(isDraggableData({ type: 'issue' })).toBe(false);
    });

    it('returns false for object without type', () => {
      expect(isDraggableData({ issue: createMockIssue() })).toBe(false);
    });

    it('returns false for wrong type value', () => {
      expect(isDraggableData({ issue: createMockIssue(), type: 'card' })).toBe(false);
      expect(isDraggableData({ issue: createMockIssue(), type: 'other' })).toBe(false);
    });

    it('returns true for valid DraggableData', () => {
      const data = { issue: createMockIssue(), type: 'issue' };
      expect(isDraggableData(data)).toBe(true);
    });

    it('returns true even with extra properties', () => {
      const data = {
        issue: createMockIssue(),
        type: 'issue',
        extra: 'property',
      };
      expect(isDraggableData(data)).toBe(true);
    });
  });

  describe('isDroppableData type guard', () => {
    it('returns false for null', () => {
      expect(isDroppableData(null)).toBe(false);
    });

    it('returns false for undefined', () => {
      expect(isDroppableData(undefined)).toBe(false);
    });

    it('returns false for non-object', () => {
      expect(isDroppableData('string')).toBe(false);
      expect(isDroppableData(123)).toBe(false);
    });

    it('returns false for object without status', () => {
      expect(isDroppableData({})).toBe(false);
      expect(isDroppableData({ other: 'value' })).toBe(false);
    });

    it('returns false for non-string status', () => {
      expect(isDroppableData({ status: 123 })).toBe(false);
      expect(isDroppableData({ status: null })).toBe(false);
      expect(isDroppableData({ status: { value: 'open' } })).toBe(false);
    });

    it('returns true for valid DroppableData', () => {
      expect(isDroppableData({ status: 'open' })).toBe(true);
      expect(isDroppableData({ status: 'in_progress' })).toBe(true);
      expect(isDroppableData({ status: 'custom_status' })).toBe(true);
    });

    it('returns true for empty string status', () => {
      // Empty string is technically a valid string
      expect(isDroppableData({ status: '' })).toBe(true);
    });
  });

  describe('createDragEndHandler', () => {
    describe('early returns (validation)', () => {
      it('returns early when over is null (no drop target)', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, null);

        await handler(event);

        expect(onIssueStatusChange).not.toHaveBeenCalled();
        expect(api.updateIssue).not.toHaveBeenCalled();
      });

      it('returns early when active data is missing issue', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const event = createMockDragEvent(null, 'in_progress');

        await handler(event);

        expect(onIssueStatusChange).not.toHaveBeenCalled();
        expect(api.updateIssue).not.toHaveBeenCalled();
      });

      it('returns early when active data has wrong type', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'in_progress', { activeType: 'card' });

        await handler(event);

        expect(onIssueStatusChange).not.toHaveBeenCalled();
        expect(api.updateIssue).not.toHaveBeenCalled();
      });

      it('returns early when over data is missing status', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'in_progress', { hasOverData: false });

        await handler(event);

        expect(onIssueStatusChange).not.toHaveBeenCalled();
        expect(api.updateIssue).not.toHaveBeenCalled();
      });

      it('returns early when dropping on same column (same status)', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'open');

        await handler(event);

        expect(onIssueStatusChange).not.toHaveBeenCalled();
        expect(api.updateIssue).not.toHaveBeenCalled();
      });
    });

    describe('callback execution', () => {
      it('calls onIssueStatusChange with correct arguments', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ id: 'test-123', status: 'open' });
        const event = createMockDragEvent(issue, 'in_progress');

        await handler(event);

        expect(onIssueStatusChange).toHaveBeenCalledTimes(1);
        expect(onIssueStatusChange).toHaveBeenCalledWith('test-123', 'in_progress');
      });

      it('calls onIssueStatusChange BEFORE updateIssue API call', async () => {
        const callOrder: string[] = [];

        const onIssueStatusChange = vi.fn(() => {
          callOrder.push('onIssueStatusChange');
        });

        vi.mocked(api.updateIssue).mockImplementation(async () => {
          callOrder.push('updateIssue');
          return createMockIssue();
        });

        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        expect(callOrder).toEqual(['onIssueStatusChange', 'updateIssue']);
      });

      it('calls onSuccess after successful API call', async () => {
        const onIssueStatusChange = vi.fn();
        const onSuccess = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange, onSuccess });

        const issue = createMockIssue({ id: 'success-test', status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        expect(onSuccess).toHaveBeenCalledTimes(1);
        expect(onSuccess).toHaveBeenCalledWith(
          expect.objectContaining({ id: 'success-test' }),
          'closed'
        );
      });

      it('does not call onSuccess when API call fails', async () => {
        vi.mocked(api.updateIssue).mockRejectedValue(new Error('API Error'));

        const onIssueStatusChange = vi.fn();
        const onSuccess = vi.fn();
        const onError = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange, onSuccess, onError });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        expect(onSuccess).not.toHaveBeenCalled();
      });

      it('calls onError with error, issue, and previousStatus on API failure', async () => {
        const apiError = new Error('Network error');
        vi.mocked(api.updateIssue).mockRejectedValue(apiError);

        const onIssueStatusChange = vi.fn();
        const onError = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange, onError });

        const issue = createMockIssue({ id: 'error-test', status: 'in_progress' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        expect(onError).toHaveBeenCalledTimes(1);
        expect(onError).toHaveBeenCalledWith(
          apiError,
          expect.objectContaining({ id: 'error-test' }),
          'in_progress'
        );
      });

      it('does not throw when onSuccess is not provided', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await expect(handler(event)).resolves.not.toThrow();
      });

      it('does not throw when onError is not provided and API fails', async () => {
        vi.mocked(api.updateIssue).mockRejectedValue(new Error('API Error'));

        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await expect(handler(event)).resolves.not.toThrow();
      });
    });

    describe('API interaction', () => {
      it('calls updateIssue with correct issue ID', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ id: 'api-test-id', status: 'open' });
        const event = createMockDragEvent(issue, 'in_progress');

        await handler(event);

        expect(api.updateIssue).toHaveBeenCalledWith('api-test-id', expect.any(Object));
      });

      it('calls updateIssue with status update payload', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        expect(api.updateIssue).toHaveBeenCalledWith(expect.any(String), { status: 'closed' });
      });

      it('handles different status transitions', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const transitions: [Status, Status][] = [
          ['open', 'in_progress'],
          ['in_progress', 'closed'],
          ['closed', 'open'],
          ['open', 'blocked'],
          ['blocked', 'deferred'],
        ];

        for (const [from, to] of transitions) {
          vi.clearAllMocks();

          const issue = createMockIssue({ status: from });
          const event = createMockDragEvent(issue, to);

          await handler(event);

          expect(api.updateIssue).toHaveBeenCalledWith(expect.any(String), { status: to });
        }
      });
    });

    describe('edge cases', () => {
      it('handles issue with undefined status (defaults to open)', async () => {
        const onIssueStatusChange = vi.fn();
        const onError = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange, onError });

        vi.mocked(api.updateIssue).mockRejectedValue(new Error('fail'));

        const issue = createMockIssue({ status: undefined });
        const event = createMockDragEvent(issue, 'in_progress');

        await handler(event);

        // Should call onIssueStatusChange since undefined !== 'in_progress'
        expect(onIssueStatusChange).toHaveBeenCalled();

        // onError should receive 'open' as previousStatus
        expect(onError).toHaveBeenCalledWith(expect.any(Error), expect.any(Object), 'open');
      });

      it('handles custom status values', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'custom_status' as Status });
        const event = createMockDragEvent(issue, 'another_custom' as Status);

        await handler(event);

        expect(onIssueStatusChange).toHaveBeenCalledWith(expect.any(String), 'another_custom');
        expect(api.updateIssue).toHaveBeenCalledWith(expect.any(String), {
          status: 'another_custom',
        });
      });

      it('optimistic update still happens even if API fails', async () => {
        vi.mocked(api.updateIssue).mockRejectedValue(new Error('API Error'));

        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        // Optimistic update should have been called
        expect(onIssueStatusChange).toHaveBeenCalledWith(expect.any(String), 'closed');
      });

      it('returns a Promise that resolves', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        const result = handler(event);

        expect(result).toBeInstanceOf(Promise);
        await expect(result).resolves.toBeUndefined();
      });

      it('returns early for empty active data current', async () => {
        const onIssueStatusChange = vi.fn();
        const handler = createDragEndHandler({ onIssueStatusChange });

        const event: DragEndEvent = {
          active: {
            id: 'test',
            data: { current: undefined },
            rect: { current: { initial: null, translated: null } },
          },
          over: {
            id: 'in_progress',
            data: { current: { status: 'in_progress' } },
            disabled: false,
            rect: { width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0 },
          },
          activatorEvent: null as unknown as Event,
          collisions: null,
          delta: { x: 0, y: 0 },
        };

        await handler(event);

        expect(onIssueStatusChange).not.toHaveBeenCalled();
      });
    });

    describe('handler factory', () => {
      it('returns a function', () => {
        const handler = createDragEndHandler({
          onIssueStatusChange: vi.fn(),
        });

        expect(typeof handler).toBe('function');
      });

      it('creates independent handlers', async () => {
        const callback1 = vi.fn();
        const callback2 = vi.fn();

        const handler1 = createDragEndHandler({ onIssueStatusChange: callback1 });
        const _handler2 = createDragEndHandler({ onIssueStatusChange: callback2 });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler1(event);

        expect(callback1).toHaveBeenCalled();
        expect(callback2).not.toHaveBeenCalled();
      });

      it('captures options at creation time', async () => {
        const onSuccess = vi.fn();
        const handler = createDragEndHandler({
          onIssueStatusChange: vi.fn(),
          onSuccess,
        });

        const issue = createMockIssue({ status: 'open' });
        const event = createMockDragEvent(issue, 'closed');

        await handler(event);

        expect(onSuccess).toHaveBeenCalled();
      });
    });
  });
});
