/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for PriorityDropdown component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import { PriorityDropdown } from '../PriorityDropdown';
import type { Priority } from '@/types';

describe('PriorityDropdown', () => {
  const defaultProps = {
    priority: 2 as Priority,
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Display', () => {
    it('renders current priority as colored badge with correct label', () => {
      render(<PriorityDropdown {...defaultProps} priority={2} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P2 - Medium');
    });

    it('renders P0 - Critical correctly', () => {
      render(<PriorityDropdown {...defaultProps} priority={0} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P0 - Critical');
      expect(trigger).toHaveAttribute('data-priority', '0');
    });

    it('renders P1 - High correctly', () => {
      render(<PriorityDropdown {...defaultProps} priority={1} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P1 - High');
      expect(trigger).toHaveAttribute('data-priority', '1');
    });

    it('renders P2 - Medium correctly', () => {
      render(<PriorityDropdown {...defaultProps} priority={2} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P2 - Medium');
      expect(trigger).toHaveAttribute('data-priority', '2');
    });

    it('renders P3 - Normal correctly', () => {
      render(<PriorityDropdown {...defaultProps} priority={3} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P3 - Normal');
      expect(trigger).toHaveAttribute('data-priority', '3');
    });

    it('renders P4 - Backlog correctly', () => {
      render(<PriorityDropdown {...defaultProps} priority={4} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P4 - Backlog');
      expect(trigger).toHaveAttribute('data-priority', '4');
    });

    it('renders color dot with correct data-priority attribute', () => {
      render(<PriorityDropdown {...defaultProps} priority={1} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      const colorDot = trigger.querySelector('[class*="colorDot"]');
      expect(colorDot).toHaveAttribute('data-priority', '1');
    });

    it('renders dropdown arrow', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('▾');
    });

    it('applies custom className', () => {
      render(<PriorityDropdown {...defaultProps} className="custom-class" />);
      const container = screen.getByTestId('priority-dropdown-trigger').parentElement;
      expect(container).toHaveClass('custom-class');
    });

    it('defaults to Medium (P2) for invalid priority', () => {
      // TypeScript prevents this at compile time, but testing runtime fallback
      render(<PriorityDropdown {...defaultProps} priority={99 as Priority} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P2 - Medium');
    });
  });

  describe('Dropdown behavior', () => {
    it('opens dropdown menu on click', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();
    });

    it('closes dropdown on Escape key', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();

      fireEvent.keyDown(document, { key: 'Escape' });
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();
    });

    it('closes dropdown on click outside', () => {
      render(
        <div>
          <PriorityDropdown {...defaultProps} />
          <button data-testid="outside-button">Outside</button>
        </div>
      );
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId('outside-button'));
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();
    });

    it('closes dropdown after selection', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<PriorityDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();
    });

    it('toggles dropdown on repeated clicks', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();
    });

    it('renders all 5 priority options in menu', () => {
      render(<PriorityDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));

      expect(screen.getByTestId('priority-option-0')).toHaveTextContent('P0 - Critical');
      expect(screen.getByTestId('priority-option-1')).toHaveTextContent('P1 - High');
      expect(screen.getByTestId('priority-option-2')).toHaveTextContent('P2 - Medium');
      expect(screen.getByTestId('priority-option-3')).toHaveTextContent('P3 - Normal');
      expect(screen.getByTestId('priority-option-4')).toHaveTextContent('P4 - Backlog');
    });

    it('returns focus to trigger when closed with Escape', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      fireEvent.click(trigger);

      fireEvent.keyDown(document, { key: 'Escape' });
      expect(document.activeElement).toBe(trigger);
    });

    it('does not open when disabled', () => {
      render(<PriorityDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();
    });

    it('does not open when saving', () => {
      render(<PriorityDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();
    });
  });

  describe('Selection', () => {
    it('calls onSave with selected priority', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith(0);
      });
    });

    it('shows checkmark on current option', () => {
      render(<PriorityDropdown {...defaultProps} priority={2} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));

      const currentOption = screen.getByTestId('priority-option-2');
      expect(currentOption).toHaveTextContent('✓');
      expect(currentOption).toHaveAttribute('data-selected', 'true');

      // Other options should not have checkmark
      expect(screen.getByTestId('priority-option-0')).not.toHaveTextContent('✓');
      expect(screen.getByTestId('priority-option-1')).not.toHaveTextContent('✓');
      expect(screen.getByTestId('priority-option-3')).not.toHaveTextContent('✓');
      expect(screen.getByTestId('priority-option-4')).not.toHaveTextContent('✓');
    });

    it('updates display immediately (optimistic update)', async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      // Should show P0 immediately before save completes
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveTextContent('P0 - Critical');
      expect(trigger).toHaveAttribute('data-priority', '0');

      await act(async () => {
        resolvePromise!();
      });
    });

    it('does not call onSave when selecting same priority', async () => {
      const onSave = vi.fn();
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-2'));
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it('calls onSave for each different priority value', async () => {
      const priorities = [0, 1, 3, 4] as Priority[];

      for (const targetPriority of priorities) {
        const onSave = vi.fn().mockResolvedValue(undefined);
        const { unmount } = render(
          <PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />
        );

        fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
        await act(async () => {
          fireEvent.click(screen.getByTestId(`priority-option-${targetPriority}`));
        });

        await waitFor(() => {
          expect(onSave).toHaveBeenCalledWith(targetPriority);
        });

        unmount();
      }
    });
  });

  describe('Loading state', () => {
    it('disables trigger when isSaving is true', () => {
      render(<PriorityDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toBeDisabled();
    });

    it('shows saving indicator when isSaving is true', () => {
      render(<PriorityDropdown {...defaultProps} isSaving />);
      expect(screen.getByTestId('priority-saving')).toBeInTheDocument();
    });

    it('has aria-label on saving indicator', () => {
      render(<PriorityDropdown {...defaultProps} isSaving />);
      const savingIndicator = screen.getByTestId('priority-saving');
      expect(savingIndicator).toHaveAttribute('aria-label', 'Saving...');
    });

    it('applies data-saving attribute to trigger', () => {
      render(<PriorityDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveAttribute('data-saving', 'true');
    });

    it('does not show saving indicator when not saving', () => {
      render(<PriorityDropdown {...defaultProps} isSaving={false} />);
      expect(screen.queryByTestId('priority-saving')).not.toBeInTheDocument();
    });

    it('does not apply data-saving attribute when not saving', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).not.toHaveAttribute('data-saving');
    });

    it('disables trigger when disabled prop is true', () => {
      render(<PriorityDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toBeDisabled();
    });

    it('disables trigger when both disabled and isSaving are true', () => {
      render(<PriorityDropdown {...defaultProps} disabled isSaving />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toBeDisabled();
    });
  });

  describe('Error handling', () => {
    it('reverts to previous priority on save failure', async () => {
      let rejectPromise: (error: Error) => void;
      const savePromise = new Promise<void>((_, reject) => {
        rejectPromise = reject;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      // Initially shows optimistic value
      expect(screen.getByTestId('priority-dropdown-trigger')).toHaveTextContent('P0 - Critical');

      // Reject the promise
      await act(async () => {
        rejectPromise!(new Error('Save failed'));
      });

      // After error, should revert
      await waitFor(() => {
        expect(screen.getByTestId('priority-dropdown-trigger')).toHaveTextContent('P2 - Medium');
      });
    });

    it('displays error message on save failure', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Network error'));
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('priority-error')).toHaveTextContent('Network error');
      });
    });

    it('displays generic error for non-Error exceptions', async () => {
      const onSave = vi.fn().mockRejectedValue('string error');
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('priority-error')).toHaveTextContent('Failed to update priority');
      });
    });

    it('error has role="alert"', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Save failed'));
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(screen.getByRole('alert')).toHaveTextContent('Save failed');
      });
    });

    it('clears error when dropdown is opened', async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error('First error'))
        .mockResolvedValueOnce(undefined);

      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('priority-error')).toBeInTheDocument();
      });

      // Open dropdown again - error should be cleared
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      expect(screen.queryByTestId('priority-error')).not.toBeInTheDocument();
    });

    it('allows retry after failure', async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error('First error'))
        .mockResolvedValueOnce(undefined);

      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('priority-error')).toBeInTheDocument();
      });

      // Retry should succeed
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      await waitFor(() => {
        expect(screen.queryByTestId('priority-error')).not.toBeInTheDocument();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
    });
  });

  describe('Accessibility', () => {
    it('has aria-expanded attribute reflecting dropdown state', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      expect(trigger).toHaveAttribute('aria-expanded', 'false');

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute('aria-expanded', 'true');

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute('aria-expanded', 'false');
    });

    it('has aria-haspopup="listbox" on trigger', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
    });

    it('has aria-label on trigger', () => {
      render(<PriorityDropdown {...defaultProps} priority={2} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger).toHaveAttribute(
        'aria-label',
        'Priority: P2 - Medium. Click to change.'
      );
    });

    it('menu has role="listbox"', () => {
      render(<PriorityDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      expect(screen.getByRole('listbox')).toBeInTheDocument();
    });

    it('menu has aria-label', () => {
      render(<PriorityDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      expect(screen.getByRole('listbox')).toHaveAttribute('aria-label', 'Select priority');
    });

    it('options have role="option"', () => {
      render(<PriorityDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));

      const options = screen.getAllByRole('option');
      expect(options).toHaveLength(5);
    });

    it('current option has aria-selected="true"', () => {
      render(<PriorityDropdown {...defaultProps} priority={2} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));

      const options = screen.getAllByRole('option');
      expect(options[2]).toHaveAttribute('aria-selected', 'true');
      expect(options[0]).toHaveAttribute('aria-selected', 'false');
      expect(options[1]).toHaveAttribute('aria-selected', 'false');
      expect(options[3]).toHaveAttribute('aria-selected', 'false');
      expect(options[4]).toHaveAttribute('aria-selected', 'false');
    });

    it('trigger is a button with type="button"', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');
      expect(trigger.tagName).toBe('BUTTON');
      expect(trigger).toHaveAttribute('type', 'button');
    });
  });

  describe('Keyboard navigation', () => {
    it('opens dropdown with Enter key', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.keyDown(trigger, { key: 'Enter' });
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();
    });

    it('opens dropdown with Space key', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.keyDown(trigger, { key: ' ' });
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();
    });

    it('navigates down with ArrowDown key', () => {
      render(<PriorityDropdown {...defaultProps} priority={0} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);

      // Initially focused on current priority (P0, index 0)
      expect(screen.getByTestId('priority-option-0')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('priority-option-1')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('priority-option-2')).toHaveAttribute('data-focused', 'true');
    });

    it('navigates up with ArrowUp key', () => {
      render(<PriorityDropdown {...defaultProps} priority={4} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);

      // Initially focused on current priority (P4, index 4)
      expect(screen.getByTestId('priority-option-4')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      expect(screen.getByTestId('priority-option-3')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      expect(screen.getByTestId('priority-option-2')).toHaveAttribute('data-focused', 'true');
    });

    it('stops at first option when pressing ArrowUp', () => {
      render(<PriorityDropdown {...defaultProps} priority={0} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-option-0')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      expect(screen.getByTestId('priority-option-0')).toHaveAttribute('data-focused', 'true');
    });

    it('stops at last option when pressing ArrowDown', () => {
      render(<PriorityDropdown {...defaultProps} priority={4} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-option-4')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('priority-option-4')).toHaveAttribute('data-focused', 'true');
    });

    it('selects focused option with Enter key', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      fireEvent.keyDown(trigger, { key: 'ArrowDown' });

      // Now focused on index 4 (P4)
      expect(screen.getByTestId('priority-option-4')).toHaveAttribute('data-focused', 'true');

      await act(async () => {
        fireEvent.keyDown(trigger, { key: 'Enter' });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith(4);
      });
    });

    it('selects focused option with Space key', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      fireEvent.keyDown(trigger, { key: 'ArrowUp' });

      // Now focused on index 0 (P0)
      expect(screen.getByTestId('priority-option-0')).toHaveAttribute('data-focused', 'true');

      await act(async () => {
        fireEvent.keyDown(trigger, { key: ' ' });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith(0);
      });
    });

    it('closes dropdown with Escape key', () => {
      render(<PriorityDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();

      fireEvent.keyDown(trigger, { key: 'Escape' });
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();
    });

    it('navigates to first option with Home key', () => {
      render(<PriorityDropdown {...defaultProps} priority={4} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-option-4')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'Home' });
      expect(screen.getByTestId('priority-option-0')).toHaveAttribute('data-focused', 'true');
    });

    it('navigates to last option with End key', () => {
      render(<PriorityDropdown {...defaultProps} priority={0} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-option-0')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'End' });
      expect(screen.getByTestId('priority-option-4')).toHaveAttribute('data-focused', 'true');
    });

    it('sets initial focus to current priority when opening', () => {
      render(<PriorityDropdown {...defaultProps} priority={3} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('priority-option-3')).toHaveAttribute('data-focused', 'true');
    });

    it('resets focus when dropdown closes', () => {
      render(<PriorityDropdown {...defaultProps} priority={2} />);
      const trigger = screen.getByTestId('priority-dropdown-trigger');

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('priority-option-3')).toHaveAttribute('data-focused', 'true');

      // Close and reopen
      fireEvent.keyDown(trigger, { key: 'Escape' });
      fireEvent.click(trigger);

      // Should be back to current priority
      expect(screen.getByTestId('priority-option-2')).toHaveAttribute('data-focused', 'true');
    });
  });

  describe('Props sync', () => {
    it('syncs optimistic priority when prop changes', () => {
      const { rerender } = render(<PriorityDropdown {...defaultProps} priority={2} />);
      expect(screen.getByTestId('priority-dropdown-trigger')).toHaveTextContent('P2 - Medium');

      rerender(<PriorityDropdown {...defaultProps} priority={1} />);
      expect(screen.getByTestId('priority-dropdown-trigger')).toHaveTextContent('P1 - High');
    });

    it('updates checkmark when priority prop changes', () => {
      const { rerender } = render(<PriorityDropdown {...defaultProps} priority={2} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      expect(screen.getByTestId('priority-option-2')).toHaveAttribute('data-selected', 'true');

      // Close and update priority
      fireEvent.keyDown(document, { key: 'Escape' });
      rerender(<PriorityDropdown {...defaultProps} priority={1} />);

      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      expect(screen.getByTestId('priority-option-1')).toHaveAttribute('data-selected', 'true');
      expect(screen.getByTestId('priority-option-2')).not.toHaveAttribute('data-selected');
    });

    it('updates aria-label when priority prop changes', () => {
      const { rerender } = render(<PriorityDropdown {...defaultProps} priority={2} />);
      expect(screen.getByTestId('priority-dropdown-trigger')).toHaveAttribute(
        'aria-label',
        'Priority: P2 - Medium. Click to change.'
      );

      rerender(<PriorityDropdown {...defaultProps} priority={0} />);
      expect(screen.getByTestId('priority-dropdown-trigger')).toHaveAttribute(
        'aria-label',
        'Priority: P0 - Critical. Click to change.'
      );
    });
  });

  describe('Edge cases', () => {
    it('handles rapid priority changes', () => {
      const { rerender } = render(<PriorityDropdown {...defaultProps} priority={0} />);

      rerender(<PriorityDropdown {...defaultProps} priority={1} />);
      rerender(<PriorityDropdown {...defaultProps} priority={2} />);
      rerender(<PriorityDropdown {...defaultProps} priority={3} />);
      rerender(<PriorityDropdown {...defaultProps} priority={4} />);

      expect(screen.getByTestId('priority-dropdown-trigger')).toHaveTextContent('P4 - Backlog');
    });

    it('handles click on menu without selecting (click on menu background)', async () => {
      render(<PriorityDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));

      const menu = screen.getByTestId('priority-dropdown-menu');
      fireEvent.click(menu);

      // Menu should still be open (click on menu itself, not an option)
      expect(screen.getByTestId('priority-dropdown-menu')).toBeInTheDocument();
    });

    it('does not fire multiple saves for rapid selections', async () => {
      let resolveFirst: () => void;
      const firstPromise = new Promise<void>((resolve) => {
        resolveFirst = resolve;
      });
      const onSave = vi.fn().mockReturnValueOnce(firstPromise).mockResolvedValue(undefined);

      render(<PriorityDropdown {...defaultProps} priority={2} onSave={onSave} />);

      // First selection
      fireEvent.click(screen.getByTestId('priority-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-0'));
      });

      // Dropdown is closed after first selection
      expect(screen.queryByTestId('priority-dropdown-menu')).not.toBeInTheDocument();

      // Resolve first save
      await act(async () => {
        resolveFirst!();
      });

      expect(onSave).toHaveBeenCalledTimes(1);
    });

    it('handles undefined className gracefully', () => {
      render(<PriorityDropdown {...defaultProps} className={undefined} />);
      const container = screen.getByTestId('priority-dropdown-trigger').parentElement;
      expect(container).toBeInTheDocument();
    });
  });
});
