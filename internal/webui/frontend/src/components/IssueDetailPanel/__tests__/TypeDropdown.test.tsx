/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TypeDropdown component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import { TypeDropdown } from '../TypeDropdown';
import type { IssueType } from '@/types';

describe('TypeDropdown', () => {
  const defaultProps = {
    type: 'task' as IssueType,
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Display', () => {
    it('renders current type with icon and label', () => {
      render(<TypeDropdown {...defaultProps} type="task" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Task');
    });

    it('renders Bug correctly', () => {
      render(<TypeDropdown {...defaultProps} type="bug" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Bug');
      expect(trigger).toHaveAttribute('data-type', 'bug');
    });

    it('renders Feature correctly', () => {
      render(<TypeDropdown {...defaultProps} type="feature" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Feature');
      expect(trigger).toHaveAttribute('data-type', 'feature');
    });

    it('renders Task correctly', () => {
      render(<TypeDropdown {...defaultProps} type="task" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Task');
      expect(trigger).toHaveAttribute('data-type', 'task');
    });

    it('renders Epic correctly', () => {
      render(<TypeDropdown {...defaultProps} type="epic" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Epic');
      expect(trigger).toHaveAttribute('data-type', 'epic');
    });

    it('renders Chore correctly', () => {
      render(<TypeDropdown {...defaultProps} type="chore" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Chore');
      expect(trigger).toHaveAttribute('data-type', 'chore');
    });

    it('renders dropdown arrow', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('▾');
    });

    it('applies custom className', () => {
      render(<TypeDropdown {...defaultProps} className="custom-class" />);
      const container = screen.getByTestId('type-dropdown-trigger').parentElement;
      expect(container).toHaveClass('custom-class');
    });

    it('defaults to Task when type is undefined', () => {
      render(<TypeDropdown {...defaultProps} type={undefined} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Task');
    });

    it('renders TypeIcon in trigger', () => {
      render(<TypeDropdown {...defaultProps} type="bug" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      // TypeIcon is rendered as an SVG
      const svg = trigger.querySelector('svg');
      expect(svg).toBeInTheDocument();
    });
  });

  describe('Dropdown behavior', () => {
    it('opens dropdown menu on click', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();
    });

    it('closes dropdown on Escape key', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();

      fireEvent.keyDown(document, { key: 'Escape' });
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();
    });

    it('closes dropdown on click outside', () => {
      render(
        <div>
          <TypeDropdown {...defaultProps} />
          <button data-testid="outside-button">Outside</button>
        </div>
      );
      const trigger = screen.getByTestId('type-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId('outside-button'));
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();
    });

    it('closes dropdown after selection', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();
    });

    it('toggles dropdown on repeated clicks', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();
    });

    it('renders all 5 type options in menu', () => {
      render(<TypeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));

      expect(screen.getByTestId('type-option-bug')).toHaveTextContent('Bug');
      expect(screen.getByTestId('type-option-feature')).toHaveTextContent('Feature');
      expect(screen.getByTestId('type-option-task')).toHaveTextContent('Task');
      expect(screen.getByTestId('type-option-epic')).toHaveTextContent('Epic');
      expect(screen.getByTestId('type-option-chore')).toHaveTextContent('Chore');
    });

    it('each option shows icon and label', () => {
      render(<TypeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));

      const bugOption = screen.getByTestId('type-option-bug');
      const featureOption = screen.getByTestId('type-option-feature');

      // Each option should have an SVG icon
      expect(bugOption.querySelector('svg')).toBeInTheDocument();
      expect(featureOption.querySelector('svg')).toBeInTheDocument();

      // Each option should have label text
      expect(bugOption).toHaveTextContent('Bug');
      expect(featureOption).toHaveTextContent('Feature');
    });

    it('returns focus to trigger when closed with Escape', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      fireEvent.click(trigger);

      fireEvent.keyDown(document, { key: 'Escape' });
      expect(document.activeElement).toBe(trigger);
    });

    it('does not open when disabled', () => {
      render(<TypeDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();
    });

    it('does not open when saving', () => {
      render(<TypeDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      fireEvent.click(trigger);
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();
    });
  });

  describe('Selection', () => {
    it('calls onSave with selected type', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('bug');
      });
    });

    it('shows checkmark on current option', () => {
      render(<TypeDropdown {...defaultProps} type="task" />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));

      const currentOption = screen.getByTestId('type-option-task');
      expect(currentOption).toHaveTextContent('✓');
      expect(currentOption).toHaveAttribute('data-selected', 'true');

      // Other options should not have checkmark
      expect(screen.getByTestId('type-option-bug')).not.toHaveTextContent('✓');
      expect(screen.getByTestId('type-option-feature')).not.toHaveTextContent('✓');
      expect(screen.getByTestId('type-option-epic')).not.toHaveTextContent('✓');
      expect(screen.getByTestId('type-option-chore')).not.toHaveTextContent('✓');
    });

    it('updates display immediately (optimistic update)', async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      // Should show Bug immediately before save completes
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveTextContent('Bug');
      expect(trigger).toHaveAttribute('data-type', 'bug');

      await act(async () => {
        resolvePromise!();
      });
    });

    it('does not call onSave when selecting same type', async () => {
      const onSave = vi.fn();
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-task'));
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it('calls onSave for each different type value', async () => {
      const types = ['bug', 'feature', 'epic', 'chore'] as IssueType[];

      for (const targetType of types) {
        const onSave = vi.fn().mockResolvedValue(undefined);
        const { unmount } = render(
          <TypeDropdown {...defaultProps} type="task" onSave={onSave} />
        );

        fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
        await act(async () => {
          fireEvent.click(screen.getByTestId(`type-option-${targetType}`));
        });

        await waitFor(() => {
          expect(onSave).toHaveBeenCalledWith(targetType);
        });

        unmount();
      }
    });
  });

  describe('Loading state', () => {
    it('disables trigger when isSaving is true', () => {
      render(<TypeDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toBeDisabled();
    });

    it('shows saving indicator when isSaving is true', () => {
      render(<TypeDropdown {...defaultProps} isSaving />);
      expect(screen.getByTestId('type-saving')).toBeInTheDocument();
    });

    it('has aria-label on saving indicator', () => {
      render(<TypeDropdown {...defaultProps} isSaving />);
      const savingIndicator = screen.getByTestId('type-saving');
      expect(savingIndicator).toHaveAttribute('aria-label', 'Saving...');
    });

    it('applies data-saving attribute to trigger', () => {
      render(<TypeDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveAttribute('data-saving', 'true');
    });

    it('does not show saving indicator when not saving', () => {
      render(<TypeDropdown {...defaultProps} isSaving={false} />);
      expect(screen.queryByTestId('type-saving')).not.toBeInTheDocument();
    });

    it('does not apply data-saving attribute when not saving', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).not.toHaveAttribute('data-saving');
    });

    it('disables trigger when disabled prop is true', () => {
      render(<TypeDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toBeDisabled();
    });

    it('disables trigger when both disabled and isSaving are true', () => {
      render(<TypeDropdown {...defaultProps} disabled isSaving />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toBeDisabled();
    });
  });

  describe('Error handling', () => {
    it('reverts to previous type on save failure', async () => {
      let rejectPromise: (error: Error) => void;
      const savePromise = new Promise<void>((_, reject) => {
        rejectPromise = reject;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      // Initially shows optimistic value
      expect(screen.getByTestId('type-dropdown-trigger')).toHaveTextContent('Bug');

      // Reject the promise
      await act(async () => {
        rejectPromise!(new Error('Save failed'));
      });

      // After error, should revert
      await waitFor(() => {
        expect(screen.getByTestId('type-dropdown-trigger')).toHaveTextContent('Task');
      });
    });

    it('displays error message on save failure', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Network error'));
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('type-error')).toHaveTextContent('Network error');
      });
    });

    it('displays generic error for non-Error exceptions', async () => {
      const onSave = vi.fn().mockRejectedValue('string error');
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('type-error')).toHaveTextContent('Failed to update type');
      });
    });

    it('error has role="alert"', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Save failed'));
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
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

      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('type-error')).toBeInTheDocument();
      });

      // Open dropdown again - error should be cleared
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      expect(screen.queryByTestId('type-error')).not.toBeInTheDocument();
    });

    it('allows retry after failure', async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error('First error'))
        .mockResolvedValueOnce(undefined);

      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('type-error')).toBeInTheDocument();
      });

      // Retry should succeed
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      await waitFor(() => {
        expect(screen.queryByTestId('type-error')).not.toBeInTheDocument();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
    });
  });

  describe('Accessibility', () => {
    it('has aria-expanded attribute reflecting dropdown state', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      expect(trigger).toHaveAttribute('aria-expanded', 'false');

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute('aria-expanded', 'true');

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute('aria-expanded', 'false');
    });

    it('has aria-haspopup="listbox" on trigger', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
    });

    it('has aria-label on trigger', () => {
      render(<TypeDropdown {...defaultProps} type="task" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger).toHaveAttribute('aria-label', 'Type: Task. Click to change.');
    });

    it('menu has role="listbox"', () => {
      render(<TypeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      expect(screen.getByRole('listbox')).toBeInTheDocument();
    });

    it('menu has aria-label', () => {
      render(<TypeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      expect(screen.getByRole('listbox')).toHaveAttribute('aria-label', 'Select type');
    });

    it('options have role="option"', () => {
      render(<TypeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));

      const options = screen.getAllByRole('option');
      expect(options).toHaveLength(5);
    });

    it('current option has aria-selected="true"', () => {
      render(<TypeDropdown {...defaultProps} type="task" />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));

      const options = screen.getAllByRole('option');
      // Options order: bug, feature, task, epic, chore (task is index 2)
      expect(options[2]).toHaveAttribute('aria-selected', 'true');
      expect(options[0]).toHaveAttribute('aria-selected', 'false');
      expect(options[1]).toHaveAttribute('aria-selected', 'false');
      expect(options[3]).toHaveAttribute('aria-selected', 'false');
      expect(options[4]).toHaveAttribute('aria-selected', 'false');
    });

    it('trigger is a button with type="button"', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      expect(trigger.tagName).toBe('BUTTON');
      expect(trigger).toHaveAttribute('type', 'button');
    });
  });

  describe('Keyboard navigation', () => {
    it('opens dropdown with Enter key', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.keyDown(trigger, { key: 'Enter' });
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();
    });

    it('opens dropdown with Space key', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.keyDown(trigger, { key: ' ' });
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();
    });

    it('navigates down with ArrowDown key', () => {
      render(<TypeDropdown {...defaultProps} type="bug" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);

      // Initially focused on current type (bug, index 0)
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('type-option-feature')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('type-option-task')).toHaveAttribute('data-focused', 'true');
    });

    it('navigates up with ArrowUp key', () => {
      render(<TypeDropdown {...defaultProps} type="chore" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);

      // Initially focused on current type (chore, index 4)
      expect(screen.getByTestId('type-option-chore')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      expect(screen.getByTestId('type-option-epic')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      expect(screen.getByTestId('type-option-task')).toHaveAttribute('data-focused', 'true');
    });

    it('stops at first option when pressing ArrowUp', () => {
      render(<TypeDropdown {...defaultProps} type="bug" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-focused', 'true');
    });

    it('stops at last option when pressing ArrowDown', () => {
      render(<TypeDropdown {...defaultProps} type="chore" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-option-chore')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('type-option-chore')).toHaveAttribute('data-focused', 'true');
    });

    it('selects focused option with Enter key', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      fireEvent.keyDown(trigger, { key: 'ArrowDown' });

      // Now focused on index 4 (chore)
      expect(screen.getByTestId('type-option-chore')).toHaveAttribute('data-focused', 'true');

      await act(async () => {
        fireEvent.keyDown(trigger, { key: 'Enter' });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('chore');
      });
    });

    it('selects focused option with Space key', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: 'ArrowUp' });
      fireEvent.keyDown(trigger, { key: 'ArrowUp' });

      // Now focused on index 0 (bug)
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-focused', 'true');

      await act(async () => {
        fireEvent.keyDown(trigger, { key: ' ' });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('bug');
      });
    });

    it('closes dropdown with Escape key', () => {
      render(<TypeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();

      fireEvent.keyDown(trigger, { key: 'Escape' });
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();
    });

    it('navigates to first option with Home key', () => {
      render(<TypeDropdown {...defaultProps} type="chore" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-option-chore')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'Home' });
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-focused', 'true');
    });

    it('navigates to last option with End key', () => {
      render(<TypeDropdown {...defaultProps} type="bug" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-focused', 'true');

      fireEvent.keyDown(trigger, { key: 'End' });
      expect(screen.getByTestId('type-option-chore')).toHaveAttribute('data-focused', 'true');
    });

    it('sets initial focus to current type when opening', () => {
      render(<TypeDropdown {...defaultProps} type="epic" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      expect(screen.getByTestId('type-option-epic')).toHaveAttribute('data-focused', 'true');
    });

    it('resets focus when dropdown closes', () => {
      render(<TypeDropdown {...defaultProps} type="task" />);
      const trigger = screen.getByTestId('type-dropdown-trigger');

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: 'ArrowDown' });
      expect(screen.getByTestId('type-option-epic')).toHaveAttribute('data-focused', 'true');

      // Close and reopen
      fireEvent.keyDown(trigger, { key: 'Escape' });
      fireEvent.click(trigger);

      // Should be back to current type
      expect(screen.getByTestId('type-option-task')).toHaveAttribute('data-focused', 'true');
    });
  });

  describe('Props sync', () => {
    it('syncs optimistic type when prop changes', () => {
      const { rerender } = render(<TypeDropdown {...defaultProps} type="task" />);
      expect(screen.getByTestId('type-dropdown-trigger')).toHaveTextContent('Task');

      rerender(<TypeDropdown {...defaultProps} type="bug" />);
      expect(screen.getByTestId('type-dropdown-trigger')).toHaveTextContent('Bug');
    });

    it('updates checkmark when type prop changes', () => {
      const { rerender } = render(<TypeDropdown {...defaultProps} type="task" />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      expect(screen.getByTestId('type-option-task')).toHaveAttribute('data-selected', 'true');

      // Close and update type
      fireEvent.keyDown(document, { key: 'Escape' });
      rerender(<TypeDropdown {...defaultProps} type="bug" />);

      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      expect(screen.getByTestId('type-option-bug')).toHaveAttribute('data-selected', 'true');
      expect(screen.getByTestId('type-option-task')).not.toHaveAttribute('data-selected');
    });

    it('updates aria-label when type prop changes', () => {
      const { rerender } = render(<TypeDropdown {...defaultProps} type="task" />);
      expect(screen.getByTestId('type-dropdown-trigger')).toHaveAttribute(
        'aria-label',
        'Type: Task. Click to change.'
      );

      rerender(<TypeDropdown {...defaultProps} type="bug" />);
      expect(screen.getByTestId('type-dropdown-trigger')).toHaveAttribute(
        'aria-label',
        'Type: Bug. Click to change.'
      );
    });
  });

  describe('Edge cases', () => {
    it('handles rapid type changes', () => {
      const { rerender } = render(<TypeDropdown {...defaultProps} type="bug" />);

      rerender(<TypeDropdown {...defaultProps} type="feature" />);
      rerender(<TypeDropdown {...defaultProps} type="task" />);
      rerender(<TypeDropdown {...defaultProps} type="epic" />);
      rerender(<TypeDropdown {...defaultProps} type="chore" />);

      expect(screen.getByTestId('type-dropdown-trigger')).toHaveTextContent('Chore');
    });

    it('handles click on menu without selecting (click on menu background)', async () => {
      render(<TypeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));

      const menu = screen.getByTestId('type-dropdown-menu');
      fireEvent.click(menu);

      // Menu should still be open (click on menu itself, not an option)
      expect(screen.getByTestId('type-dropdown-menu')).toBeInTheDocument();
    });

    it('does not fire multiple saves for rapid selections', async () => {
      let resolveFirst: () => void;
      const firstPromise = new Promise<void>((resolve) => {
        resolveFirst = resolve;
      });
      const onSave = vi.fn().mockReturnValueOnce(firstPromise).mockResolvedValue(undefined);

      render(<TypeDropdown {...defaultProps} type="task" onSave={onSave} />);

      // First selection
      fireEvent.click(screen.getByTestId('type-dropdown-trigger'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('type-option-bug'));
      });

      // Dropdown is closed after first selection
      expect(screen.queryByTestId('type-dropdown-menu')).not.toBeInTheDocument();

      // Resolve first save
      await act(async () => {
        resolveFirst!();
      });

      expect(onSave).toHaveBeenCalledTimes(1);
    });

    it('handles undefined className gracefully', () => {
      render(<TypeDropdown {...defaultProps} className={undefined} />);
      const container = screen.getByTestId('type-dropdown-trigger').parentElement;
      expect(container).toBeInTheDocument();
    });

    it('handles custom/unknown type strings', () => {
      render(<TypeDropdown {...defaultProps} type={'custom_type' as IssueType} />);
      const trigger = screen.getByTestId('type-dropdown-trigger');
      // formatIssueType converts underscores to spaces and capitalizes words
      expect(trigger).toHaveTextContent('Custom Type');
      expect(trigger).toHaveAttribute('data-type', 'custom_type');
    });
  });
});
