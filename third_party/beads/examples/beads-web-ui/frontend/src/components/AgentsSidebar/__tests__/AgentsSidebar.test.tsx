/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentsSidebar component.
 * Focuses on the viewSwitcher slot behavior.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';

import '@testing-library/jest-dom';
import { AgentsSidebar } from '../AgentsSidebar';

// Mock the hooks to prevent API calls in tests
vi.mock('@/hooks', () => ({
  useAgentContext: () => ({
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
    stats: { open: 0, closed: 0, total: 0, completion: 0 },
    isLoading: false,
    isConnected: true,
    lastUpdated: new Date(),
  }),
}));

describe('AgentsSidebar', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  describe('viewSwitcher slot', () => {
    it('renders viewSwitcher content when provided', () => {
      render(<AgentsSidebar viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>} />);

      expect(screen.getByTestId('custom-switcher')).toBeInTheDocument();
      expect(screen.getByText('My Switcher')).toBeInTheDocument();
    });

    it('does not render viewSwitcher when collapsed', () => {
      render(
        <AgentsSidebar
          defaultCollapsed={true}
          viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>}
        />
      );

      expect(screen.queryByTestId('custom-switcher')).not.toBeInTheDocument();
    });

    it('does not render viewSwitcher slot when prop is not provided', () => {
      const { container } = render(<AgentsSidebar />);

      // The viewSwitcherSlot wrapper div should not be present
      const slotDiv = container.querySelector('[class*="viewSwitcherSlot"]');
      expect(slotDiv).not.toBeInTheDocument();
    });

    it('hides viewSwitcher when sidebar is collapsed via toggle', () => {
      render(<AgentsSidebar viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>} />);

      // Switcher should be visible initially
      expect(screen.getByTestId('custom-switcher')).toBeInTheDocument();

      // Click the collapse toggle button
      const toggleButton = screen.getByRole('button', { name: /collapse agents sidebar/i });
      fireEvent.click(toggleButton);

      // Switcher should be hidden after collapse
      expect(screen.queryByTestId('custom-switcher')).not.toBeInTheDocument();
    });

    it('shows viewSwitcher again when sidebar is expanded after collapse', () => {
      render(
        <AgentsSidebar
          defaultCollapsed={true}
          viewSwitcher={<div data-testid="custom-switcher">My Switcher</div>}
        />
      );

      // Initially collapsed, switcher should not be visible
      expect(screen.queryByTestId('custom-switcher')).not.toBeInTheDocument();

      // Expand
      const expandButton = screen.getByRole('button', { name: /expand agents sidebar/i });
      fireEvent.click(expandButton);
      expect(screen.getByTestId('custom-switcher')).toBeInTheDocument();
    });
  });
});
