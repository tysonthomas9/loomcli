/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BulkActionToolbar component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import '@testing-library/jest-dom';

import { BulkActionToolbar, type BulkAction } from '../BulkActionToolbar';

describe('BulkActionToolbar', () => {
  const defaultProps = {
    selectedIds: new Set(['issue-1', 'issue-2']),
    onClearSelection: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('rendering', () => {
    it('renders when items are selected', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('bulk-action-toolbar')).toBeInTheDocument();
    });

    it('does not render when no items are selected', () => {
      render(
        <BulkActionToolbar
          selectedIds={new Set()}
          onClearSelection={defaultProps.onClearSelection}
        />
      );
      expect(screen.queryByTestId('bulk-action-toolbar')).not.toBeInTheDocument();
    });

    it('shows correct selection count', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('selection-count')).toHaveTextContent('2 selected');
    });

    it('shows singular text for one item', () => {
      render(
        <BulkActionToolbar
          selectedIds={new Set(['issue-1'])}
          onClearSelection={defaultProps.onClearSelection}
        />
      );
      expect(screen.getByRole('toolbar')).toHaveAttribute(
        'aria-label',
        'Bulk actions for 1 selected issue'
      );
    });

    it('shows plural text for multiple items', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByRole('toolbar')).toHaveAttribute(
        'aria-label',
        'Bulk actions for 2 selected issues'
      );
    });

    it('applies custom className', () => {
      render(<BulkActionToolbar {...defaultProps} className="custom-class" />);
      expect(screen.getByTestId('bulk-action-toolbar')).toHaveClass('custom-class');
    });

    it('renders without custom className', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      const toolbar = screen.getByTestId('bulk-action-toolbar');
      expect(toolbar).toBeInTheDocument();
    });
  });

  describe('deselect all', () => {
    it('renders deselect all button', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('bulk-action-clear')).toBeInTheDocument();
    });

    it('calls onClearSelection when clicked', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      fireEvent.click(screen.getByTestId('bulk-action-clear'));
      expect(defaultProps.onClearSelection).toHaveBeenCalledTimes(1);
    });

    it('deselect button has correct text', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('bulk-action-clear')).toHaveTextContent('Deselect all');
    });

    it('deselect button is type="button"', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('bulk-action-clear')).toHaveAttribute('type', 'button');
    });
  });

  describe('actions', () => {
    it('renders action buttons', () => {
      const actions: BulkAction[] = [
        { id: 'close', label: 'Close', onClick: vi.fn() },
        { id: 'priority', label: 'Priority', onClick: vi.fn() },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      expect(screen.getByTestId('bulk-action-close')).toBeInTheDocument();
      expect(screen.getByTestId('bulk-action-priority')).toBeInTheDocument();
    });

    it('calls action onClick with selectedIds', () => {
      const onClick = vi.fn();
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      fireEvent.click(screen.getByTestId('bulk-action-close'));
      expect(onClick).toHaveBeenCalledWith(defaultProps.selectedIds);
    });

    it('shows loading state', () => {
      const actions: BulkAction[] = [
        { id: 'close', label: 'Close', onClick: vi.fn(), loading: true },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      expect(screen.getByTestId('bulk-action-close')).toHaveTextContent('Loading...');
    });

    it('disables button when disabled', () => {
      const onClick = vi.fn();
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick, disabled: true }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-close');
      expect(button).toBeDisabled();
      fireEvent.click(button);
      expect(onClick).not.toHaveBeenCalled();
    });

    it('disables button when loading', () => {
      const onClick = vi.fn();
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick, loading: true }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-close');
      expect(button).toBeDisabled();
    });

    it('does not call onClick when disabled', () => {
      const onClick = vi.fn();
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick, disabled: true }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      fireEvent.click(screen.getByTestId('bulk-action-close'));
      expect(onClick).not.toHaveBeenCalled();
    });

    it('does not call onClick when loading', () => {
      const onClick = vi.fn();
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick, loading: true }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      fireEvent.click(screen.getByTestId('bulk-action-close'));
      expect(onClick).not.toHaveBeenCalled();
    });

    it('renders icon when provided', () => {
      const actions: BulkAction[] = [
        {
          id: 'close',
          label: 'Close',
          onClick: vi.fn(),
          icon: <span data-testid="close-icon">X</span>,
        },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      expect(screen.getByTestId('close-icon')).toBeInTheDocument();
    });

    it('does not render icon when not provided', () => {
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick: vi.fn() }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-close');
      // The icon wrapper should not exist
      expect(button.querySelector('[class*="icon"]')).not.toBeInTheDocument();
    });

    it('renders with empty actions array', () => {
      render(<BulkActionToolbar {...defaultProps} actions={[]} />);
      expect(screen.getByTestId('bulk-action-toolbar')).toBeInTheDocument();
      // Only the deselect button should be present
      expect(screen.getByTestId('bulk-action-clear')).toBeInTheDocument();
    });

    it('action buttons are type="button"', () => {
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick: vi.fn() }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      expect(screen.getByTestId('bulk-action-close')).toHaveAttribute('type', 'button');
    });
  });

  describe('variants', () => {
    it('applies primary variant class', () => {
      const actions: BulkAction[] = [
        { id: 'save', label: 'Save', onClick: vi.fn(), variant: 'primary' },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-save');
      expect(button.className).toMatch(/primary/);
    });

    it('applies secondary variant class', () => {
      const actions: BulkAction[] = [
        { id: 'cancel', label: 'Cancel', onClick: vi.fn(), variant: 'secondary' },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-cancel');
      expect(button.className).toMatch(/secondary/);
    });

    it('applies danger variant class', () => {
      const actions: BulkAction[] = [
        { id: 'delete', label: 'Delete', onClick: vi.fn(), variant: 'danger' },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-delete');
      expect(button.className).toMatch(/danger/);
    });

    it('defaults to secondary variant when not specified', () => {
      const actions: BulkAction[] = [{ id: 'action', label: 'Action', onClick: vi.fn() }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      const button = screen.getByTestId('bulk-action-action');
      expect(button.className).toMatch(/secondary/);
    });
  });

  describe('accessibility', () => {
    it('has toolbar role', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByRole('toolbar')).toBeInTheDocument();
    });

    it('has descriptive aria-label', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByRole('toolbar')).toHaveAttribute(
        'aria-label',
        'Bulk actions for 2 selected issues'
      );
    });

    it('action buttons have aria-label', () => {
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick: vi.fn() }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      expect(screen.getByTestId('bulk-action-close')).toHaveAttribute('aria-label', 'Close');
    });

    it('deselect button has aria-label', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('bulk-action-clear')).toHaveAttribute(
        'aria-label',
        'Clear selection'
      );
    });

    it('action buttons can be found by role', () => {
      const actions: BulkAction[] = [{ id: 'close', label: 'Close', onClick: vi.fn() }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);
      // Should find 2 buttons: action + deselect
      const buttons = screen.getAllByRole('button');
      expect(buttons.length).toBe(2);
    });

    it('deselect button can be activated with keyboard', () => {
      render(<BulkActionToolbar {...defaultProps} />);
      const clearButton = screen.getByTestId('bulk-action-clear');

      clearButton.focus();
      expect(document.activeElement).toBe(clearButton);

      fireEvent.click(clearButton);
      expect(defaultProps.onClearSelection).toHaveBeenCalled();
    });
  });

  describe('edge cases', () => {
    it('handles large selection count', () => {
      const largeSet = new Set(Array.from({ length: 1000 }, (_, i) => `issue-${i}`));
      render(
        <BulkActionToolbar
          selectedIds={largeSet}
          onClearSelection={defaultProps.onClearSelection}
        />
      );
      expect(screen.getByTestId('selection-count')).toHaveTextContent('1000 selected');
    });

    it('handles selectedIds changing', () => {
      const { rerender } = render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('selection-count')).toHaveTextContent('2 selected');

      rerender(
        <BulkActionToolbar
          selectedIds={new Set(['issue-1', 'issue-2', 'issue-3'])}
          onClearSelection={defaultProps.onClearSelection}
        />
      );
      expect(screen.getByTestId('selection-count')).toHaveTextContent('3 selected');
    });

    it('handles selectedIds becoming empty', () => {
      const { rerender } = render(<BulkActionToolbar {...defaultProps} />);
      expect(screen.getByTestId('bulk-action-toolbar')).toBeInTheDocument();

      rerender(
        <BulkActionToolbar
          selectedIds={new Set()}
          onClearSelection={defaultProps.onClearSelection}
        />
      );
      expect(screen.queryByTestId('bulk-action-toolbar')).not.toBeInTheDocument();
    });

    it('handles async onClick handlers', async () => {
      const asyncHandler = vi.fn().mockResolvedValue(undefined);
      const actions: BulkAction[] = [{ id: 'async', label: 'Async Action', onClick: asyncHandler }];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);

      fireEvent.click(screen.getByTestId('bulk-action-async'));

      expect(asyncHandler).toHaveBeenCalledWith(defaultProps.selectedIds);
    });

    it('handles multiple actions', () => {
      const actions: BulkAction[] = [
        { id: 'action1', label: 'Action 1', onClick: vi.fn() },
        { id: 'action2', label: 'Action 2', onClick: vi.fn() },
        { id: 'action3', label: 'Action 3', onClick: vi.fn() },
      ];
      render(<BulkActionToolbar {...defaultProps} actions={actions} />);

      expect(screen.getByTestId('bulk-action-action1')).toBeInTheDocument();
      expect(screen.getByTestId('bulk-action-action2')).toBeInTheDocument();
      expect(screen.getByTestId('bulk-action-action3')).toBeInTheDocument();
    });

    it('handles undefined className gracefully', () => {
      render(
        <BulkActionToolbar
          selectedIds={defaultProps.selectedIds}
          onClearSelection={defaultProps.onClearSelection}
          className={undefined}
        />
      );
      expect(screen.getByTestId('bulk-action-toolbar')).toBeInTheDocument();
    });

    it('handles actions prop being undefined', () => {
      render(
        <BulkActionToolbar
          selectedIds={defaultProps.selectedIds}
          onClearSelection={defaultProps.onClearSelection}
        />
      );
      expect(screen.getByTestId('bulk-action-toolbar')).toBeInTheDocument();
      expect(screen.getByTestId('bulk-action-clear')).toBeInTheDocument();
    });
  });
});
