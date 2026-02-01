/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentCard component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { AgentCard } from './AgentCard';
import type { LoomAgentStatus } from '@/types';

/** Helper to build a minimal agent object. */
function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: 'falcon',
    branch: 'webui/falcon',
    status: 'ready',
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

describe('AgentCard', () => {
  describe('avatar', () => {
    it('renders the first letter of the agent name', () => {
      render(<AgentCard agent={makeAgent({ name: 'nova' })} />);

      expect(screen.getByLabelText('nova avatar')).toHaveTextContent('n');
    });

    it('renders uppercase initial for uppercase name', () => {
      render(<AgentCard agent={makeAgent({ name: 'Falcon' })} />);

      expect(screen.getByLabelText('Falcon avatar')).toHaveTextContent('F');
    });

    it('applies a background color style', () => {
      render(<AgentCard agent={makeAgent({ name: 'ember' })} />);

      const avatar = screen.getByLabelText('ember avatar');
      expect(avatar.style.backgroundColor).toBeTruthy();
    });

    it('returns the same color for the same name (deterministic)', () => {
      const { unmount } = render(<AgentCard agent={makeAgent({ name: 'atlas' })} />);
      const color1 = screen.getByLabelText('atlas avatar').style.backgroundColor;
      unmount();

      render(<AgentCard agent={makeAgent({ name: 'atlas' })} />);
      const color2 = screen.getByLabelText('atlas avatar').style.backgroundColor;

      expect(color1).toBe(color2);
    });

    it('different names can produce different colors', () => {
      const { unmount } = render(<AgentCard agent={makeAgent({ name: 'aaa' })} />);
      const color1 = screen.getByLabelText('aaa avatar').style.backgroundColor;
      unmount();

      render(<AgentCard agent={makeAgent({ name: 'zzz' })} />);
      const color2 = screen.getByLabelText('zzz avatar').style.backgroundColor;

      // Not guaranteed to differ for all pairs, but these specific names should
      expect(color1).not.toBe(color2);
    });
  });

  describe('status dot', () => {
    it('renders a status dot element', () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      // The status dot is aria-hidden
      const dot = container.querySelector('[aria-hidden="true"]');
      expect(dot).toBeInTheDocument();
    });

    it('has a background color style', () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: 'working: bd-123 (5m)' })} />
      );

      const dot = container.querySelector('[aria-hidden="true"]');
      expect(dot).toBeInstanceOf(HTMLElement);
      expect((dot as HTMLElement).style.backgroundColor).toBeTruthy();
    });
  });

  describe('agent name', () => {
    it('displays the agent name', () => {
      render(<AgentCard agent={makeAgent({ name: 'nova' })} />);

      expect(screen.getByText('nova')).toBeInTheDocument();
    });
  });

  describe('status line', () => {
    it('shows "Ready" with branch for ready status', () => {
      render(<AgentCard agent={makeAgent({ status: 'ready', branch: 'main' })} />);

      expect(screen.getByText(/Ready.*main/)).toBeInTheDocument();
    });

    it('shows "Idle" with branch for idle status', () => {
      render(<AgentCard agent={makeAgent({ status: 'idle', branch: 'dev' })} />);

      expect(screen.getByText(/Idle.*dev/)).toBeInTheDocument();
    });

    it('shows "Working..." for working without task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'working', branch: 'b' })} />);

      expect(screen.getByText(/Working\.\.\./)).toBeInTheDocument();
    });

    it('shows "Working: bd-123" for working with task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'working: bd-123 (5m)', branch: 'b' })} />);

      expect(screen.getByText(/Working: bd-123.*b/)).toBeInTheDocument();
    });

    it('shows "Planning..." for planning without task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'planning', branch: 'b' })} />);

      expect(screen.getByText(/Planning\.\.\./)).toBeInTheDocument();
    });

    it('shows "Planning: bd-456" for planning with task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'planning: bd-456 (2m)', branch: 'b' })} />);

      expect(screen.getByText(/Planning: bd-456.*b/)).toBeInTheDocument();
    });

    it('shows "Done" for done without task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'done', branch: 'b' })} />);

      expect(screen.getByText(/Done.*b/)).toBeInTheDocument();
    });

    it('shows "Done: bd-789" for done with task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'done: bd-789 (10m)', branch: 'b' })} />);

      expect(screen.getByText(/Done: bd-789.*b/)).toBeInTheDocument();
    });

    it('shows "Awaiting review" for review without task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'review', branch: 'b' })} />);

      expect(screen.getByText(/Awaiting review.*b/)).toBeInTheDocument();
    });

    it('shows "Review: bd-100" for review with task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'review: bd-100 (3m)', branch: 'b' })} />);

      expect(screen.getByText(/Review: bd-100.*b/)).toBeInTheDocument();
    });

    it('shows "Error" for error without task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'error', branch: 'b' })} />);

      expect(screen.getByText(/Error.*b/)).toBeInTheDocument();
    });

    it('shows "Error: bd-999" for error with task ID', () => {
      render(<AgentCard agent={makeAgent({ status: 'error: bd-999 (1m)', branch: 'b' })} />);

      expect(screen.getByText(/Error: bd-999.*b/)).toBeInTheDocument();
    });

    it('shows "Uncommitted changes" for dirty status', () => {
      render(<AgentCard agent={makeAgent({ status: 'dirty', branch: 'b' })} />);

      expect(screen.getByText(/Uncommitted changes.*b/)).toBeInTheDocument();
    });

    it('shows "2 changes" for changes status', () => {
      render(<AgentCard agent={makeAgent({ status: '2 changes', branch: 'b' })} />);

      expect(screen.getByText(/2 changes.*b/)).toBeInTheDocument();
    });

    it('shows "1 change" (singular) for single change', () => {
      render(<AgentCard agent={makeAgent({ status: '1 change', branch: 'b' })} />);

      expect(screen.getByText(/1 change.*b/)).toBeInTheDocument();
    });

    it('includes bullet separator between label and branch', () => {
      render(<AgentCard agent={makeAgent({ status: 'ready', branch: 'main' })} />);

      // Unicode bullet \u2022
      expect(screen.getByText(/Ready \u2022 main/)).toBeInTheDocument();
    });
  });

  describe('error status data attribute', () => {
    it('sets data-error on status line when status is error', () => {
      render(<AgentCard agent={makeAgent({ status: 'error', branch: 'b' })} />);

      const statusLine = screen.getByText(/Error.*b/);
      expect(statusLine).toHaveAttribute('data-error');
    });

    it('does not set data-error for non-error statuses', () => {
      render(<AgentCard agent={makeAgent({ status: 'ready', branch: 'b' })} />);

      const statusLine = screen.getByText(/Ready.*b/);
      expect(statusLine).not.toHaveAttribute('data-error');
    });
  });

  describe('commit count', () => {
    it('shows +N when agent.ahead > 0', () => {
      render(<AgentCard agent={makeAgent({ ahead: 3 })} />);

      expect(screen.getByText('+3')).toBeInTheDocument();
    });

    it('shows correct title tooltip for commit count', () => {
      render(<AgentCard agent={makeAgent({ ahead: 5 })} />);

      expect(screen.getByTitle('5 commits ahead')).toBeInTheDocument();
    });

    it('does not show commit count when ahead is 0', () => {
      render(<AgentCard agent={makeAgent({ ahead: 0 })} />);

      expect(screen.queryByText(/^\+/)).not.toBeInTheDocument();
    });

    it('shows +1 for single commit ahead', () => {
      render(<AgentCard agent={makeAgent({ ahead: 1 })} />);

      expect(screen.getByText('+1')).toBeInTheDocument();
      expect(screen.getByTitle('1 commits ahead')).toBeInTheDocument();
    });
  });

  describe('data-status attribute', () => {
    it('sets data-status to the parsed status type', () => {
      const { container } = render(<AgentCard agent={makeAgent({ status: 'ready' })} />);

      expect(container.firstChild).toHaveAttribute('data-status', 'ready');
    });

    it('sets data-status to working for working status', () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: 'working: bd-123 (5m)' })} />
      );

      expect(container.firstChild).toHaveAttribute('data-status', 'working');
    });

    it('sets data-status to changes for changes status', () => {
      const { container } = render(<AgentCard agent={makeAgent({ status: '3 changes' })} />);

      expect(container.firstChild).toHaveAttribute('data-status', 'changes');
    });
  });

  describe('className prop', () => {
    it('applies additional className to root element', () => {
      const { container } = render(<AgentCard agent={makeAgent()} className="custom-class" />);

      expect(container.firstChild).toHaveClass('custom-class');
    });

    it('works without className prop', () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      // Should render without error
      expect(container.firstChild).toBeInTheDocument();
    });
  });

  describe('onClick handler', () => {
    it('calls onClick when clicked', () => {
      const handleClick = vi.fn();
      render(<AgentCard agent={makeAgent()} onClick={handleClick} />);

      const card = screen.getByRole('button');
      fireEvent.click(card);

      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('sets role="button" when onClick is provided', () => {
      render(<AgentCard agent={makeAgent()} onClick={() => {}} />);

      expect(screen.getByRole('button')).toBeInTheDocument();
    });

    it('does not set role when onClick is not provided', () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      expect(container.querySelector('[role="button"]')).not.toBeInTheDocument();
    });

    it('sets tabIndex=0 when onClick is provided', () => {
      render(<AgentCard agent={makeAgent()} onClick={() => {}} />);

      expect(screen.getByRole('button')).toHaveAttribute('tabindex', '0');
    });

    it('does not set tabIndex when onClick is not provided', () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      expect(container.firstChild).not.toHaveAttribute('tabindex');
    });

    it('calls onClick on Enter key', () => {
      const handleClick = vi.fn();
      render(<AgentCard agent={makeAgent()} onClick={handleClick} />);

      const card = screen.getByRole('button');
      fireEvent.keyDown(card, { key: 'Enter' });

      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('does not call onClick on other keys', () => {
      const handleClick = vi.fn();
      render(<AgentCard agent={makeAgent()} onClick={handleClick} />);

      const card = screen.getByRole('button');
      fireEvent.keyDown(card, { key: 'a' });

      expect(handleClick).not.toHaveBeenCalled();
    });
  });

  describe('taskTitle prop', () => {
    it('uses taskTitle as title attribute on status line when provided', () => {
      render(
        <AgentCard
          agent={makeAgent({ status: 'working: bd-123 (5m)', branch: 'b' })}
          taskTitle="Fix the login bug"
        />
      );

      expect(screen.getByTitle('Fix the login bug')).toBeInTheDocument();
    });

    it('uses status line text as title when taskTitle is not provided', () => {
      render(<AgentCard agent={makeAgent({ status: 'ready', branch: 'main' })} />);

      expect(screen.getByTitle('Ready \u2022 main')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles empty name gracefully', () => {
      const { container } = render(<AgentCard agent={makeAgent({ name: '' })} />);

      // aria-label will be " avatar" — verify the avatar element exists
      const avatar = container.querySelector('[aria-label=" avatar"]');
      expect(avatar).toBeInTheDocument();
    });

    it('handles unknown status string as ready', () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: 'something_unknown', branch: 'b' })} />
      );

      expect(container.firstChild).toHaveAttribute('data-status', 'ready');
      expect(screen.getByText(/Ready.*b/)).toBeInTheDocument();
    });

    it('handles large ahead count', () => {
      render(<AgentCard agent={makeAgent({ ahead: 999 })} />);

      expect(screen.getByText('+999')).toBeInTheDocument();
    });
  });
});
