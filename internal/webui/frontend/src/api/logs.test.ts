import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import {
  getTaskLogPhases,
  getTaskLogStreamUrl,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
  getAgentLogArchive,
} from './logs';
import { getAuthToken, get } from './client';

vi.mock('./client', () => ({
  getAuthToken: vi.fn(),
  get: vi.fn(),
}));

const mockGetAuthToken = getAuthToken as ReturnType<typeof vi.fn>;
const mockGet = get as ReturnType<typeof vi.fn>;

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
  });

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
  });

  describe('agent terminal endpoints', () => {
    it('fetches agent terminal mode', async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { agent: 'ember', mode: 'tmux' },
      });

      const mode = await getAgentTerminalInfo('ember');

      expect(mode).toBe('tmux');
      expect(mockGet).toHaveBeenCalledWith('/api/agents/ember/terminal/info');
    });

    it('fetches one-time agent terminal token', async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { token: 'abc123' },
      });

      const token = await getAgentTerminalToken('ember');

      expect(token).toBe('abc123');
      expect(mockGet).toHaveBeenCalledWith('/api/agents/ember/terminal/token');
    });

    it('builds ws url for agent terminal', () => {
      const url = getAgentTerminalWsUrl('ember', 'abc123');
      expect(url).toContain('/api/agents/ember/terminal/ws?token=abc123');
      expect(url.startsWith('ws://') || url.startsWith('wss://')).toBe(true);
    });

    it('fetches static agent archive logs', async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: ['a', 'b'], line_count: 2 },
      });

      const archive = await getAgentLogArchive('ember', 100);

      expect(archive).toEqual({ lines: ['a', 'b'], lineCount: 2 });
      expect(mockGet).toHaveBeenCalledWith('/api/agents/ember/logs?lines=100');
    });

    it('normalizes null archive payload fields', async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { lines: null, line_count: null },
      });

      const archive = await getAgentLogArchive('ember', 50);

      expect(archive).toEqual({ lines: [], lineCount: 0 });
      expect(mockGet).toHaveBeenCalledWith('/api/agents/ember/logs?lines=50');
    });
  });
});
