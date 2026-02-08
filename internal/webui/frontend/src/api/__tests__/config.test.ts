/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the backend config API functions (config.ts).
 *
 * These tests verify that getBackendConfig and updateBackendConfig correctly
 * call the API client and unwrap the response envelope.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';

import { getBackendConfig, updateBackendConfig } from '../config';
import type { BackendConfigData } from '../config';

// Mock the API client module
vi.mock('../client', () => ({
  get: vi.fn(),
  patch: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    statusText: string;
    constructor(status: number, statusText: string) {
      super(`API Error: ${status} ${statusText}`);
      this.name = 'ApiError';
      this.status = status;
      this.statusText = statusText;
    }
  },
}));

import { get, patch } from '../client';

const mockGet = vi.mocked(get);
const mockPatch = vi.mocked(patch);

/**
 * Helper to create a mock BackendConfigData.
 */
function createMockConfigData(overrides?: Partial<BackendConfigData>): BackendConfigData {
  return {
    backend: 'anthropic',
    source: 'project',
    available: ['anthropic', 'openai', 'local'],
    agents: [],
    ...overrides,
  };
}

describe('getBackendConfig', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('calls GET /api/config/backend and unwraps response', async () => {
    const configData = createMockConfigData();
    mockGet.mockResolvedValueOnce({ success: true, data: configData });

    const result = await getBackendConfig();

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith('/api/config/backend');
    expect(result).toEqual(configData);
  });

  it('returns config with all fields populated', async () => {
    const configData = createMockConfigData({
      backend: 'openai',
      source: 'default',
      available: ['anthropic', 'openai'],
      agents: [
        { worktree: 'feature-a', role: 'coder', backend: 'anthropic' },
        { worktree: 'feature-b', role: 'reviewer', backend: 'openai' },
      ],
    });
    mockGet.mockResolvedValueOnce({ success: true, data: configData });

    const result = await getBackendConfig();

    expect(result.backend).toBe('openai');
    expect(result.source).toBe('default');
    expect(result.available).toEqual(['anthropic', 'openai']);
    expect(result.agents).toHaveLength(2);
    expect(result.agents[0].worktree).toBe('feature-a');
  });

  it('throws on failure response', async () => {
    mockGet.mockResolvedValueOnce({ success: false, error: 'config not found' });

    await expect(getBackendConfig()).rejects.toThrow();
  });

  it('throws on network error from client', async () => {
    mockGet.mockRejectedValueOnce(new Error('Network error'));

    await expect(getBackendConfig()).rejects.toThrow('Network error');
  });
});

describe('updateBackendConfig', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('calls PATCH /api/config/backend with correct body and unwraps response', async () => {
    const configData = createMockConfigData({ backend: 'openai', source: 'project' });
    mockPatch.mockResolvedValueOnce({ success: true, data: configData });

    const result = await updateBackendConfig('openai');

    expect(mockPatch).toHaveBeenCalledTimes(1);
    expect(mockPatch).toHaveBeenCalledWith('/api/config/backend', { backend: 'openai' });
    expect(result).toEqual(configData);
  });

  it('returns updated config data after successful patch', async () => {
    const configData = createMockConfigData({
      backend: 'local',
      source: 'project',
      available: ['anthropic', 'openai', 'local'],
    });
    mockPatch.mockResolvedValueOnce({ success: true, data: configData });

    const result = await updateBackendConfig('local');

    expect(result.backend).toBe('local');
    expect(result.source).toBe('project');
  });

  it('throws on failure response', async () => {
    mockPatch.mockResolvedValueOnce({ success: false, error: 'invalid backend' });

    await expect(updateBackendConfig('invalid')).rejects.toThrow();
  });

  it('throws on network error from client', async () => {
    mockPatch.mockRejectedValueOnce(new Error('Connection refused'));

    await expect(updateBackendConfig('openai')).rejects.toThrow('Connection refused');
  });
});
