/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the Loom Agent API client (agents.ts).
 *
 * These tests verify that the API client correctly handles mapping between
 * API field names and frontend field names, particularly the 'backlog' -> 'blocked'
 * mapping for task status categories.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

import type { LoomTaskLists } from '@/types';

import { fetchStatus, fetchTasks, type FetchStatusResult } from '../agents';

// Mock fetch for testing
const mockFetch = vi.fn();
global.fetch = mockFetch;

describe('fetchStatus', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('successfully fetches and maps status from API', async () => {
    const mockResponse: LoomStatusResponse = {
      agents: null,
      tasks: {
        needs_planning: 5,
        ready_to_implement: 3,
        in_progress: 2,
        need_review: 1,
        blocked: 4, // This is the mapped field (from API "backlog")
      },
      agent_tasks: null,
      sync: {
        db_synced: true,
        db_last_sync: '2024-01-15T12:00:00Z',
      },
      stats: {
        open: 15,
        closed: 25,
        total: 40,
        completion: 62.5,
      },
      timestamp: '2024-01-15T12:30:00Z',
    };

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        agents: null,
        tasks: {
          needs_planning: 5,
          ready_to_implement: 3,
          in_progress: 2,
          need_review: 1,
          backlog: 4, // API sends "backlog"
        },
        agent_tasks: null,
        sync: mockResponse.sync,
        stats: mockResponse.stats,
        timestamp: mockResponse.timestamp,
      }),
    });

    const result = await fetchStatus();

    expect(result.tasks.needs_planning).toBe(5);
    expect(result.tasks.ready_to_implement).toBe(3);
    expect(result.tasks.in_progress).toBe(2);
    expect(result.tasks.need_review).toBe(1);
    expect(result.tasks.blocked).toBe(4); // Mapped from "backlog"
  });

  it('maps API backlog field to frontend blocked field', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        agents: null,
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 10, // API field name
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: '2024-01-15T12:00:00Z',
        },
        stats: {
          open: 0,
          closed: 0,
          total: 10,
          completion: 0,
        },
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchStatus();

    expect(result.tasks).toHaveProperty('blocked');
    expect(result.tasks.blocked).toBe(10);
    // Ensure old field name does not exist in result
    // Verify the API field name does not leak into the result
    expect((result.tasks as Record<string, unknown>).backlog).toBeUndefined();
  });

  it('returns tasks.blocked as 0 when backlog is 0', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        agents: null,
        tasks: {
          needs_planning: 5,
          ready_to_implement: 3,
          in_progress: 2,
          need_review: 1,
          backlog: 0,
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: '2024-01-15T12:00:00Z',
        },
        stats: {
          open: 11,
          closed: 25,
          total: 36,
          completion: 69.4,
        },
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchStatus();

    expect(result.tasks.blocked).toBe(0);
  });

  it('preserves all other task status counts during mapping', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        agents: null,
        tasks: {
          needs_planning: 10,
          ready_to_implement: 20,
          in_progress: 15,
          need_review: 8,
          backlog: 5,
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: '2024-01-15T12:00:00Z',
        },
        stats: {
          open: 53,
          closed: 100,
          total: 153,
          completion: 65.4,
        },
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchStatus();

    // Verify all counts are preserved correctly
    expect(result.tasks.needs_planning).toBe(10);
    expect(result.tasks.ready_to_implement).toBe(20);
    expect(result.tasks.in_progress).toBe(15);
    expect(result.tasks.need_review).toBe(8);
    expect(result.tasks.blocked).toBe(5);
  });

  it('returns complete FetchStatusResult with all properties', async () => {
    const agents = [{ name: 'nova', branch: 'main', status: 'ready', ahead: 0, behind: 0 }];

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        agents,
        tasks: {
          needs_planning: 1,
          ready_to_implement: 2,
          in_progress: 3,
          need_review: 4,
          backlog: 5,
        },
        agent_tasks: { nova: { id: 'bd-123', title: 'Test', priority: 1, status: 'in_progress' } },
        sync: {
          db_synced: true,
          db_last_sync: '2024-01-15T12:00:00Z',
        },
        stats: {
          open: 15,
          closed: 25,
          total: 40,
          completion: 62.5,
        },
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result: FetchStatusResult = await fetchStatus();

    expect(result).toHaveProperty('agents');
    expect(result).toHaveProperty('tasks');
    expect(result).toHaveProperty('agentTasks');
    expect(result).toHaveProperty('sync');
    expect(result).toHaveProperty('stats');
    expect(result).toHaveProperty('timestamp');

    expect(result.agents).toEqual(agents);
    expect(result.tasks.blocked).toBe(5);
    expect(result.timestamp).toBe('2024-01-15T12:30:00Z');
  });

  it('throws error on non-ok HTTP response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
    });

    await expect(fetchStatus()).rejects.toThrow('Loom server returned 500');
  });

  it('throws error on network failure', async () => {
    mockFetch.mockRejectedValueOnce(new Error('Network error'));

    await expect(fetchStatus()).rejects.toThrow('Network error');
  });
});

