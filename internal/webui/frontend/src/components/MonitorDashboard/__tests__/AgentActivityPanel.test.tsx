/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentActivityPanel component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import type { LoomAgentStatus, LoomSyncInfo, LoomTaskInfo, LoomConnectionState } from '@/types';

import { AgentActivityPanel } from '../AgentActivityPanel';

/**
 * Create a mock agent status for testing.
 */
function createAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: 'test-agent',
    branch: 'main',
    status: 'ready',
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

/**
 * Create a mock sync info for testing.
 */
function createSyncInfo(overrides: Partial<LoomSyncInfo> = {}): LoomSyncInfo {
  return {
    db_synced: true,
    db_last_sync: '2026-01-28T00:00:00Z',
    git_needs_push: 0,
    git_needs_pull: 0,
    ...overrides,
  };
}

/**
 * Create mock agent tasks map for testing.
 */
function createAgentTasks(
  tasks: Record<string, Partial<LoomTaskInfo>> = {}
): Record<string, LoomTaskInfo> {
  const result: Record<string, LoomTaskInfo> = {};
  for (const [agentName, taskOverrides] of Object.entries(tasks)) {
    result[agentName] = {
      id: `task-${agentName}`,
      title: `Task for ${agentName}`,
      priority: 2,
      status: 'in_progress',
      ...taskOverrides,
    };
  }
  return result;
}

/**
 * Default props for most tests.
 */
const defaultProps = {
  agents: [],
  agentTasks: {},
  sync: createSyncInfo(),
  isLoading: false,
  isConnected: true,
  connectionState: 'connected' as LoomConnectionState,
  retryCountdown: 0,
  lastUpdated: new Date(),
};

