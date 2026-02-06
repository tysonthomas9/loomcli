/**
 * @vitest-environment jsdom
 */

/**
 * Integration tests for AgentRow rendering within IssueCard.
 * Tests that AgentRow appears for in_progress column with assignee,
 * and does not appear for other columns.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import { useAgentContext } from '@/hooks';
import type { Issue, LoomAgentStatus } from '@/types';

import { IssueCard } from '../IssueCard';

// Mock useAgentContext to control agent data
vi.mock('@/hooks', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/hooks')>();
  return {
    ...orig,
    useAgentContext: vi.fn(() => ({
      agents: [],
      tasks: {
        needs_planning: 0,
        ready_to_implement: 0,
        in_progress: 0,
        need_review: 0,
        blocked: 0,
      },
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
      isConnected: false,
      connectionState: 'never_connected' as const,
      wasEverConnected: false,
      retryCountdown: 0,
      error: null,
      lastUpdated: null,
      refetch: async () => {},
      retryNow: () => {},
      getAgentByName: () => undefined as LoomAgentStatus | undefined,
    })),
  };
});

// Mock AgentCard utility functions to return predictable values
vi.mock('@/components/AgentCard', () => ({
  getAvatarColor: vi.fn((name: string) => `#color-${name}`),
  getStatusDotColor: vi.fn(() => '#22c55e'),
  getStatusLabel: vi.fn(() => 'Working'),
}));

const mockUseAgentContext = vi.mocked(useAgentContext);

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-issue-abc123',
    title: 'Test Issue Title',
    priority: 2,
    created_at: '2024-01-15T10:30:00Z',
    updated_at: '2024-01-15T10:30:00Z',
    ...overrides,
  };
}

/**
 * Create a mock LoomAgentStatus.
 */
function createMockAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: 'nova',
    branch: 'feature/bd-123',
    status: 'working: bd-123 (5m)',
    ahead: 2,
    behind: 0,
    ...overrides,
  };
}

/**
 * Helper to configure mockUseAgentContext to return a specific agent.
 */
function setupAgentContext(agent: LoomAgentStatus | undefined) {
  mockUseAgentContext.mockReturnValue({
    agents: agent ? [agent] : [],
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
    refetch: async () => {},
    retryNow: () => {},
    getAgentByName: (name: string) => (agent && agent.name === name ? agent : undefined),
  });
}

describe('IssueCard AgentRow integration', () => {
  describe('in_progress column with assignee', () => {
    it('renders AgentRow when columnId is in_progress and assignee matches an agent', () => {
      const agent = createMockAgent({ name: 'nova' });
      setupAgentContext(agent);

      const issue = createTestIssue({ assignee: 'nova' });
      const { container } = render(<IssueCard issue={issue} columnId="in_progress" />);

      // AgentRow renders the agent name
      expect(screen.getByText('nova')).toBeInTheDocument();
      // AgentRow renders the avatar initial
      expect(screen.getByText('N')).toBeInTheDocument();
      // AgentRow renders the activity text
      expect(screen.getByText('Working')).toBeInTheDocument();
      // Status dot should be present
      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toBeInTheDocument();
    });

    it('renders AgentRow with name only when agent not found in loom', () => {
      // getAgentByName returns undefined for unknown agents
      setupAgentContext(undefined);

      const issue = createTestIssue({ assignee: 'unknown-agent' });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      // The assignee name should still appear (AgentRow renders without status)
      expect(screen.getByText('unknown-agent')).toBeInTheDocument();
    });

    it('strips [H] prefix from human assignee display name', () => {
      setupAgentContext(undefined);

      const issue = createTestIssue({ assignee: '[H] Alice' });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(screen.getByText('Alice')).toBeInTheDocument();
      expect(screen.queryByText('[H] Alice')).not.toBeInTheDocument();
    });
  });

  describe('AgentRow not shown in other columns', () => {
    it.each(['open', 'review', 'done', 'backlog', 'blocked'])(
      'does not render AgentRow for columnId="%s"',
      (columnId) => {
        const agent = createMockAgent({ name: 'nova' });
        setupAgentContext(agent);

        const issue = createTestIssue({ assignee: 'nova' });
        const { container } = render(<IssueCard issue={issue} columnId={columnId} />);

        // AgentRow specific elements should not be present
        const agentRow = container.querySelector('[class*="agentRow"]');
        expect(agentRow).not.toBeInTheDocument();
      }
    );

    it('does not render AgentRow when columnId is undefined', () => {
      const agent = createMockAgent({ name: 'nova' });
      setupAgentContext(agent);

      const issue = createTestIssue({ assignee: 'nova' });
      const { container } = render(<IssueCard issue={issue} />);

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });
  });

  describe('AgentRow not shown without assignee', () => {
    it('does not render AgentRow when issue has no assignee', () => {
      setupAgentContext(createMockAgent());

      const issue = createTestIssue({ assignee: undefined });
      const { container } = render(<IssueCard issue={issue} columnId="in_progress" />);

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });

    it('does not render AgentRow when assignee is empty string', () => {
      setupAgentContext(createMockAgent());

      const issue = createTestIssue({ assignee: '' });
      const { container } = render(<IssueCard issue={issue} columnId="in_progress" />);

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });
  });

  describe('does not affect other IssueCard functionality', () => {
    it('still renders title and priority when AgentRow is shown', () => {
      const agent = createMockAgent({ name: 'nova' });
      setupAgentContext(agent);

      const issue = createTestIssue({ title: 'Important Task', priority: 1, assignee: 'nova' });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(screen.getByRole('heading', { name: 'Important Task' })).toBeInTheDocument();
      expect(screen.getByText('P1')).toBeInTheDocument();
    });

    it('still renders blocked badge alongside AgentRow', () => {
      const agent = createMockAgent({ name: 'nova' });
      setupAgentContext(agent);

      const issue = createTestIssue({ assignee: 'nova' });
      render(<IssueCard issue={issue} columnId="in_progress" blockedByCount={2} />);

      expect(screen.getByLabelText('Blocked by 2 issues')).toBeInTheDocument();
      // AgentRow should still be present
      expect(screen.getByText('nova')).toBeInTheDocument();
    });
  });
});
