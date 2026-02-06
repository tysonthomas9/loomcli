/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EmptyColumn component.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import '@testing-library/jest-dom';

import { EmptyColumn } from '../EmptyColumn';

describe('EmptyColumn', () => {
  describe('rendering', () => {
    it('renders with default message when no props', () => {
      render(<EmptyColumn />);

      expect(screen.getByText('No issues')).toBeInTheDocument();
    });

    it('renders icon by default', () => {
      const { container } = render(<EmptyColumn />);

      const svg = container.querySelector('svg');
      expect(svg).toBeInTheDocument();
    });

    it('renders correct status-specific message for open status', () => {
      render(<EmptyColumn status="open" />);

      expect(screen.getByText('No open issues')).toBeInTheDocument();
    });

    it('renders correct status-specific message for in_progress status', () => {
      render(<EmptyColumn status="in_progress" />);

      expect(screen.getByText('No issues in progress')).toBeInTheDocument();
    });

    it('renders correct status-specific message for closed status', () => {
      render(<EmptyColumn status="closed" />);

      expect(screen.getByText('No closed issues')).toBeInTheDocument();
    });

    it('renders correct status-specific message for blocked status', () => {
      render(<EmptyColumn status="blocked" />);

      expect(screen.getByText('No blocked issues')).toBeInTheDocument();
    });

    it('renders correct status-specific message for deferred status', () => {
      render(<EmptyColumn status="deferred" />);

      expect(screen.getByText('No deferred issues')).toBeInTheDocument();
    });

    it('renders correct status-specific message for tombstone status', () => {
      render(<EmptyColumn status="tombstone" />);

      expect(screen.getByText('No archived issues')).toBeInTheDocument();
    });

    it('renders correct status-specific message for pinned status', () => {
      render(<EmptyColumn status="pinned" />);

      expect(screen.getByText('No pinned issues')).toBeInTheDocument();
    });

    it('renders correct status-specific message for hooked status', () => {
      render(<EmptyColumn status="hooked" />);

      expect(screen.getByText('No hooked issues')).toBeInTheDocument();
    });

    it('custom message overrides status message', () => {
      render(<EmptyColumn status="open" message="Custom empty message" />);

      expect(screen.getByText('Custom empty message')).toBeInTheDocument();
      expect(screen.queryByText('No open issues')).not.toBeInTheDocument();
    });

    it('showIcon=false hides icon', () => {
      const { container } = render(<EmptyColumn showIcon={false} />);

      const svg = container.querySelector('svg');
      expect(svg).not.toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('className prop applied to root element', () => {
      const { container } = render(<EmptyColumn className="custom-class" />);

      const root = container.firstChild;
      expect(root).toHaveClass('custom-class');
    });

    it('unknown status uses default message', () => {
      render(<EmptyColumn status="unknown_custom_status" />);

      expect(screen.getByText('No issues')).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has role="status"', () => {
      render(<EmptyColumn />);

      expect(screen.getByRole('status')).toBeInTheDocument();
    });

    it('has aria-label matching message', () => {
      render(<EmptyColumn status="open" />);

      const statusElement = screen.getByRole('status');
      expect(statusElement).toHaveAttribute('aria-label', 'No open issues');
    });

    it('has aria-label matching custom message when provided', () => {
      render(<EmptyColumn message="All done!" />);

      const statusElement = screen.getByRole('status');
      expect(statusElement).toHaveAttribute('aria-label', 'All done!');
    });

    it('icon has aria-hidden="true"', () => {
      const { container } = render(<EmptyColumn />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('aria-hidden', 'true');
    });
  });

  describe('backlog status (Pending→Backlog rename)', () => {
    it('renders correct message for backlog status', () => {
      render(<EmptyColumn status="backlog" />);

      expect(screen.getByText('No blocked or deferred issues')).toBeInTheDocument();
    });

    it('renders backlog message in aria-label', () => {
      render(<EmptyColumn status="backlog" />);

      const statusElement = screen.getByRole('status');
      expect(statusElement).toHaveAttribute('aria-label', 'No blocked or deferred issues');
    });

    it('pending status falls through to default message after rename', () => {
      render(<EmptyColumn status="pending" />);

      expect(screen.getByText('No issues')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('renders with all props provided', () => {
      const { container } = render(
        <EmptyColumn
          status="open"
          message="Override message"
          showIcon={true}
          className="extra-class"
        />
      );

      expect(screen.getByText('Override message')).toBeInTheDocument();
      expect(container.querySelector('svg')).toBeInTheDocument();
      expect(container.firstChild).toHaveClass('extra-class');
    });

    it('renders each known status correctly', () => {
      const statuses = [
        'open',
        'in_progress',
        'closed',
        'blocked',
        'deferred',
        'tombstone',
        'pinned',
        'hooked',
      ] as const;
      const expectedMessages: Record<(typeof statuses)[number], string> = {
        open: 'No open issues',
        in_progress: 'No issues in progress',
        closed: 'No closed issues',
        blocked: 'No blocked issues',
        deferred: 'No deferred issues',
        tombstone: 'No archived issues',
        pinned: 'No pinned issues',
        hooked: 'No hooked issues',
      };

      statuses.forEach((status) => {
        const { unmount } = render(<EmptyColumn status={status} />);

        expect(screen.getByText(expectedMessages[status])).toBeInTheDocument();

        unmount();
      });
    });
  });
});