describe('AgentActivityPanel', () => {
  describe('rendering agent cards', () => {
    it('renders all agent cards', () => {
      const agents = [
        createAgent({ name: 'nova', status: 'ready' }),
        createAgent({ name: 'falcon', status: 'working: bd-123 (5m)' }),
        createAgent({ name: 'ember', status: 'idle' }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      expect(screen.getByText('nova')).toBeInTheDocument();
      expect(screen.getByText('falcon')).toBeInTheDocument();
      expect(screen.getByText('ember')).toBeInTheDocument();
    });

    it('renders with testid "agent-activity-panel"', () => {
      render(<AgentActivityPanel {...defaultProps} />);

      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });
  });

  describe('summary counts', () => {
    it('shows correct summary counts (active, idle, error, needsPush)', () => {
      const agents = [
        createAgent({ name: 'active-1', status: 'working: bd-1 (5m)' }),
        createAgent({ name: 'active-2', status: 'planning: bd-2 (2m)' }),
        createAgent({ name: 'idle-1', status: 'ready' }),
        createAgent({ name: 'idle-2', status: 'idle' }),
        createAgent({ name: 'idle-3', status: 'done: bd-3 (10m)' }),
        createAgent({ name: 'error-1', status: 'error: bd-4 (1m)' }),
        createAgent({ name: 'needs-push-1', status: 'ready', ahead: 2 }),
        createAgent({ name: 'needs-push-2', status: 'working: bd-5 (3m)', ahead: 1 }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      // Active: working + planning = 3 (active-1, active-2, needs-push-2)
      // Idle: ready + idle + done = 4 (idle-1, idle-2, idle-3, needs-push-1)
      // Error: 1 (error-1)
      // Needs push: 2 (needs-push-1, needs-push-2)

      // Find summary items by data-type attribute
      const summaryItems = screen.getAllByText(/^[0-9]+$/);
      expect(summaryItems.length).toBeGreaterThanOrEqual(4);

      // Verify active count
      expect(screen.getByText('3')).toBeInTheDocument();
      expect(screen.getByText('active')).toBeInTheDocument();

      // Verify idle count
      expect(screen.getByText('4')).toBeInTheDocument();
      expect(screen.getByText('idle')).toBeInTheDocument();

      // Verify error count (shown only when > 0)
      expect(screen.getByText('1')).toBeInTheDocument();
      expect(screen.getByText('error')).toBeInTheDocument();

      // Verify needs push count (shown only when > 0)
      expect(screen.getByText('2')).toBeInTheDocument();
      expect(screen.getByText('need push')).toBeInTheDocument();
    });

    it('hides error count when no errors', () => {
      const agents = [
        createAgent({ name: 'agent-1', status: 'ready' }),
        createAgent({ name: 'agent-2', status: 'working: bd-1 (5m)' }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      expect(screen.queryByText('error')).not.toBeInTheDocument();
    });

    it('hides "need push" count when all synced', () => {
      const agents = [
        createAgent({ name: 'agent-1', status: 'ready', ahead: 0 }),
        createAgent({ name: 'agent-2', status: 'working: bd-1 (5m)', ahead: 0 }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      expect(screen.queryByText('need push')).not.toBeInTheDocument();
    });
  });

  describe('disconnected state', () => {
    it('shows "never connected" state when server never reached', () => {
      render(
        <AgentActivityPanel
          {...defaultProps}
          isConnected={false}
          isLoading={false}
          connectionState="never_connected"
        />
      );

      expect(screen.getByText('Loom server not running')).toBeInTheDocument();
      expect(screen.getByText(/loom serve/)).toBeInTheDocument();
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });

    it('shows disconnected state when connection lost and no cached agents', () => {
      render(
        <AgentActivityPanel
          {...defaultProps}
          isConnected={false}
          isLoading={false}
          connectionState="disconnected"
        />
      );

      expect(screen.getByText('Loom server not available')).toBeInTheDocument();
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });

    it('shows agents with stale data when disconnected but have cached agents', () => {
      const agents = [createAgent({ name: 'nova' })];

      render(
        <AgentActivityPanel
          {...defaultProps}
          agents={agents}
          isConnected={false}
          isLoading={false}
          connectionState="disconnected"
        />
      );

      // Should still show cached agents (stale data scenario)
      expect(screen.getByText('nova')).toBeInTheDocument();
    });

    it('shows reconnecting state with countdown', () => {
      render(
        <AgentActivityPanel
          {...defaultProps}
          isConnected={false}
          isLoading={false}
          connectionState="reconnecting"
          retryCountdown={10}
        />
      );

      expect(screen.getByText('Reconnecting to loom server...')).toBeInTheDocument();
      expect(screen.getByText('Retry in 10s')).toBeInTheDocument();
    });

    it('shows retry button in never connected state', () => {
      const onRetry = vi.fn();
      render(
        <AgentActivityPanel
          {...defaultProps}
          isConnected={false}
          isLoading={false}
          connectionState="never_connected"
          onRetry={onRetry}
        />
      );

      const retryButton = screen.getByRole('button', { name: /check connection/i });
      expect(retryButton).toBeInTheDocument();
      fireEvent.click(retryButton);
      expect(onRetry).toHaveBeenCalledTimes(1);
    });
  });

  describe('loading state', () => {
    it('shows loading state', () => {
      render(
        <AgentActivityPanel {...defaultProps} agents={[]} isLoading={true} isConnected={true} />
      );

      expect(screen.getByText('Loading agents...')).toBeInTheDocument();
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });

    it('shows agents instead of loading state when agents exist', () => {
      const agents = [createAgent({ name: 'nova' })];

      render(
        <AgentActivityPanel {...defaultProps} agents={agents} isLoading={true} isConnected={true} />
      );

      expect(screen.queryByText('Loading agents...')).not.toBeInTheDocument();
      expect(screen.getByText('nova')).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    it('shows empty state when no agents', () => {
      render(
        <AgentActivityPanel {...defaultProps} agents={[]} isLoading={false} isConnected={true} />
      );

      expect(screen.getByText('No agents found')).toBeInTheDocument();
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });
  });

  describe('agent click handling', () => {
    it('calls onAgentClick when agent card clicked', () => {
      const onAgentClick = vi.fn();
      const agents = [createAgent({ name: 'nova' }), createAgent({ name: 'falcon' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} onAgentClick={onAgentClick} />);

      // Click on the nova agent card
      fireEvent.click(screen.getByText('nova'));

      expect(onAgentClick).toHaveBeenCalledTimes(1);
      expect(onAgentClick).toHaveBeenCalledWith('nova');
    });

    it('calls onAgentClick with correct agent name', () => {
      const onAgentClick = vi.fn();
      const agents = [createAgent({ name: 'nova' }), createAgent({ name: 'falcon' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} onAgentClick={onAgentClick} />);

      // Click on the falcon agent card
      fireEvent.click(screen.getByText('falcon'));

      expect(onAgentClick).toHaveBeenCalledWith('falcon');
    });

    it('does not throw when clicking without onAgentClick handler', () => {
      const agents = [createAgent({ name: 'nova' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      expect(() => fireEvent.click(screen.getByText('nova'))).not.toThrow();
    });
  });

  describe('task titles', () => {
    it('shows task titles from agentTasks', () => {
      const agents = [
        createAgent({ name: 'nova', status: 'working: bd-123 (5m)' }),
        createAgent({ name: 'falcon', status: 'planning: bd-456 (2m)' }),
      ];
      const agentTasks = createAgentTasks({
        nova: { id: 'bd-123', title: 'Fix the login bug' },
        falcon: { id: 'bd-456', title: 'Add new feature' },
      });

      render(<AgentActivityPanel {...defaultProps} agents={agents} agentTasks={agentTasks} />);

      expect(screen.getByTitle('Fix the login bug')).toBeInTheDocument();
      expect(screen.getByTitle('Add new feature')).toBeInTheDocument();
    });

    it('handles missing task info gracefully', () => {
      const agents = [createAgent({ name: 'nova', status: 'working: bd-123 (5m)' })];
      // Empty agentTasks - no task info for nova
      const agentTasks = {};

      render(<AgentActivityPanel {...defaultProps} agents={agents} agentTasks={agentTasks} />);

      // Should still render the agent card
      expect(screen.getByText('nova')).toBeInTheDocument();
      // Should not crash when task info is missing
      expect(screen.queryByText('Fix the login bug')).not.toBeInTheDocument();
    });
  });

  describe('custom className', () => {
    it('applies custom className', () => {
      render(<AgentActivityPanel {...defaultProps} className="custom-class" />);

      const panel = screen.getByTestId('agent-activity-panel');
      expect(panel).toHaveClass('custom-class');
    });

    it('preserves base styles when custom className is applied', () => {
      const { container } = render(
        <AgentActivityPanel {...defaultProps} className="custom-class" />
      );

      const panel = container.firstChild as HTMLElement;
      // Should have both the module CSS class and custom class
      expect(panel.classList.length).toBeGreaterThan(1);
    });
  });

  describe('edge cases', () => {
    it('handles empty agents array', () => {
      render(<AgentActivityPanel {...defaultProps} agents={[]} />);

      expect(screen.getByText('No agents found')).toBeInTheDocument();
    });

    it('handles agents with all status types', () => {
      const agents = [
        createAgent({ name: 'ready-agent', status: 'ready' }),
        createAgent({ name: 'working-agent', status: 'working: bd-1 (5m)' }),
        createAgent({ name: 'planning-agent', status: 'planning: bd-2 (2m)' }),
        createAgent({ name: 'done-agent', status: 'done: bd-3 (10m)' }),
        createAgent({ name: 'review-agent', status: 'review: bd-4 (1h)' }),
        createAgent({ name: 'idle-agent', status: 'idle' }),
        createAgent({ name: 'error-agent', status: 'error: bd-5 (3m)' }),
        createAgent({ name: 'dirty-agent', status: 'dirty' }),
        createAgent({ name: 'changes-agent', status: '5 changes' }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      // All agents should be rendered
      for (const agent of agents) {
        expect(screen.getByText(agent.name)).toBeInTheDocument();
      }
    });

    it('handles large number of agents', () => {
      const agents = Array.from({ length: 20 }, (_, i) => createAgent({ name: `agent-${i}` }));

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      // Check first and last agent are rendered
      expect(screen.getByText('agent-0')).toBeInTheDocument();
      expect(screen.getByText('agent-19')).toBeInTheDocument();
    });

    it('handles null lastUpdated', () => {
      render(<AgentActivityPanel {...defaultProps} lastUpdated={null} />);

      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });
  });

  describe('summary computation', () => {
    it('counts working agents as active', () => {
      const agents = [createAgent({ name: 'agent-1', status: 'working: bd-1 (5m)' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      expect(screen.getByText('1')).toBeInTheDocument();
      expect(screen.getByText('active')).toBeInTheDocument();
    });

    it('counts planning agents as active', () => {
      const agents = [createAgent({ name: 'agent-1', status: 'planning: bd-1 (5m)' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      expect(screen.getByText('1')).toBeInTheDocument();
      expect(screen.getByText('active')).toBeInTheDocument();
    });

    it('counts ready agents as idle', () => {
      const agents = [createAgent({ name: 'agent-1', status: 'ready' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      // Summary shows 0 active, 1 idle
      const idleSummary = screen.getByText('idle');
      expect(idleSummary).toBeInTheDocument();
    });

    it('counts done agents as idle', () => {
      const agents = [createAgent({ name: 'agent-1', status: 'done: bd-1 (10m)' })];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      // 0 active, 1 idle
      expect(screen.getByText('0')).toBeInTheDocument();
      expect(screen.getByText('active')).toBeInTheDocument();
      expect(screen.getByText('1')).toBeInTheDocument();
      expect(screen.getByText('idle')).toBeInTheDocument();
    });

    it('counts agents with ahead > 0 as needing push', () => {
      const agents = [
        createAgent({ name: 'agent-1', status: 'ready', ahead: 3 }),
        createAgent({ name: 'agent-2', status: 'ready', ahead: 1 }),
        createAgent({ name: 'agent-3', status: 'ready', ahead: 0 }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      // 2 agents need push
      expect(screen.getByText('need push')).toBeInTheDocument();
      expect(screen.getByText('2')).toBeInTheDocument();
    });
  });

  describe('colored summary dots', () => {
    it('renders colored dots in summary bar for each status', () => {
      const agents = [
        createAgent({ name: 'a1', status: 'working: bd-1 (5m)' }),
        createAgent({ name: 'a2', status: 'ready' }),
        createAgent({ name: 'a3', status: 'error: bd-2 (1m)' }),
        createAgent({ name: 'a4', status: 'ready', ahead: 2 }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      const summary = screen.getByRole('status', { name: /summary/i });
      const dots = summary.querySelectorAll('[data-type][aria-hidden="true"]');
      // 4 dots: active, idle, error, sync
      expect(dots.length).toBe(4);
    });

    it('renders dots with correct data-type attributes', () => {
      const agents = [
        createAgent({ name: 'a1', status: 'working: bd-1 (5m)' }),
        createAgent({ name: 'a2', status: 'ready' }),
        createAgent({ name: 'a3', status: 'error: bd-2 (1m)' }),
        createAgent({ name: 'a4', status: 'ready', ahead: 1 }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      const summary = screen.getByRole('status', { name: /summary/i });
      const dots = summary.querySelectorAll('[data-type][aria-hidden="true"]');
      const dotTypes = Array.from(dots).map((d) => d.getAttribute('data-type'));
      expect(dotTypes).toContain('active');
      expect(dotTypes).toContain('idle');
      expect(dotTypes).toContain('error');
      expect(dotTypes).toContain('sync');
    });

    it('only renders active and idle dots when no errors or push needed', () => {
      const agents = [
        createAgent({ name: 'a1', status: 'working: bd-1 (5m)' }),
        createAgent({ name: 'a2', status: 'ready' }),
      ];

      render(<AgentActivityPanel {...defaultProps} agents={agents} />);

      const summary = screen.getByRole('status', { name: /summary/i });
      const dots = summary.querySelectorAll('[aria-hidden="true"]');
      expect(dots.length).toBe(2);
      const dotTypes = Array.from(dots).map((d) => d.getAttribute('data-type'));
      expect(dotTypes).toContain('active');
      expect(dotTypes).toContain('idle');
      expect(dotTypes).not.toContain('error');
      expect(dotTypes).not.toContain('sync');
    });
  });

  describe('accessibility', () => {
    it('has data-testid for e2e tests', () => {
      render(<AgentActivityPanel {...defaultProps} />);

      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });

    it('maintains testid in all states', () => {
      // Loading state
      const { rerender } = render(<AgentActivityPanel {...defaultProps} isLoading={true} />);
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();

      // Disconnected state
      rerender(
        <AgentActivityPanel {...defaultProps} isConnected={false} connectionState="disconnected" />
      );
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();

      // Empty state
      rerender(<AgentActivityPanel {...defaultProps} agents={[]} />);
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();

      // With agents
      rerender(<AgentActivityPanel {...defaultProps} agents={[createAgent()]} />);
      expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
    });
  });
});
