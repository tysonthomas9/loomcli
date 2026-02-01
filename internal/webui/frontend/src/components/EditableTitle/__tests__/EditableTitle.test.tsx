/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import { EditableTitle } from '../EditableTitle';

describe('EditableTitle', () => {
  const defaultProps = {
    title: 'Test Issue Title',
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Display Mode', () => {
    it('renders title text', () => {
      render(<EditableTitle {...defaultProps} />);
      expect(screen.getByText('Test Issue Title')).toBeInTheDocument();
    });

    it('shows edit hint SVG', () => {
      render(<EditableTitle {...defaultProps} />);
      const container = screen.getByTestId('editable-title-display');
      const hint = container.querySelector('svg');
      expect(hint).toBeInTheDocument();
    });

    it('enters edit mode on click', () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      expect(screen.getByTestId('editable-title-input')).toBeInTheDocument();
    });

    it('enters edit mode on Enter key', () => {
      render(<EditableTitle {...defaultProps} />);
      const display = screen.getByTestId('editable-title-display');
      fireEvent.keyDown(display, { key: 'Enter' });
      expect(screen.getByTestId('editable-title-input')).toBeInTheDocument();
    });

    it('enters edit mode on Space key', () => {
      render(<EditableTitle {...defaultProps} />);
      const display = screen.getByTestId('editable-title-display');
      fireEvent.keyDown(display, { key: ' ' });
      expect(screen.getByTestId('editable-title-input')).toBeInTheDocument();
    });

    it('does not enter edit mode when disabled', () => {
      render(<EditableTitle {...defaultProps} disabled />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      expect(screen.queryByTestId('editable-title-input')).not.toBeInTheDocument();
    });

    it('does not enter edit mode when isSaving is true', () => {
      render(<EditableTitle {...defaultProps} isSaving />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      expect(screen.queryByTestId('editable-title-input')).not.toBeInTheDocument();
    });

    it('does not show edit hint when disabled', () => {
      render(<EditableTitle {...defaultProps} disabled />);
      const container = screen.getByTestId('editable-title-display');
      const hint = container.querySelector('svg');
      expect(hint).not.toBeInTheDocument();
    });
  });

  describe('Edit Mode', () => {
    it('shows input with current title', () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input') as HTMLInputElement;
      expect(input.value).toBe('Test Issue Title');
    });

    it('focuses input when entering edit mode', () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      expect(document.activeElement).toBe(screen.getByTestId('editable-title-input'));
    });

    it('saves on Enter key', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableTitle {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: 'New Title' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('New Title');
      });
    });

    it('saves on blur', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableTitle {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: 'New Title' } });
      await act(async () => {
        fireEvent.blur(input);
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('New Title');
      });
    });

    it('cancels on Escape key', () => {
      const onSave = vi.fn();
      render(<EditableTitle {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: 'New Title' } });
      fireEvent.keyDown(input, { key: 'Escape' });
      expect(onSave).not.toHaveBeenCalled();
      expect(screen.getByTestId('editable-title-display')).toBeInTheDocument();
    });

    it('shows error for empty title', async () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: '' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      expect(screen.getByTestId('title-error')).toHaveTextContent('Title cannot be empty');
      expect(defaultProps.onSave).not.toHaveBeenCalled();
    });

    it('shows error for whitespace-only title', async () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: '   ' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      expect(screen.getByTestId('title-error')).toHaveTextContent('Title cannot be empty');
      expect(defaultProps.onSave).not.toHaveBeenCalled();
    });

    it('trims whitespace from title', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableTitle {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: '  Trimmed Title  ' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('Trimmed Title');
      });
    });

    it('does not save if title unchanged', async () => {
      const onSave = vi.fn();
      render(<EditableTitle {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('shows error message on API error and stays in edit mode', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('API Error'));
      render(<EditableTitle title="Original Title" onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: 'New Title' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      await waitFor(() => {
        expect(screen.getByTestId('title-error')).toHaveTextContent('API Error');
      });
      // Should stay in edit mode with input still visible
      expect(screen.getByTestId('editable-title-input')).toBeInTheDocument();
    });

    it('shows generic error message for non-Error exceptions', async () => {
      const onSave = vi.fn().mockRejectedValue('string error');
      render(<EditableTitle title="Original Title" onSave={onSave} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: 'New Title' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      await waitFor(() => {
        expect(screen.getByTestId('title-error')).toHaveTextContent('Failed to save title');
      });
    });
  });

  describe('Saving State', () => {
    it('shows saving indicator when isSaving is true during edit mode', () => {
      const { rerender } = render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      rerender(<EditableTitle {...defaultProps} isSaving />);
      expect(screen.getByTestId('title-saving')).toHaveTextContent('Saving...');
    });

    it('disables input when isSaving is true', () => {
      const { rerender } = render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      rerender(<EditableTitle {...defaultProps} isSaving />);
      expect(screen.getByTestId('editable-title-input')).toBeDisabled();
    });
  });

  describe('Accessibility', () => {
    it('has correct aria-label in display mode', () => {
      render(<EditableTitle {...defaultProps} />);
      expect(screen.getByTestId('editable-title-display')).toHaveAttribute(
        'aria-label',
        'Test Issue Title. Click to edit.'
      );
    });

    it('has correct aria-label on input', () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      expect(screen.getByTestId('editable-title-input')).toHaveAttribute(
        'aria-label',
        'Edit issue title'
      );
    });

    it('error has role="alert"', async () => {
      render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: '' } });
      await act(async () => {
        fireEvent.keyDown(input, { key: 'Enter' });
      });
      expect(screen.getByRole('alert')).toHaveTextContent('Title cannot be empty');
    });

    it('has role="button" in display mode', () => {
      render(<EditableTitle {...defaultProps} />);
      expect(screen.getByTestId('editable-title-display')).toHaveAttribute('role', 'button');
    });

    it('has tabIndex=0 when not disabled', () => {
      render(<EditableTitle {...defaultProps} />);
      expect(screen.getByTestId('editable-title-display')).toHaveAttribute('tabIndex', '0');
    });

    it('has tabIndex=-1 when disabled', () => {
      render(<EditableTitle {...defaultProps} disabled />);
      expect(screen.getByTestId('editable-title-display')).toHaveAttribute('tabIndex', '-1');
    });
  });

  describe('Props sync', () => {
    it('syncs draft title when title prop changes', () => {
      const { rerender } = render(<EditableTitle {...defaultProps} />);
      // Enter and exit edit mode
      fireEvent.click(screen.getByTestId('editable-title-display'));
      fireEvent.keyDown(screen.getByTestId('editable-title-input'), { key: 'Escape' });
      // Rerender with new title
      rerender(<EditableTitle {...defaultProps} title="Updated Title" />);
      expect(screen.getByText('Updated Title')).toBeInTheDocument();
    });

    it('does not sync draft title while in edit mode', () => {
      const { rerender } = render(<EditableTitle {...defaultProps} />);
      fireEvent.click(screen.getByTestId('editable-title-display'));
      const input = screen.getByTestId('editable-title-input');
      fireEvent.change(input, { target: { value: 'Edited' } });
      // Rerender with new title - should not affect current draft
      rerender(<EditableTitle {...defaultProps} title="Updated Title" />);
      expect(screen.getByTestId('editable-title-input')).toHaveValue('Edited');
    });
  });

  describe('className prop', () => {
    it('applies custom className', () => {
      render(<EditableTitle {...defaultProps} className="custom-class" />);
      expect(screen.getByTestId('editable-title-display')).toHaveClass('custom-class');
    });
  });
});
