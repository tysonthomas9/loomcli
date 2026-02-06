/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentRow component.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import '@testing-library/jest-dom';

import type { ParsedLoomStatus } from '@/types';

import { AgentRow } from '../AgentRow';
import type { AgentRowProps } from '../AgentRow';

/**
 * Create default props for AgentRow.
 */
function createProps(overrides: Partial<AgentRowProps> = {}): AgentRowProps {
  return {
    agentName: 'nova',
    status: null,
    avatarColor: '#4a90d9',
    ...overrides,
  };
}

/**
 * Create a mock ParsedLoomStatus.
 */
function createParsedStatus(overrides: Partial<ParsedLoomStatus> = {}): ParsedLoomStatus {
  return {
    type: 'working',
    taskId: 'bd-123',
    duration: '5m',
    raw: 'working: bd-123 (5m)',
    ...overrides,
  };
}

describe('AgentRow', () => {
  describe('rendering', () => {
    it('renders agent name', () => {
      render(<AgentRow {...createProps({ agentName: 'falcon' })} />);

      expect(screen.getByText('falcon')).toBeInTheDocument();
    });

    it('renders avatar with first letter uppercased', () => {
      render(<AgentRow {...createProps({ agentName: 'nova' })} />);

      expect(screen.getByText('N')).toBeInTheDocument();
    });

    it('renders ? for empty agent name', () => {
      render(<AgentRow {...createProps({ agentName: '' })} />);

      expect(screen.getByText('?')).toBeInTheDocument();
    });

    it('applies avatarColor as inline style', () => {
      const { container } = render(<AgentRow {...createProps({ avatarColor: '#ff5500' })} />);

      // Find the avatar div (has inline style, inside avatarContainer)
      const avatar = container.querySelector('[style]');
      expect(avatar).toBeInTheDocument();
      expect(avatar!.style.backgroundColor).toBeTruthy();
    });
  });

  describe('with agent data (status present)', () => {
    it('renders status dot when status and dotColor are provided', () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: '#22c55e',
          })}
        />
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toBeInTheDocument();
      expect(dot).toHaveStyle({ backgroundColor: '#22c55e' });
    });

    it('status dot has aria-hidden', () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: '#22c55e',
          })}
        />
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toHaveAttribute('aria-hidden', 'true');
    });

    it('renders activity text when provided', () => {
      render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            activity: 'Working: bd-123',
          })}
        />
      );

      expect(screen.getByText('Working: bd-123')).toBeInTheDocument();
    });

    it('activity text has title attribute', () => {
      render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            activity: 'Working: bd-123',
          })}
        />
      );

      const activityEl = screen.getByText('Working: bd-123');
      expect(activityEl).toHaveAttribute('title', 'Working: bd-123');
    });
  });

  describe('without agent data (status null)', () => {
    it('does not render status dot when status is null', () => {
      const { container } = render(
        <AgentRow {...createProps({ status: null, dotColor: '#22c55e' })} />
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it('does not render status dot when dotColor is undefined', () => {
      const { container } = render(
        <AgentRow
          {...createProps({
            status: createParsedStatus(),
            dotColor: undefined,
          })}
        />
      );

      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it('does not render activity when not provided', () => {
      const { container } = render(<AgentRow {...createProps({ activity: undefined })} />);

      const activity = container.querySelector('[class*="activity"]');
      expect(activity).not.toBeInTheDocument();
    });

    it('renders only name when no status or activity', () => {
      render(<AgentRow {...createProps({ agentName: 'nova', status: null })} />);

      expect(screen.getByText('nova')).toBeInTheDocument();
      expect(screen.getByText('N')).toBeInTheDocument();
    });
  });

  describe('[H] prefix stripping', () => {
    it('strips [H] prefix from display name', () => {
      render(<AgentRow {...createProps({ agentName: '[H] Alice' })} />);

      expect(screen.getByText('Alice')).toBeInTheDocument();
      expect(screen.queryByText('[H] Alice')).not.toBeInTheDocument();
    });

    it('strips [H] prefix without space after bracket', () => {
      render(<AgentRow {...createProps({ agentName: '[H]Bob' })} />);

      expect(screen.getByText('Bob')).toBeInTheDocument();
    });

    it('avatar initial uses stripped display name', () => {
      render(<AgentRow {...createProps({ agentName: '[H] Alice' })} />);

      // Initial is from the stripped displayName, so 'A' for 'Alice'
      expect(screen.getByText('A')).toBeInTheDocument();
    });

    it('does not strip [H] from middle of name', () => {
      render(<AgentRow {...createProps({ agentName: 'agent [H] test' })} />);

      expect(screen.getByText('agent [H] test')).toBeInTheDocument();
    });

    it('handles name that is only [H]', () => {
      render(<AgentRow {...createProps({ agentName: '[H]' })} />);

      // After stripping "[H]", display name should be empty string
      // The name span will exist but be empty
      const { container } = render(<AgentRow {...createProps({ agentName: '[H]' })} />);
      const nameSpan = container.querySelector('[class*="name"]');
      expect(nameSpan).toBeInTheDocument();
      expect(nameSpan?.textContent).toBe('');
    });
  });

  describe('CSS classes', () => {
    it('renders with agentRow class', () => {
      const { container } = render(<AgentRow {...createProps()} />);

      const row = container.firstChild;
      expect((row as HTMLElement).className).toMatch(/agentRow/);
    });

    it('renders avatar with avatar class', () => {
      const { container } = render(<AgentRow {...createProps()} />);

      const avatar = container.querySelector('[class*="avatar"]');
      expect(avatar).toBeInTheDocument();
    });

    it('renders name with name class', () => {
      const { container } = render(<AgentRow {...createProps()} />);

      const name = container.querySelector('[class*="name"]');
      expect(name).toBeInTheDocument();
    });
  });
});
