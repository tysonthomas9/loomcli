/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useAgentContext hook and AgentProvider.
 */

import { renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, it, expect, vi } from 'vitest';

import type { LoomAgentStatus } from '@/types';

import { AgentProvider, useAgentContext } from '../useAgentContext';
import { useAgents } from '../useAgents';

// Mock useAgents so we don't trigger real polling
vi.mock('../useAgents', () => ({
  useAgents: vi.fn(),
}));
const mockUseAgents = vi.mocked(useAgents);

/**
 * Create a mock agent for testing.
 */
function createMockAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: 'nova',
    branch: 'main',
    status: 'ready',
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

/**
 * Helper to set up mockUseAgents return value.
 */
function setupMockAgents(agents: LoomAgentStatus[] = []) {
  mockUseAgents.mockReturnValue({
    agents,
    tasks: { needs_planning: 0, ready_to_implement: 0, in_progress: 0, need_review: 0, blocked: 0 },
    taskLists: {
      needsPlanning: [],
      readyToImplement: [],
      needsReview: [],
      inProgress: [],
      blocked: [],
    },
    agentTasks: {},
    sync: { db_synced: true, db_last_sync: '', git_needs_push: 0, git_needs_pull: 0 },
    stats: { open: 0, closed: 0, total: 0, completion: 0 },
    isLoading: false,
    isConnected: true,
    connectionState: 'connected' as const,
    wasEverConnected: true,
    retryCountdown: 0,
    error: null,
    lastUpdated: null,
    refetch: vi.fn(),
    retryNow: vi.fn(),
  });
}

describe('useAgentContext', () => {
  describe('outside provider', () => {
    it('returns safe defaults without throwing', () => {
      const { result } = renderHook(() => useAgentContext());

      expect(result.current.agents).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.isConnected).toBe(false);
      expect(result.current.connectionState).toBe('never_connected');
      expect(result.current.wasEverConnected).toBe(false);
      expect(result.current.retryCountdown).toBe(0);
      expect(result.current.error).toBeNull();
      expect(result.current.lastUpdated).toBeNull();
    });

    it('getAgentByName returns undefined outside provider', () => {
      const { result } = renderHook(() => useAgentContext());

      expect(result.current.getAgentByName('anything')).toBeUndefined();
    });

    it('refetch is a no-op async function outside provider', async () => {
      const { result } = renderHook(() => useAgentContext());

      // Should not throw
      await expect(result.current.refetch()).resolves.toBeUndefined();
    });

    it('retryNow is a no-op function outside provider', () => {
      const { result } = renderHook(() => useAgentContext());

      // Should not throw
      expect(() => result.current.retryNow()).not.toThrow();
    });

    it('returns default tasks object outside provider', () => {
      const { result } = renderHook(() => useAgentContext());

      expect(result.current.tasks).toEqual({
        needs_planning: 0,
        ready_to_implement: 0,
        in_progress: 0,
        need_review: 0,
        blocked: 0,
      });
    });

    it('returns default taskLists outside provider', () => {
      const { result } = renderHook(() => useAgentContext());

      expect(result.current.taskLists).toEqual({
        needsPlanning: [],
        readyToImplement: [],
        needsReview: [],
        inProgress: [],
        blocked: [],
      });
    });

    it('returns default sync object outside provider', () => {
      const { result } = renderHook(() => useAgentContext());

      expect(result.current.sync).toEqual({
        db_synced: true,
        db_last_sync: '',
        git_needs_push: 0,
        git_needs_pull: 0,
      });
    });

    it('returns default stats outside provider', () => {
      const { result } = renderHook(() => useAgentContext());

      expect(result.current.stats).toEqual({
        open: 0,
        closed: 0,
        total: 0,
        completion: 0,
      });
    });
  });

  describe('inside provider', () => {
    function wrapper({ children }: { children: ReactNode }) {
      return <AgentProvider>{children}</AgentProvider>;
    }

    it('provides agents from useAgents', () => {
      const agents = [createMockAgent({ name: 'nova' }), createMockAgent({ name: 'falcon' })];
      setupMockAgents(agents);

      const { result } = renderHook(() => useAgentContext(), { wrapper });

      expect(result.current.agents).toHaveLength(2);
      expect(result.current.agents[0].name).toBe('nova');
      expect(result.current.agents[1].name).toBe('falcon');
    });

    it('provides isConnected from useAgents', () => {
      setupMockAgents();

      const { result } = renderHook(() => useAgentContext(), { wrapper });

      expect(result.current.isConnected).toBe(true);
    });

    it('getAgentByName finds agent by name', () => {
      const agents = [
        createMockAgent({ name: 'nova', status: 'working: bd-123 (5m)' }),
        createMockAgent({ name: 'falcon', status: 'ready' }),
      ];
      setupMockAgents(agents);

      const { result } = renderHook(() => useAgentContext(), { wrapper });

      const found = result.current.getAgentByName('nova');
      expect(found).toBeDefined();
      expect(found!.name).toBe('nova');
      expect(found!.status).toBe('working: bd-123 (5m)');
    });

    it('getAgentByName returns undefined for unknown agent', () => {
      const agents = [createMockAgent({ name: 'nova' })];
      setupMockAgents(agents);

      const { result } = renderHook(() => useAgentContext(), { wrapper });

      expect(result.current.getAgentByName('unknown-agent')).toBeUndefined();
    });

    it('getAgentByName returns undefined when agents list is empty', () => {
      setupMockAgents([]);

      const { result } = renderHook(() => useAgentContext(), { wrapper });

      expect(result.current.getAgentByName('nova')).toBeUndefined();
    });

    it('passes pollInterval of 5000 to useAgents', () => {
      setupMockAgents();

      renderHook(() => useAgentContext(), { wrapper });

      expect(mockUseAgents).toHaveBeenCalledWith({ pollInterval: 5000 });
    });
  });
});
