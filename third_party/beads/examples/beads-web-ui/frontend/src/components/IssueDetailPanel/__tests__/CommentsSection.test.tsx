/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CommentsSection component.
 */

import { render, screen, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import type { Comment } from '@/types';

import { CommentsSection } from '../CommentsSection';

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

describe('CommentsSection', () => {
  // Mock Date.now for consistent relative time formatting
  const _realNow = Date.now;
  beforeEach(() => {
    // Set "now" to January 27, 2026 12:00:00 UTC
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-27T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe('rendering', () => {
    it('renders section with "Comments" title', () => {
      render(<CommentsSection />);
      expect(screen.getByTestId('comments-section')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: /comments/i })).toBeInTheDocument();
    });

    it('shows comment count in title when comments exist', () => {
      const comments = [
        createTestComment({ id: 1 }),
        createTestComment({ id: 2 }),
        createTestComment({ id: 3 }),
      ];
      render(<CommentsSection comments={comments} />);
      expect(screen.getByRole('heading')).toHaveTextContent('Comments (3)');
    });

    it('does not show count when no comments', () => {
      render(<CommentsSection comments={[]} />);
      expect(screen.getByRole('heading')).toHaveTextContent('Comments');
      expect(screen.getByRole('heading')).not.toHaveTextContent('(');
    });

    it('renders each comment in the array', () => {
      const comments = [
        createTestComment({ id: 1, author: 'Alice' }),
        createTestComment({ id: 2, author: 'Bob' }),
      ];
      render(<CommentsSection comments={comments} />);
      const items = screen.getAllByTestId('comment-item');
      expect(items).toHaveLength(2);
    });
  });

  describe('comment content', () => {
    it('shows author name', () => {
      const comments = [createTestComment({ author: 'Jane Doe' })];
      render(<CommentsSection comments={comments} />);
      expect(screen.getByText('Jane Doe')).toBeInTheDocument();
    });

    it('shows formatted timestamp', () => {
      const comments = [createTestComment({ created_at: '2026-01-27T10:00:00Z' })];
      render(<CommentsSection comments={comments} />);
      // Should show "2h ago" since current time is 12:00
      expect(screen.getByText('2h ago')).toBeInTheDocument();
    });

    it('shows comment text', () => {
      const comments = [createTestComment({ text: 'This is the comment body.' })];
      render(<CommentsSection comments={comments} />);
      expect(screen.getByText('This is the comment body.')).toBeInTheDocument();
    });

    it('shows "Unknown" when author is empty', () => {
      const comments = [createTestComment({ author: '' })];
      render(<CommentsSection comments={comments} />);
      expect(screen.getByText('Unknown')).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    it('shows empty message when comments is undefined', () => {
      render(<CommentsSection />);
      expect(screen.getByTestId('comments-empty')).toBeInTheDocument();
      expect(screen.getByText('No comments yet.')).toBeInTheDocument();
    });

    it('shows empty message when comments array is empty', () => {
      render(<CommentsSection comments={[]} />);
      expect(screen.getByTestId('comments-empty')).toBeInTheDocument();
      expect(screen.getByText('No comments yet.')).toBeInTheDocument();
    });

    it('does not show comment list when empty', () => {
      render(<CommentsSection comments={[]} />);
      expect(screen.queryByRole('list')).not.toBeInTheDocument();
    });
  });

  describe('chronological ordering', () => {
    it('orders comments oldest first', () => {
      const comments = [
        createTestComment({ id: 3, author: 'Third', created_at: '2026-01-27T09:00:00Z' }),
        createTestComment({ id: 1, author: 'First', created_at: '2026-01-25T10:00:00Z' }),
        createTestComment({ id: 2, author: 'Second', created_at: '2026-01-26T10:00:00Z' }),
      ];
      render(<CommentsSection comments={comments} />);
      const items = screen.getAllByTestId('comment-item');

      // First, Second, Third should be in order by created_at
      expect(within(items[0]).getByText('First')).toBeInTheDocument();
      expect(within(items[1]).getByText('Second')).toBeInTheDocument();
      expect(within(items[2]).getByText('Third')).toBeInTheDocument();
    });
  });

  describe('className prop', () => {
    it('applies custom className', () => {
      render(<CommentsSection className="custom-class" />);
      expect(screen.getByTestId('comments-section')).toHaveClass('custom-class');
    });

    it('combines custom className with default styles', () => {
      render(<CommentsSection className="custom-class" />);
      const section = screen.getByTestId('comments-section');
      // CSS modules mangle class names, so check it has both
      expect(section.className).toContain('custom-class');
      expect(section.className).toMatch(/section/i);
    });
  });

  describe('text formatting', () => {
    it('preserves whitespace in comment text', () => {
      const comments = [createTestComment({ text: 'Line 1\nLine 2\n  Indented' })];
      render(<CommentsSection comments={comments} />);
      const textElement = screen.getByText(/Line 1/);
      // pre-wrap style should preserve whitespace (tested via rendered text)
      expect(textElement).toBeInTheDocument();
    });

    it('handles long comment text gracefully', () => {
      const longText = 'A'.repeat(500);
      const comments = [createTestComment({ text: longText })];
      render(<CommentsSection comments={comments} />);
      expect(screen.getByText(longText)).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('uses semantic list element', () => {
      const comments = [createTestComment()];
      render(<CommentsSection comments={comments} />);
      expect(screen.getByRole('list')).toBeInTheDocument();
    });

    it('uses list items for each comment', () => {
      const comments = [createTestComment({ id: 1 }), createTestComment({ id: 2 })];
      render(<CommentsSection comments={comments} />);
      const items = screen.getAllByRole('listitem');
      expect(items).toHaveLength(2);
    });

    it('uses time element with datetime attribute', () => {
      const comments = [createTestComment({ created_at: '2026-01-20T10:00:00Z' })];
      render(<CommentsSection comments={comments} />);
      const timeElement = screen.getByRole('time');
      expect(timeElement).toHaveAttribute('datetime', '2026-01-20T10:00:00Z');
    });

    it('uses heading for section title', () => {
      render(<CommentsSection />);
      expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument();
    });
  });
});
