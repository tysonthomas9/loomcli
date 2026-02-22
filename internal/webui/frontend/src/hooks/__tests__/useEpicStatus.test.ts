/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useEpicStatus hook.
 *
 * Tests verify enabled/disabled behavior, data mapping,
 * loading states, and error handling.
 */

import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { getEpicStatuses } from '@/api/issues';
import type { EpicStatus } from '@/types';

import { useEpicStatus } from '../useEpicStatus';

// Mock the issues API module
vi.mock('@/api/issues', () => ({
  getEpicStatuses: vi.fn(),
}));

const mockGetEpicStatuses = vi.mocked(getEpicStatuses);

/**
 * Helper to create a mock EpicStatus.
 */
function createMockEpicStatus(
  id: string,
  overrides?: Partial<EpicStatus>
): EpicStatus {
  return {
    epic: { id, title: `Epic ${id}` } as EpicStatus['epic'],
    total_children: 10,
    closed_children: 5,
    eligible_for_close: false,
    ...overrides,
  };
}

/**
 * Helper to flush pending promises.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe('useEpicStatus', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('disabled state', () => {
    it('returns empty map when disabled', async () => {
      const { result } = renderHook(() => useEpicStatus(false));

      await flushPromises();

      expect(result.current.epicStatuses.size).toBe(0);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
      expect(mockGetEpicStatuses).not.toHaveBeenCalled();
    });

    it('clears existing data when switching from enabled to disabled', async () => {
      const statuses = [createMockEpicStatus('epic-1')];
      mockGetEpicStatuses.mockResolvedValueOnce(statuses);

      const { result, rerender } = renderHook(
        ({ enabled }) => useEpicStatus(enabled),
        { initialProps: { enabled: true } }
      );

      await flushPromises();

      expect(result.current.epicStatuses.size).toBe(1);

      // Switch to disabled
      rerender({ enabled: false });

      await flushPromises();

      expect(result.current.epicStatuses.size).toBe(0);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe('enabled state', () => {
    it('fetches data when enabled', async () => {
      const statuses = [
        createMockEpicStatus('epic-1', { total_children: 8, closed_children: 3 }),
        createMockEpicStatus('epic-2', { total_children: 4, closed_children: 4, eligible_for_close: true }),
      ];
      mockGetEpicStatuses.mockResolvedValueOnce(statuses);

      const { result } = renderHook(() => useEpicStatus(true));

      // Initially loading
      expect(result.current.isLoading).toBe(true);

      await flushPromises();

      expect(mockGetEpicStatuses).toHaveBeenCalledTimes(1);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
      expect(result.current.epicStatuses.size).toBe(2);
    });

    it('maps epic ID correctly from response', async () => {
      const statuses = [
        createMockEpicStatus('epic-abc', { total_children: 5, closed_children: 2 }),
        createMockEpicStatus('epic-def', { total_children: 10, closed_children: 10, eligible_for_close: true }),
      ];
      mockGetEpicStatuses.mockResolvedValueOnce(statuses);

      const { result } = renderHook(() => useEpicStatus(true));

      await flushPromises();

      const epicAbc = result.current.epicStatuses.get('epic-abc');
      expect(epicAbc).toBeDefined();
      expect(epicAbc!.total_children).toBe(5);
      expect(epicAbc!.closed_children).toBe(2);

      const epicDef = result.current.epicStatuses.get('epic-def');
      expect(epicDef).toBeDefined();
      expect(epicDef!.eligible_for_close).toBe(true);
    });

    it('skips statuses with null epic', async () => {
      const statuses: EpicStatus[] = [
        createMockEpicStatus('epic-1'),
        { epic: null, total_children: 0, closed_children: 0, eligible_for_close: false },
      ];
      mockGetEpicStatuses.mockResolvedValueOnce(statuses);

      const { result } = renderHook(() => useEpicStatus(true));

      await flushPromises();

      expect(result.current.epicStatuses.size).toBe(1);
      expect(result.current.epicStatuses.has('epic-1')).toBe(true);
    });
  });

  describe('error handling', () => {
    it('sets error on fetch failure', async () => {
      mockGetEpicStatuses.mockRejectedValueOnce(new Error('Network error'));

      const { result } = renderHook(() => useEpicStatus(true));

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe('Network error');
      expect(result.current.epicStatuses.size).toBe(0);
    });

    it('uses generic error message for non-Error exceptions', async () => {
      mockGetEpicStatuses.mockRejectedValueOnce('string error');

      const { result } = renderHook(() => useEpicStatus(true));

      await flushPromises();

      expect(result.current.error).toBe('Failed to fetch epic statuses');
    });
  });

  describe('empty data', () => {
    it('handles empty statuses array', async () => {
      mockGetEpicStatuses.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useEpicStatus(true));

      await flushPromises();

      expect(result.current.epicStatuses.size).toBe(0);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });
});
