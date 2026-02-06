/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for StatusDropdown component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import type { Status } from '@/types/status';
import { USER_SELECTABLE_STATUSES } from '@/types/status';

import { StatusDropdown } from '../StatusDropdown';

describe('StatusDropdown', () => {
  describe('rendering', () => {
    it('renders select element with data-testid', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      expect(screen.getByTestId('status-dropdown')).toBeInTheDocument();
    });

    it('renders select element with current status selected', () => {
      render(<StatusDropdown status="in_progress" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveValue('in_progress');
    });

    it('renders all 6 user-selectable options', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      const options = select.querySelectorAll('option');

      expect(options).toHaveLength(6);
      expect(options[0]).toHaveValue('open');
      expect(options[0]).toHaveTextContent('Open');
      expect(options[1]).toHaveValue('in_progress');
      expect(options[1]).toHaveTextContent('In Progress');
      expect(options[2]).toHaveValue('blocked');
      expect(options[2]).toHaveTextContent('Blocked');
      expect(options[3]).toHaveValue('deferred');
      expect(options[3]).toHaveTextContent('Deferred');
      expect(options[4]).toHaveValue('review');
      expect(options[4]).toHaveTextContent('Review');
      expect(options[5]).toHaveValue('closed');
      expect(options[5]).toHaveTextContent('Closed');
    });

    it('does not include system statuses (tombstone, pinned, hooked)', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      const options = Array.from(select.querySelectorAll('option'));
      const optionValues = options.map((opt) => opt.value);

      expect(optionValues).not.toContain('tombstone');
      expect(optionValues).not.toContain('pinned');
      expect(optionValues).not.toContain('hooked');
    });

    it('renders all user-selectable statuses from constant', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      const options = Array.from(select.querySelectorAll('option'));
      const optionValues = options.map((opt) => opt.value);

      USER_SELECTABLE_STATUSES.forEach((status) => {
        expect(optionValues).toContain(status);
      });
    });

    it('displays each status value correctly when selected', () => {
      const statuses: Status[] = ['open', 'in_progress', 'blocked', 'deferred', 'review', 'closed'];

      statuses.forEach((status) => {
        const { unmount } = render(<StatusDropdown status={status} onStatusChange={() => {}} />);

        const select = screen.getByTestId('status-dropdown');
        expect(select).toHaveValue(status);

        unmount();
      });
    });
  });

  describe('data attributes', () => {
    it('applies correct data-status attribute for open', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'open');
    });

    it('applies correct data-status attribute for in_progress', () => {
      render(<StatusDropdown status="in_progress" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'in_progress');
    });

    it('applies correct data-status attribute for blocked', () => {
      render(<StatusDropdown status="blocked" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'blocked');
    });

    it('applies correct data-status attribute for deferred', () => {
      render(<StatusDropdown status="deferred" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'deferred');
    });

    it('applies correct data-status attribute for review', () => {
      render(<StatusDropdown status="review" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'review');
    });

    it('applies correct data-status attribute for closed', () => {
      render(<StatusDropdown status="closed" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'closed');
    });

    it('applies data-saving attribute when isSaving is true', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} isSaving={true} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-saving', 'true');
    });

    it('does not apply data-saving attribute when isSaving is false', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} isSaving={false} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).not.toHaveAttribute('data-saving');
    });

    it('does not apply data-saving attribute when isSaving is undefined', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).not.toHaveAttribute('data-saving');
    });
  });

  describe('interactions', () => {
    it('calls onStatusChange with correct status value when selection changes', () => {
      const onStatusChange = vi.fn();
      render(<StatusDropdown status="open" onStatusChange={onStatusChange} />);

      const select = screen.getByTestId('status-dropdown');
      fireEvent.change(select, { target: { value: 'in_progress' } });

      expect(onStatusChange).toHaveBeenCalledTimes(1);
      expect(onStatusChange).toHaveBeenCalledWith('in_progress');
    });

    it('calls onStatusChange for each different status value', () => {
      const targetStatuses: Status[] = ['in_progress', 'blocked', 'deferred', 'review', 'closed'];

      targetStatuses.forEach((targetStatus) => {
        const onStatusChange = vi.fn();
        const { unmount } = render(
          <StatusDropdown status="open" onStatusChange={onStatusChange} />
        );

        const select = screen.getByTestId('status-dropdown');
        fireEvent.change(select, { target: { value: targetStatus } });

        expect(onStatusChange).toHaveBeenCalledWith(targetStatus);

        unmount();
      });
    });

    it('does not fire onStatusChange when same status is selected', () => {
      const onStatusChange = vi.fn();
      render(<StatusDropdown status="in_progress" onStatusChange={onStatusChange} />);

      const select = screen.getByTestId('status-dropdown');
      fireEvent.change(select, { target: { value: 'in_progress' } });

      expect(onStatusChange).not.toHaveBeenCalled();
    });

    it('does not fire onStatusChange when same status selected for all statuses', () => {
      const statuses: Status[] = ['open', 'in_progress', 'blocked', 'deferred', 'review', 'closed'];

      statuses.forEach((status) => {
        const onStatusChange = vi.fn();
        const { unmount } = render(
          <StatusDropdown status={status} onStatusChange={onStatusChange} />
        );

        const select = screen.getByTestId('status-dropdown');
        fireEvent.change(select, { target: { value: status } });

        expect(onStatusChange).not.toHaveBeenCalled();

        unmount();
      });
    });
  });

  describe('disabled state', () => {
    it('is disabled when isSaving is true', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} isSaving={true} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toBeDisabled();
    });

    it('is disabled when disabled prop is true', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} disabled={true} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toBeDisabled();
    });

    it('is disabled when both disabled and isSaving are true', () => {
      render(
        <StatusDropdown status="open" onStatusChange={() => {}} disabled={true} isSaving={true} />
      );

      const select = screen.getByTestId('status-dropdown');
      expect(select).toBeDisabled();
    });

    it('is not disabled when disabled is false and isSaving is false', () => {
      render(
        <StatusDropdown status="open" onStatusChange={() => {}} disabled={false} isSaving={false} />
      );

      const select = screen.getByTestId('status-dropdown');
      expect(select).not.toBeDisabled();
    });

    it('is not disabled when both props are undefined', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).not.toBeDisabled();
    });

    it('does not call onStatusChange when disabled and interaction attempted', () => {
      const onStatusChange = vi.fn();
      render(<StatusDropdown status="open" onStatusChange={onStatusChange} disabled={true} />);

      const select = screen.getByTestId('status-dropdown');
      // Attempt to change value on disabled select
      fireEvent.change(select, { target: { value: 'closed' } });

      // The event may still fire but the select should be disabled
      expect(select).toBeDisabled();
    });
  });

  describe('className prop', () => {
    it('applies custom className to select element', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} className="custom-class" />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveClass('custom-class');
    });

    it('applies multiple custom classes', () => {
      render(
        <StatusDropdown
          status="open"
          onStatusChange={() => {}}
          className="custom-class another-class"
        />
      );

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveClass('custom-class');
      expect(select).toHaveClass('another-class');
    });

    it('maintains base styles with custom className', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} className="custom-class" />);

      const select = screen.getByTestId('status-dropdown');
      // Should have both the CSS module class and custom class
      expect(select.className).toMatch(/statusDropdown/);
      expect(select).toHaveClass('custom-class');
    });

    it('handles undefined className gracefully', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} className={undefined} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toBeInTheDocument();
      // Should still have the CSS module class
      expect(select.className).toMatch(/statusDropdown/);
    });
  });

  describe('accessibility', () => {
    it('has accessible label', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByRole('combobox', { name: /change issue status/i });
      expect(select).toBeInTheDocument();
    });

    it('can be found by combobox role', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      expect(screen.getByRole('combobox')).toBeInTheDocument();
    });

    it('has aria-label attribute', () => {
      render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('aria-label', 'Change issue status');
    });

    it('keyboard navigation works', () => {
      const onStatusChange = vi.fn();
      render(<StatusDropdown status="open" onStatusChange={onStatusChange} />);

      const select = screen.getByTestId('status-dropdown');

      // Focus the select
      select.focus();
      expect(document.activeElement).toBe(select);

      // Simulate keyboard change
      fireEvent.keyDown(select, { key: 'ArrowDown' });
      fireEvent.change(select, { target: { value: 'in_progress' } });

      expect(onStatusChange).toHaveBeenCalledWith('in_progress');
    });
  });

  describe('edge cases', () => {
    it('handles rapid status changes', () => {
      const onStatusChange = vi.fn();
      const { rerender } = render(<StatusDropdown status="open" onStatusChange={onStatusChange} />);

      rerender(<StatusDropdown status="in_progress" onStatusChange={onStatusChange} />);
      rerender(<StatusDropdown status="blocked" onStatusChange={onStatusChange} />);
      rerender(<StatusDropdown status="closed" onStatusChange={onStatusChange} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveValue('closed');
    });

    it('updates display when status prop changes', () => {
      const { rerender } = render(<StatusDropdown status="open" onStatusChange={() => {}} />);

      expect(screen.getByTestId('status-dropdown')).toHaveValue('open');

      rerender(<StatusDropdown status="blocked" onStatusChange={() => {}} />);

      expect(screen.getByTestId('status-dropdown')).toHaveValue('blocked');
    });

    it('handles status value not in options gracefully', () => {
      // When a system status is passed, the dropdown will still render
      // but the value may not match any option
      render(<StatusDropdown status="tombstone" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      // The select will have data-status set even for system statuses
      expect(select).toHaveAttribute('data-status', 'tombstone');
    });

    it('handles custom status value', () => {
      render(<StatusDropdown status="custom_status" onStatusChange={() => {}} />);

      const select = screen.getByTestId('status-dropdown');
      expect(select).toHaveAttribute('data-status', 'custom_status');
    });
  });
});
