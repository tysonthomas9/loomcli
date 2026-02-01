/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import { MonitorDashboard } from '../MonitorDashboard';

// Mock the hooks to prevent API calls in tests
const mockSetActiveView = vi.fn();

vi.mock('@/hooks', () => ({
  useAgents: () => ({
    stats: { open: 10, closed: 5, total: 15, completion: 33.3 },
    agents: [],
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
    isLoading: false,
    isConnected: true,
    connectionState: 'connected',
    wasEverConnected: true,
    retryCountdown: 0,
    error: null,
    lastUpdated: new Date(),
    refetch: vi.fn(),
    retryNow: vi.fn(),
  }),
  useBlockedIssues: () => ({
    data: [],
    loading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useIssues: () => ({
    issues: [],
    issuesMap: new Map(),
    isLoading: false,
    error: null,
    connectionState: 'connected',
    isConnected: true,
    reconnectAttempts: 0,
    refetch: vi.fn(),
    updateIssueStatus: vi.fn(),
    getIssue: vi.fn(),
    mutationCount: 0,
    retryConnection: vi.fn(),
  }),
  useViewState: () => ['monitor', mockSetActiveView],
}));

describe('MonitorDashboard', () => {
  it('renders all three panels', () => {
    render(<MonitorDashboard />);

    expect(screen.getByRole('heading', { name: /project health/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /agent activity/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /blocking dependencies/i })).toBeInTheDocument();
  });

  it('does not render Work Pipeline panel', () => {
    render(<MonitorDashboard />);

    expect(screen.queryByRole('heading', { name: /work pipeline/i })).not.toBeInTheDocument();
  });

  it('renders with testid for e2e tests', () => {
    render(<MonitorDashboard />);

    expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(<MonitorDashboard className="custom-class" />);

    const dashboard = screen.getByTestId('monitor-dashboard');
    expect(dashboard).toHaveClass('custom-class');
  });

  it('renders panels in correct order: Project Health, Agent Activity, Blocking Dependencies', () => {
    render(<MonitorDashboard />);

    const headings = screen.getAllByRole('heading', { level: 2 });
    const panelNames = headings.map((h) => h.textContent);
    expect(panelNames).toEqual(['Project Health', 'Agent Activity', 'Blocking Dependencies']);
  });

  it('renders expand button for mini graph', () => {
    render(<MonitorDashboard />);

    // The expand button is inside MiniDependencyGraph component
    const expandButton = screen.getByRole('button', { name: /expand to full graph view/i });
    expect(expandButton).toBeInTheDocument();
  });

  it('renders MiniDependencyGraph component', () => {
    render(<MonitorDashboard />);

    // MiniDependencyGraph is now implemented
    expect(screen.getByTestId('mini-dependency-graph')).toBeInTheDocument();
  });

  it('does not render WorkPipelinePanel', () => {
    render(<MonitorDashboard />);

    expect(screen.queryByTestId('work-pipeline-panel')).not.toBeInTheDocument();
  });

  it('renders AgentActivityPanel', () => {
    render(<MonitorDashboard />);

    // AgentActivityPanel is now implemented
    expect(screen.getByTestId('agent-activity-panel')).toBeInTheDocument();
  });

  it('renders ProjectHealthPanel with stats', () => {
    render(<MonitorDashboard />);

    // ProjectHealthPanel is now implemented
    expect(screen.getByTestId('project-health-panel')).toBeInTheDocument();
    expect(screen.getByText('33%')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument(); // open count
    expect(screen.getByText('5')).toBeInTheDocument(); // closed count
    expect(screen.getByText('15')).toBeInTheDocument(); // total count
  });

  it('has refresh indicator in agent activity panel', () => {
    render(<MonitorDashboard />);

    expect(screen.getByText('↻ 5s')).toBeInTheDocument();
  });
});
