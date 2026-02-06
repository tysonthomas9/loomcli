/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RejectCommentForm component (IssueDetailPanel variant).
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import { RejectCommentForm } from '../RejectCommentForm';

describe('RejectCommentForm (IssueDetailPanel)', () => {
  const defaultProps = {
    issueId: 'test-issue-123',
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
  };

  describe('rendering', () => {
    it('renders form with textarea and buttons', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByTestId('reject-comment-form')).toBeInTheDocument();
      expect(screen.getByTestId('reject-textarea')).toBeInTheDocument();
      expect(screen.getByTestId('reject-submit')).toBeInTheDocument();
      expect(screen.getByTestId('reject-cancel')).toBeInTheDocument();
    });

    it('renders label with question text', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByText('Why are you rejecting this?')).toBeInTheDocument();
    });

    it('textarea has correct placeholder', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByTestId('reject-textarea')).toHaveAttribute(
        'placeholder',
        'Enter feedback...'
      );
    });

    it('submit button shows "Reject"', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByTestId('reject-submit')).toHaveTextContent('Reject');
    });

    it('cancel button shows "Cancel"', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByTestId('reject-cancel')).toHaveTextContent('Cancel');
    });

    it('has accessible aria-label on form', () => {
      render(<RejectCommentForm {...defaultProps} issueId="bd-xyz123" />);

      expect(screen.getByLabelText('Rejection feedback for issue bd-xyz123')).toBeInTheDocument();
    });

    it('textarea has associated label via htmlFor', () => {
      render(<RejectCommentForm {...defaultProps} issueId="my-issue" />);

      const textarea = screen.getByTestId('reject-textarea');
      expect(textarea).toHaveAttribute('id', 'panel-reject-textarea-my-issue');
    });

    it('textarea has 3 rows', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByTestId('reject-textarea')).toHaveAttribute('rows', '3');
    });
  });

  describe('submit button state', () => {
    it('submit button disabled when text is empty', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.getByTestId('reject-submit')).toBeDisabled();
    });

    it('submit button disabled when text is whitespace only', () => {
      render(<RejectCommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: '   ' } });

      expect(screen.getByTestId('reject-submit')).toBeDisabled();
    });

    it('submit button enabled when text is present', () => {
      render(<RejectCommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: 'Some feedback' } });

      expect(screen.getByTestId('reject-submit')).not.toBeDisabled();
    });
  });

  describe('submit interaction', () => {
    it('clicking submit calls onSubmit with trimmed text', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: '  needs more work  ' } });
      fireEvent.click(screen.getByTestId('reject-submit'));

      expect(onSubmit).toHaveBeenCalledWith('needs more work');
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });

    it('form submission calls onSubmit', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');
      const form = screen.getByTestId('reject-comment-form');

      fireEvent.change(textarea, { target: { value: 'feedback text' } });
      fireEvent.submit(form);

      expect(onSubmit).toHaveBeenCalledWith('feedback text');
    });

    it('does not call onSubmit when text is empty', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const form = screen.getByTestId('reject-comment-form');

      fireEvent.submit(form);

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it('does not call onSubmit when text is whitespace only', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');
      const form = screen.getByTestId('reject-comment-form');

      fireEvent.change(textarea, { target: { value: '   ' } });
      fireEvent.submit(form);

      expect(onSubmit).not.toHaveBeenCalled();
    });
  });

  describe('cancel interaction', () => {
    it('clicking cancel calls onCancel', () => {
      const onCancel = vi.fn();
      render(<RejectCommentForm {...defaultProps} onCancel={onCancel} />);

      fireEvent.click(screen.getByTestId('reject-cancel'));

      expect(onCancel).toHaveBeenCalledTimes(1);
    });
  });

  describe('keyboard shortcuts', () => {
    it('Cmd+Enter submits form', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: 'keyboard submit' } });
      fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });

      expect(onSubmit).toHaveBeenCalledWith('keyboard submit');
    });

    it('Ctrl+Enter submits form', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: 'ctrl enter submit' } });
      fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true });

      expect(onSubmit).toHaveBeenCalledWith('ctrl enter submit');
    });

    it('regular Enter does not submit form', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: 'some text' } });
      fireEvent.keyDown(textarea, { key: 'Enter' });

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it('Escape cancels form', () => {
      const onCancel = vi.fn();
      render(<RejectCommentForm {...defaultProps} onCancel={onCancel} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.keyDown(textarea, { key: 'Escape' });

      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it('Cmd+Enter does not submit when text is empty', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });

      expect(onSubmit).not.toHaveBeenCalled();
    });
  });

  describe('submitting state', () => {
    it('shows "Submitting..." during submission', () => {
      render(<RejectCommentForm {...defaultProps} isSubmitting={true} />);

      expect(screen.getByTestId('reject-submit')).toHaveTextContent('Submitting...');
    });

    it('submit button disabled during submission', () => {
      render(<RejectCommentForm {...defaultProps} isSubmitting={true} />);
      const textarea = screen.getByTestId('reject-textarea');

      // Add text to make sure it would otherwise be enabled
      fireEvent.change(textarea, { target: { value: 'some text' } });

      expect(screen.getByTestId('reject-submit')).toBeDisabled();
    });

    it('textarea disabled during submission', () => {
      render(<RejectCommentForm {...defaultProps} isSubmitting={true} />);

      expect(screen.getByTestId('reject-textarea')).toBeDisabled();
    });

    it('cancel button disabled during submission', () => {
      render(<RejectCommentForm {...defaultProps} isSubmitting={true} />);

      expect(screen.getByTestId('reject-cancel')).toBeDisabled();
    });

    it('does not submit when already submitting', () => {
      const onSubmit = vi.fn();
      render(<RejectCommentForm {...defaultProps} onSubmit={onSubmit} isSubmitting={true} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: 'some text' } });
      fireEvent.keyDown(textarea, { key: 'Enter', metaKey: true });

      expect(onSubmit).not.toHaveBeenCalled();
    });
  });

  describe('error state', () => {
    it('displays error message when error prop is provided', () => {
      render(<RejectCommentForm {...defaultProps} error="Something went wrong" />);

      expect(screen.getByTestId('reject-error')).toHaveTextContent('Something went wrong');
    });

    it('error has role="alert"', () => {
      render(<RejectCommentForm {...defaultProps} error="Network error" />);

      expect(screen.getByRole('alert')).toHaveTextContent('Network error');
    });

    it('does not render error element when error is null', () => {
      render(<RejectCommentForm {...defaultProps} error={null} />);

      expect(screen.queryByTestId('reject-error')).not.toBeInTheDocument();
    });

    it('does not render error element when error is undefined', () => {
      render(<RejectCommentForm {...defaultProps} />);

      expect(screen.queryByTestId('reject-error')).not.toBeInTheDocument();
    });
  });

  describe('typing interaction', () => {
    it('typing updates textarea value', () => {
      render(<RejectCommentForm {...defaultProps} />);
      const textarea = screen.getByTestId('reject-textarea');

      fireEvent.change(textarea, { target: { value: 'New feedback text' } });

      expect(textarea).toHaveValue('New feedback text');
    });
  });
});
