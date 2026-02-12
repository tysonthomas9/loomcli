import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { getTaskLogPhases, getAgentLogStreamUrl, getTaskLogStreamUrl } from './logs';
import { getAuthToken } from './client';

// Mock the client module
vi.mock('./client', () => ({
  getAuthToken: vi.fn(),
}));

const mockGetAuthToken = getAuthToken as ReturnType<typeof vi.fn>;

describe('logs API', () => {
  let originalFetch: typeof global.fetch;

  beforeEach(() => {
    originalFetch = global.fetch;
    vi.clearAllMocks();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  // ============= getTaskLogPhases =============

  describe('getTaskLogPhases', () => {
    it('returns phases on successful response', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { phases: ['planning', 'implementation'] } }),
      });

      const result = await getTaskLogPhases('beads-abc');

      expect(result).toEqual(['planning', 'implementation']);
    });

    it('returns empty array on 404', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
      });

      const result = await getTaskLogPhases('nonexistent');

      expect(result).toEqual([]);
    });

    it('throws on non-404 error', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
      });

      await expect(getTaskLogPhases('beads-abc')).rejects.toThrow('Failed to fetch log phases');
    });

    it('includes Authorization header when token exists', async () => {
      mockGetAuthToken.mockReturnValue('test-token');
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { phases: [] } }),
      });

      await getTaskLogPhases('beads-abc');

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).toHaveProperty('Authorization', 'Bearer test-token');
    });

    it('omits Authorization header when no token', async () => {
      mockGetAuthToken.mockReturnValue(null);
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { phases: [] } }),
      });

      await getTaskLogPhases('beads-abc');

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).not.toHaveProperty('Authorization');
    });

    it('URL-encodes taskId with special characters', async () => {
      mockGetAuthToken.mockReturnValue(null);
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { phases: [] } }),
      });

      await getTaskLogPhases('beads/abc 123');

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call?.[0]).toBe('/api/tasks/beads%2Fabc%20123/logs');
    });

    it('propagates network errors', async () => {
      global.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

      await expect(getTaskLogPhases('beads-abc')).rejects.toThrow(TypeError);
    });
  });

  // ============= getAgentLogStreamUrl =============

  describe('getAgentLogStreamUrl', () => {
    it('returns correct URL without token', () => {
      mockGetAuthToken.mockReturnValue(null);

      const url = getAgentLogStreamUrl('spark');

      expect(url).toBe('/api/agents/spark/logs/stream');
    });

    it('appends token when available', () => {
      mockGetAuthToken.mockReturnValue('my-token');

      const url = getAgentLogStreamUrl('spark');

      expect(url).toBe('/api/agents/spark/logs/stream?token=my-token');
    });

    it('URL-encodes agent name with special characters', () => {
      mockGetAuthToken.mockReturnValue(null);

      const url = getAgentLogStreamUrl('agent/with spaces');

      expect(url).toBe('/api/agents/agent%2Fwith%20spaces/logs/stream');
    });

    it('URL-encodes token value with special characters', () => {
      mockGetAuthToken.mockReturnValue('tok&en=val');

      const url = getAgentLogStreamUrl('spark');

      expect(url).toBe('/api/agents/spark/logs/stream?token=tok%26en%3Dval');
    });
  });

  // ============= getTaskLogStreamUrl =============

  describe('getTaskLogStreamUrl', () => {
    it('returns correct URL without token', () => {
      mockGetAuthToken.mockReturnValue(null);

      const url = getTaskLogStreamUrl('beads-abc', 'planning');

      expect(url).toBe('/api/tasks/beads-abc/logs/planning/stream');
    });

    it('appends token when available', () => {
      mockGetAuthToken.mockReturnValue('my-token');

      const url = getTaskLogStreamUrl('beads-abc', 'planning');

      expect(url).toBe('/api/tasks/beads-abc/logs/planning/stream?token=my-token');
    });

    it('URL-encodes both taskId and phase', () => {
      mockGetAuthToken.mockReturnValue(null);

      const url = getTaskLogStreamUrl('task/1', 'phase 2');

      expect(url).toBe('/api/tasks/task%2F1/logs/phase%202/stream');
    });

    it('handles special characters in all positions', () => {
      mockGetAuthToken.mockReturnValue('tok&en=val');

      const url = getTaskLogStreamUrl('task/1&2', 'phase=3 4');

      expect(url).toContain('task%2F1%262');
      expect(url).toContain('phase%3D3%204');
      expect(url).toContain('token=tok%26en%3Dval');
    });
  });
});
