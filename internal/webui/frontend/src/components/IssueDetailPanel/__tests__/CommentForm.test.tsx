/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CommentForm component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

import { CommentForm } from '../CommentForm';
import type { Comment } from '@/types';

// Mock the API module
vi.mock('@/api', () => ({
  addComment: vi.fn(),
}));

// Import the mocked function for use in tests
import { addComment } from '@/api';
const mockAddComment = vi.mocked(addComment);

/**
 * Create a test comment with default values.
 */
function createTestComment(overrides: Partial<Comment> = {}): Comment {
  return {
    id: 1,
    issue_id: 'test-issue',
    author: 'Test Author',
    text: 'Test comment text',
    created_at: '2026-01-20T10:00:00Z',
    ...overrides,
  };
}

describe('CommentForm', () => {
  const defaultProps = {
    issueId: 'test-issue-123',
    onCommentAdded: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockAddComment.mockResolvedValue(createTestComment());
  });

  describe('rendering', () => {
    it('renders form with textarea and submit button', () => {
      render(<CommentForm {...defaultProps} />);
      expect(screen.getByTestId('comment-form')).toBeInTheDocument();
      expect(screen.getByTestId('comment-textarea')).toBeInTheDocument();
      expect(screen.getByTestId('comment-submit')).toBeInTheDocument();
    });

    it('textarea has correct placeholder', () => {
      render(<CommentForm {...defaultProps} />);
      expect(screen.getByTestId('comment-textarea')).toHaveAttribute(
        'placeholder',
        'Add a comment...'
      );
    });

    it('submit button shows "Add Comment"', () => {
      render(<CommentForm {...defaultProps} />);
      expect(screen.getByTestId('comment-submit')).toHaveTextContent('Add Comment');
    });

    it('applies custom className', () => {
      render(<CommentForm {...defaultProps} className="custom-class" />);
      expect(screen.getByTestId('comment-form')).toHaveClass('custom-class');
    });

    it('has accessible label on textarea', () => {
      render(<CommentForm {...defaultProps} />);
      expect(screen.getByLabelText('Add a comment')).toBeInTheDocument();
    });

    it('shows keyboard shortcut hint', () => {
      render(<CommentForm {...defaultProps} />);
      expect(screen.getByText('Cmd+Enter to submit')).toBeInTheDocument();
    });
  });

  describe('interaction', () => {
    it('typing updates textarea value', () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'New comment text' } });
      expect(textarea).toHaveValue('New comment text');
    });

    it('submit button enabled when text present', () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'Some text' } });
      expect(screen.getByTestId('comment-submit')).not.toBeDisabled();
    });

    it('submit button disabled when text empty', () => {
      render(<CommentForm {...defaultProps} />);
      expect(screen.getByTestId('comment-submit')).toBeDisabled();
    });

    it('submit button disabled when text is whitespace only', () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: '   ' } });
      expect(screen.getByTestId('comment-submit')).toBeDisabled();
    });

    it('clicking submit calls addComment API', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });
      await waitFor(() => {
        expect(mockAddComment).toHaveBeenCalledWith('test-issue-123', 'My comment');
      });
    });

    it('trims whitespace before sending to API', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: '  trimmed comment  ' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });
      await waitFor(() => {
        expect(mockAddComment).toHaveBeenCalledWith('test-issue-123', 'trimmed comment');
      });
    });

    it('Cmd+Enter triggers submit', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'Comment via keyboard' } });
      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });
      });
      await waitFor(() => {
        expect(mockAddComment).toHaveBeenCalledWith('test-issue-123', 'Comment via keyboard');
      });
    });

    it('Ctrl+Enter triggers submit', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'Comment via ctrl' } });
      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true });
      });
      await waitFor(() => {
        expect(mockAddComment).toHaveBeenCalledWith('test-issue-123', 'Comment via ctrl');
      });
    });

    it('regular Enter does not trigger submit', () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'Some comment' } });
      fireEvent.keyDown(textarea, { key: 'Enter' });
      expect(mockAddComment).not.toHaveBeenCalled();
    });
  });

  describe('state during submission', () => {
    it('shows "Adding..." during submission', async () => {
      let resolvePromise: (value: Comment) => void;
      const submitPromise = new Promise<Comment>((resolve) => {
        resolvePromise = resolve;
      });
      mockAddComment.mockReturnValue(submitPromise);

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });

      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      expect(screen.getByTestId('comment-submit')).toHaveTextContent('Adding...');

      // Resolve promise to cleanup
      await act(async () => {
        resolvePromise!(createTestComment());
      });
    });

    it('textarea disabled during submission', async () => {
      let resolvePromise: (value: Comment) => void;
      const submitPromise = new Promise<Comment>((resolve) => {
        resolvePromise = resolve;
      });
      mockAddComment.mockReturnValue(submitPromise);

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });

      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      expect(screen.getByTestId('comment-textarea')).toBeDisabled();

      // Resolve promise to cleanup
      await act(async () => {
        resolvePromise!(createTestComment());
      });
    });

    it('submit button disabled during submission', async () => {
      let resolvePromise: (value: Comment) => void;
      const submitPromise = new Promise<Comment>((resolve) => {
        resolvePromise = resolve;
      });
      mockAddComment.mockReturnValue(submitPromise);

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });

      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      expect(screen.getByTestId('comment-submit')).toBeDisabled();

      // Resolve promise to cleanup
      await act(async () => {
        resolvePromise!(createTestComment());
      });
    });
  });

  describe('success behavior', () => {
    it('clears textarea on success', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(textarea).toHaveValue('');
      });
    });

    it('calls onCommentAdded callback on success', async () => {
      const newComment = createTestComment({ id: 42, text: 'New comment' });
      mockAddComment.mockResolvedValue(newComment);
      const onCommentAdded = vi.fn();

      render(<CommentForm {...defaultProps} onCommentAdded={onCommentAdded} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(onCommentAdded).toHaveBeenCalledWith(newComment);
      });
    });

    it('keeps focus in textarea after successful submission', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(textarea).toHaveValue('');
      });
      expect(document.activeElement).toBe(textarea);
    });
  });

  describe('error handling', () => {
    it('shows error message on API failure', async () => {
      mockAddComment.mockRejectedValue(new Error('Network error'));

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('comment-error')).toHaveTextContent('Network error');
      });
    });

    it('shows generic error for non-Error exceptions', async () => {
      mockAddComment.mockRejectedValue('string error');

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('comment-error')).toHaveTextContent('Failed to add comment');
      });
    });

    it('error has role="alert"', async () => {
      mockAddComment.mockRejectedValue(new Error('API error'));

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(screen.getByRole('alert')).toHaveTextContent('API error');
      });
    });

    it('error clears when user types again', async () => {
      mockAddComment.mockRejectedValueOnce(new Error('API error'));

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('comment-error')).toBeInTheDocument();
      });

      // Type again to clear error
      fireEvent.change(textarea, { target: { value: 'My commentx' } });
      expect(screen.queryByTestId('comment-error')).not.toBeInTheDocument();
    });

    it('preserves textarea content on error', async () => {
      mockAddComment.mockRejectedValue(new Error('API error'));

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('comment-error')).toBeInTheDocument();
      });
      expect(textarea).toHaveValue('My comment');
    });

    it('allows retry after failure', async () => {
      mockAddComment
        .mockRejectedValueOnce(new Error('First attempt failed'))
        .mockResolvedValueOnce(createTestComment());

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });

      // First attempt fails
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });
      await waitFor(() => {
        expect(screen.getByTestId('comment-error')).toBeInTheDocument();
      });

      // Retry should succeed
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });
      await waitFor(() => {
        expect(textarea).toHaveValue('');
      });
      expect(mockAddComment).toHaveBeenCalledTimes(2);
    });
  });

  describe('edge cases', () => {
    it('does not submit when already submitting', async () => {
      let resolvePromise: (value: Comment) => void;
      const submitPromise = new Promise<Comment>((resolve) => {
        resolvePromise = resolve;
      });
      mockAddComment.mockReturnValue(submitPromise);

      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'My comment' } });

      // First submit
      await act(async () => {
        fireEvent.click(screen.getByTestId('comment-submit'));
      });

      // Try to submit again via keyboard while still submitting
      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });
      });

      // Should only have been called once
      expect(mockAddComment).toHaveBeenCalledTimes(1);

      // Resolve promise to cleanup
      await act(async () => {
        resolvePromise!(createTestComment());
      });
    });

    it('does not submit empty text via keyboard shortcut', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');

      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });
      });

      expect(mockAddComment).not.toHaveBeenCalled();
    });

    it('does not submit whitespace-only text via keyboard shortcut', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: '   ' } });

      await act(async () => {
        fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });
      });

      expect(mockAddComment).not.toHaveBeenCalled();
    });

    it('prevents default on keyboard submit', async () => {
      render(<CommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('comment-textarea');
      fireEvent.change(textarea, { target: { value: 'Comment' } });

      const event = new KeyboardEvent('keydown', {
        key: 'Enter',
        metaKey: true,
        bubbles: true,
        cancelable: true,
      });
      const preventDefault = vi.spyOn(event, 'preventDefault');

      await act(async () => {
        textarea.dispatchEvent(event);
      });

      expect(preventDefault).toHaveBeenCalled();
    });
  });
});