describe('fetchTasks', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('successfully fetches and maps task lists from API', async () => {
    const taskLists = {
      needsPlanning: [{ id: 'bd-001', title: 'Plan feature', priority: 2, status: 'open' }],
      readyToImplement: [{ id: 'bd-002', title: 'Implement feature', priority: 1, status: 'open' }],
      inProgress: [{ id: 'bd-003', title: 'In progress task', priority: 0, status: 'in_progress' }],
      needsReview: [{ id: 'bd-004', title: 'Review code', priority: 1, status: 'review' }],
      blocked: [{ id: 'bd-005', title: 'Blocked task', priority: 3, status: 'blocked' }],
    };

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 1,
          ready_to_implement: 1,
          in_progress: 1,
          need_review: 1,
          blocked: 1,
        },
        needs_planning: taskLists.needsPlanning,
        ready_to_implement: taskLists.readyToImplement,
        in_progress: taskLists.inProgress,
        needs_review: taskLists.needsReview,
        backlog: taskLists.blocked, // API sends "backlog"
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchTasks();

    expect(result.needsPlanning).toEqual(taskLists.needsPlanning);
    expect(result.readyToImplement).toEqual(taskLists.readyToImplement);
    expect(result.inProgress).toEqual(taskLists.inProgress);
    expect(result.needsReview).toEqual(taskLists.needsReview);
    expect(result.blocked).toEqual(taskLists.blocked); // Mapped from "backlog"
  });

  it('maps API backlog field to frontend blocked field in task lists', async () => {
    const blockedTasks = [
      { id: 'bd-100', title: 'First blocked task', priority: 2, status: 'blocked' },
      { id: 'bd-101', title: 'Second blocked task', priority: 3, status: 'blocked' },
    ];

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 2,
        },
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: blockedTasks, // API field name
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result: LoomTaskLists = await fetchTasks();

    expect(result).toHaveProperty('blocked');
    expect(result.blocked).toEqual(blockedTasks);
    // Ensure old field name does not exist in result
    // Verify the API field name does not leak into the result
    expect((result as Record<string, unknown>).backlog).toBeUndefined();
  });

  it('returns empty blocked array when API sends null backlog', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 0,
        },
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: null, // API sends null
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchTasks();

    expect(result.blocked).toEqual([]);
  });

  it('returns blocked array with multiple tasks', async () => {
    const blockedTasks = Array.from({ length: 5 }, (_, i) => ({
      id: `bd-${200 + i}`,
      title: `Blocked task ${i + 1}`,
      priority: Math.floor(Math.random() * 5),
      status: 'blocked' as const,
    }));

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 5,
        },
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: blockedTasks,
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchTasks();

    expect(result.blocked).toHaveLength(5);
    expect(result.blocked).toEqual(blockedTasks);
  });

  it('preserves all other task lists during mapping', async () => {
    const taskLists = {
      needsPlanning: [{ id: 'bd-010', title: 'Plan', priority: 1, status: 'open' }],
      readyToImplement: [
        { id: 'bd-020', title: 'Ready 1', priority: 0, status: 'open' },
        { id: 'bd-021', title: 'Ready 2', priority: 1, status: 'open' },
      ],
      inProgress: [{ id: 'bd-030', title: 'Working', priority: 0, status: 'in_progress' }],
      needsReview: [{ id: 'bd-040', title: 'Review', priority: 1, status: 'review' }],
      blocked: [{ id: 'bd-050', title: 'Blocked', priority: 2, status: 'blocked' }],
    };

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 1,
          ready_to_implement: 2,
          in_progress: 1,
          need_review: 1,
          blocked: 1,
        },
        needs_planning: taskLists.needsPlanning,
        ready_to_implement: taskLists.readyToImplement,
        in_progress: taskLists.inProgress,
        needs_review: taskLists.needsReview,
        backlog: taskLists.blocked,
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result = await fetchTasks();

    // Verify all lists are preserved correctly
    expect(result.needsPlanning).toEqual(taskLists.needsPlanning);
    expect(result.readyToImplement).toEqual(taskLists.readyToImplement);
    expect(result.inProgress).toEqual(taskLists.inProgress);
    expect(result.needsReview).toEqual(taskLists.needsReview);
    expect(result.blocked).toEqual(taskLists.blocked);
  });

  it('returns complete LoomTaskLists with all properties', async () => {
    const taskLists = {
      needsPlanning: [{ id: 'bd-001', title: 'Plan', priority: 2, status: 'open' }],
      readyToImplement: [{ id: 'bd-002', title: 'Implement', priority: 1, status: 'open' }],
      inProgress: [{ id: 'bd-003', title: 'In progress', priority: 0, status: 'in_progress' }],
      needsReview: [{ id: 'bd-004', title: 'Review', priority: 1, status: 'review' }],
      blocked: [{ id: 'bd-005', title: 'Blocked', priority: 3, status: 'blocked' }],
    };

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 1,
          ready_to_implement: 1,
          in_progress: 1,
          need_review: 1,
          blocked: 1,
        },
        needs_planning: taskLists.needsPlanning,
        ready_to_implement: taskLists.readyToImplement,
        in_progress: taskLists.inProgress,
        needs_review: taskLists.needsReview,
        backlog: taskLists.blocked,
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const result: LoomTaskLists = await fetchTasks();

    expect(result).toHaveProperty('needsPlanning');
    expect(result).toHaveProperty('readyToImplement');
    expect(result).toHaveProperty('inProgress');
    expect(result).toHaveProperty('needsReview');
    expect(result).toHaveProperty('blocked');
  });

  it('throws error on non-ok HTTP response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      statusText: 'Not Found',
    });

    await expect(fetchTasks()).rejects.toThrow('Loom server returned 404');
  });

  it('throws error on network failure', async () => {
    mockFetch.mockRejectedValueOnce(new Error('Connection timeout'));

    await expect(fetchTasks()).rejects.toThrow('Connection timeout');
  });
});

describe('API field mapping consistency', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('both fetchStatus and fetchTasks use consistent blocked field name', async () => {
    // fetchStatus should map backlog -> blocked
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        agents: null,
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 7,
        },
        agent_tasks: null,
        sync: { db_synced: true, db_last_sync: '2024-01-15T12:00:00Z' },
        stats: { open: 7, closed: 0, total: 7, completion: 0 },
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const statusResult = await fetchStatus();

    expect(statusResult.tasks.blocked).toBe(7);

    // fetchTasks should map backlog -> blocked
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        summary: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 7,
        },
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: [{ id: 'bd-100', title: 'Blocked', priority: 0, status: 'blocked' }],
        timestamp: '2024-01-15T12:30:00Z',
      }),
    });

    const tasksResult = await fetchTasks();

    expect(tasksResult.blocked).toHaveLength(1);
    expect(tasksResult.blocked[0].id).toBe('bd-100');
  });
});
