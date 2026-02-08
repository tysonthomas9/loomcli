/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useBackendConfig hook.
 *
 * These tests verify fetch-on-mount behavior, loading/error states,
 * optimistic updates, rollback on failure, and refetch.
 */

import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { getBackendConfig, updateBackendConfig } from '@/api/config';
import type { BackendConfigData } from '@/api/config';

import { useBackendConfig } from '../useBackendConfig';

// Mock the config API module
vi.mock('@/api/config', () => ({
  getBackendConfig: vi.fn(),
  updateBackendConfig: vi.fn(),
}));

const mockGetBackendConfig = vi.mocked(getBackendConfig);
const mockUpdateBackendConfig = vi.mocked(updateBackendConfig);

/**
 * Helper to create a mock BackendConfigData.
 */
function createMockConfig(overrides?: Partial<BackendConfigData>): BackendConfigData {
  return {
    backend: 'anthropic',
    source: 'project',
    available: ['anthropic', 'openai', 'local'],
    agents: [],
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

describe('useBackendConfig', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('initial fetch', () => {
    it('fetches config on mount and returns data', async () => {
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(mockGetBackendConfig).toHaveBeenCalledTimes(1);
      expect(result.current.config).toEqual(mockConfig);
    });

    it('returns loading true initially, false after fetch', async () => {
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      // Initially loading
      expect(result.current.isLoading).toBe(true);
      expect(result.current.config).toBeNull();

      await flushPromises();

      // After fetch completes
      expect(result.current.isLoading).toBe(false);
      expect(result.current.config).toEqual(mockConfig);
    });

    it('returns error on fetch failure', async () => {
      mockGetBackendConfig.mockRejectedValueOnce(new Error('Server unavailable'));

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe('Server unavailable');
      expect(result.current.config).toBeNull();
    });

    it('returns generic error message for non-Error exceptions', async () => {
      mockGetBackendConfig.mockRejectedValueOnce('string error');

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.error).toBe('Failed to load backend config');
    });
  });

  describe('updateBackend', () => {
    it('optimistically updates config.backend', async () => {
      const mockConfig = createMockConfig({ backend: 'anthropic' });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config?.backend).toBe('anthropic');

      // Start update but don't resolve yet
      const updatedConfig = createMockConfig({ backend: 'openai', source: 'project' });
      let resolveUpdate!: (value: BackendConfigData) => void;
      mockUpdateBackendConfig.mockImplementationOnce(
        () => new Promise((resolve) => { resolveUpdate = resolve; })
      );

      let updatePromise: Promise<void>;
      act(() => {
        updatePromise = result.current.updateBackend('openai');
      });

      // Optimistic update should be applied immediately
      expect(result.current.config?.backend).toBe('openai');
      expect(result.current.config?.source).toBe('project');
      expect(result.current.isSaving).toBe(true);

      // Resolve the API call
      await act(async () => {
        resolveUpdate(updatedConfig);
        await updatePromise!;
      });

      expect(result.current.isSaving).toBe(false);
      expect(result.current.config?.backend).toBe('openai');
    });

    it('rolls back on API error', async () => {
      const mockConfig = createMockConfig({ backend: 'anthropic', source: 'default' });
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config?.backend).toBe('anthropic');

      // Make the update fail
      mockUpdateBackendConfig.mockRejectedValueOnce(new Error('Save failed'));

      await act(async () => {
        try {
          await result.current.updateBackend('openai');
        } catch {
          // Expected to throw
        }
      });

      // Should have rolled back to the original config
      expect(result.current.config?.backend).toBe('anthropic');
      expect(result.current.config?.source).toBe('default');
      expect(result.current.error).toBe('Save failed');
      expect(result.current.isSaving).toBe(false);
    });

    it('does nothing if config is null', async () => {
      mockGetBackendConfig.mockRejectedValueOnce(new Error('failed'));

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config).toBeNull();

      await act(async () => {
        await result.current.updateBackend('openai');
      });

      expect(mockUpdateBackendConfig).not.toHaveBeenCalled();
    });

    it('calls updateBackendConfig API with correct backend string', async () => {
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);
      const updatedConfig = createMockConfig({ backend: 'local' });
      mockUpdateBackendConfig.mockResolvedValueOnce(updatedConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      await act(async () => {
        await result.current.updateBackend('local');
      });

      expect(mockUpdateBackendConfig).toHaveBeenCalledWith('local');
    });
  });

  describe('refetch', () => {
    it('re-fetches config from API', async () => {
      const initialConfig = createMockConfig({ backend: 'anthropic' });
      mockGetBackendConfig.mockResolvedValueOnce(initialConfig);

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.config?.backend).toBe('anthropic');
      expect(mockGetBackendConfig).toHaveBeenCalledTimes(1);

      // Setup a different config for the refetch
      const updatedConfig = createMockConfig({ backend: 'openai' });
      mockGetBackendConfig.mockResolvedValueOnce(updatedConfig);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

      expect(mockGetBackendConfig).toHaveBeenCalledTimes(2);
      expect(result.current.config?.backend).toBe('openai');
    });

    it('clears error on refetch', async () => {
      // First fetch fails
      mockGetBackendConfig.mockRejectedValueOnce(new Error('Network error'));

      const { result } = renderHook(() => useBackendConfig());

      await flushPromises();

      expect(result.current.error).toBe('Network error');

      // Refetch succeeds
      const mockConfig = createMockConfig();
      mockGetBackendConfig.mockResolvedValueOnce(mockConfig);

      await act(async () => {
        result.current.refetch();
      });

      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.config).toEqual(mockConfig);
    });
  });
});
