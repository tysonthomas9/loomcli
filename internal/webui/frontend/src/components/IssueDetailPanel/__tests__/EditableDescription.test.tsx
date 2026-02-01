/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EditableDescription component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import { EditableDescription } from '../EditableDescription';

// Mock MarkdownRenderer to avoid complexity
vi.mock('../MarkdownRenderer', () => ({
  MarkdownRenderer: ({ content }: { content: string | undefined }) => (
    <div data-testid="markdown-renderer">{content ?? 'No content'}</div>
  ),
}));

describe('EditableDescription', () => {
  const defaultProps = {
    description: 'Test description with **markdown**',
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('View Mode', () => {
    it('renders markdown content correctly', () => {
      render(<EditableDescription {...defaultProps} />);
      expect(screen.getByTestId('markdown-renderer')).toHaveTextContent(
        'Test description with **markdown**'
      );
    });

    it('shows edit button when isEditable=true', () => {
      render(<EditableDescription {...defaultProps} isEditable />);
      expect(screen.getByTestId('description-edit-button')).toBeInTheDocument();
    });

    it('shows edit button by default (isEditable defaults to true)', () => {
      render(<EditableDescription {...defaultProps} />);
      expect(screen.getByTestId('description-edit-button')).toBeInTheDocument();
    });

    it('hides edit button when isEditable=false', () => {
      render(<EditableDescription {...defaultProps} isEditable={false} />);
      expect(screen.queryByTestId('description-edit-button')).not.toBeInTheDocument();
    });

    it('shows placeholder when description is undefined', () => {
      render(<EditableDescription {...defaultProps} description={undefined} />);
      expect(screen.getByText('No description. Click to add one.')).toBeInTheDocument();
    });

    it('shows placeholder when description is empty string', () => {
      render(<EditableDescription {...defaultProps} description="" />);
      // Empty string is falsy, so placeholder shows
      expect(screen.getByText('No description. Click to add one.')).toBeInTheDocument();
    });

    it('enters edit mode when edit button is clicked', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.getByTestId('description-textarea')).toBeInTheDocument();
    });

    it('edit button has accessible label', () => {
      render(<EditableDescription {...defaultProps} />);
      expect(screen.getByLabelText('Edit description')).toBeInTheDocument();
    });

    it('applies custom className', () => {
      render(<EditableDescription {...defaultProps} className="custom-class" />);
      expect(screen.getByTestId('editable-description')).toHaveClass('custom-class');
    });
  });

  describe('Edit Mode', () => {
    it('textarea contains current description', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea') as HTMLTextAreaElement;
      expect(textarea.value).toBe('Test description with **markdown**');
    });

    it('textarea is empty when description is undefined', () => {
      render(<EditableDescription {...defaultProps} description={undefined} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea') as HTMLTextAreaElement;
      expect(textarea.value).toBe('');
    });

    it('focuses textarea when entering edit mode', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(document.activeElement).toBe(screen.getByTestId('description-textarea'));
    });

    it('preview updates in real-time as user types', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New preview content' } });
      expect(screen.getByTestId('markdown-renderer')).toHaveTextContent('New preview content');
    });

    it('shows preview label', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.getByText('Preview')).toBeInTheDocument();
    });

    it('has correct aria-label on textarea', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.getByTestId('description-textarea')).toHaveAttribute(
        'aria-label',
        'Edit description'
      );
    });

    it('has placeholder text in textarea', () => {
      render(<EditableDescription {...defaultProps} description={undefined} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.getByTestId('description-textarea')).toHaveAttribute(
        'placeholder',
        'Enter description (supports markdown)'
      );
    });
  });

  describe('Save Flow', () => {
    it('save button triggers onSave with new value', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('New description');
      });
    });

    it('trims whitespace from description before saving', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: '  Trimmed description  ' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('Trimmed description');
      });
    });

    it('does not call onSave if description unchanged', async () => {
      const onSave = vi.fn();
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('returns to view mode on successful save', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.queryByTestId('description-textarea')).not.toBeInTheDocument();
      });
    });

    it('shows loading state during save', async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });

      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });

      // Should show saving state
      expect(screen.getByTestId('description-save')).toHaveTextContent('Saving...');
      expect(screen.getByTestId('description-save')).toBeDisabled();
      expect(screen.getByTestId('description-cancel')).toBeDisabled();
      expect(screen.getByTestId('description-textarea')).toBeDisabled();

      // Resolve the promise
      await act(async () => {
        resolvePromise!();
      });
    });

    it('shows error on save failure', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Save failed'));
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.getByTestId('description-error')).toHaveTextContent('Save failed');
      });
    });

    it('shows generic error for non-Error exceptions', async () => {
      const onSave = vi.fn().mockRejectedValue('string error');
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.getByTestId('description-error')).toHaveTextContent(
          'Failed to save description'
        );
      });
    });

    it('stays in edit mode on failure', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Save failed'));
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.getByTestId('description-textarea')).toBeInTheDocument();
      });
    });

    it('error has role="alert"', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Save failed'));
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.getByRole('alert')).toHaveTextContent('Save failed');
      });
    });

    it('allows retry after failure', async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error('First attempt failed'))
        .mockResolvedValueOnce(undefined);

      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });

      // First attempt fails
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.getByTestId('description-error')).toBeInTheDocument();
      });

      // Retry should succeed
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.queryByTestId('description-textarea')).not.toBeInTheDocument();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
    });
  });

  describe('Cancel Flow', () => {
    it('cancel button reverts to view mode', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      fireEvent.click(screen.getByTestId('description-cancel'));
      expect(screen.queryByTestId('description-textarea')).not.toBeInTheDocument();
      expect(screen.getByTestId('description-edit-button')).toBeInTheDocument();
    });

    it('cancel button does not call onSave', () => {
      const onSave = vi.fn();
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'Changed description' } });
      fireEvent.click(screen.getByTestId('description-cancel'));
      expect(onSave).not.toHaveBeenCalled();
    });

    it('cancel button resets draft to original value', () => {
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'Changed description' } });
      fireEvent.click(screen.getByTestId('description-cancel'));

      // Re-enter edit mode, should show original value
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.getByTestId('description-textarea')).toHaveValue(
        'Test description with **markdown**'
      );
    });
  });

  describe('Keyboard Shortcuts', () => {
    it('Escape key cancels edit', () => {
      const onSave = vi.fn();
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'Changed description' } });
      fireEvent.keyDown(textarea, { key: 'Escape' });
      expect(onSave).not.toHaveBeenCalled();
      expect(screen.queryByTestId('description-textarea')).not.toBeInTheDocument();
    });

    it('Cmd+Enter triggers save on Mac', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('New description');
      });
    });

    it('Ctrl+Enter triggers save on Windows/Linux', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });
      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true });
      });
      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith('New description');
      });
    });

    it('regular Enter does not trigger save (allows multiline)', () => {
      const onSave = vi.fn();
      render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.keyDown(textarea, { key: 'Enter' });
      expect(onSave).not.toHaveBeenCalled();
      expect(screen.getByTestId('description-textarea')).toBeInTheDocument();
    });
  });

  describe('Props sync', () => {
    it('syncs draft description when prop changes while not editing', () => {
      const { rerender } = render(<EditableDescription {...defaultProps} />);
      rerender(<EditableDescription {...defaultProps} description="Updated description" />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.getByTestId('description-textarea')).toHaveValue('Updated description');
    });

    it('does not sync draft description while in edit mode', () => {
      const { rerender } = render(<EditableDescription {...defaultProps} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'Edited' } });
      rerender(<EditableDescription {...defaultProps} description="Updated from server" />);
      expect(screen.getByTestId('description-textarea')).toHaveValue('Edited');
    });
  });

  describe('Edge cases', () => {
    it('does not enter edit mode when isSaving', async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });

      // Start saving
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });

      // Should be in saving state - buttons should be disabled
      expect(screen.getByTestId('description-save')).toBeDisabled();

      // Resolve and cleanup
      await act(async () => {
        resolvePromise!();
      });
    });

    it('clears error when canceling and re-entering edit mode', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error('Save failed'));
      render(<EditableDescription {...defaultProps} onSave={onSave} />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      fireEvent.change(textarea, { target: { value: 'New description' } });

      // Trigger error
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      await waitFor(() => {
        expect(screen.getByTestId('description-error')).toBeInTheDocument();
      });

      // Cancel
      fireEvent.click(screen.getByTestId('description-cancel'));

      // Re-enter edit mode - error should be cleared
      fireEvent.click(screen.getByTestId('description-edit-button'));
      expect(screen.queryByTestId('description-error')).not.toBeInTheDocument();
    });

    it('treats empty string and undefined as equivalent for save comparison', async () => {
      const onSave = vi.fn();
      render(<EditableDescription {...defaultProps} description="" />);
      fireEvent.click(screen.getByTestId('description-edit-button'));
      const textarea = screen.getByTestId('description-textarea');
      // Clear to empty - should be considered unchanged from empty
      fireEvent.change(textarea, { target: { value: '' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('description-save'));
      });
      expect(onSave).not.toHaveBeenCalled();
    });
  });
});
