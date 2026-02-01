/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BlockedSummary component.
 */

import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { BlockedSummary } from '../BlockedSummary';
import type { BlockedIssue, Priority } from '@/types';

// Mock the useBlockedIssues hook
vi.mock('@/hooks', () => ({
  useBlockedIssues: vi.fn(),
}));

// Import mock after vi.mock call
import { useBlockedIssues } from '@/hooks';

/**
 * Create a test blocked issue with required fields.
 */
function createBlockedIssue(overrides: Partial<BlockedIssue> = {}): BlockedIssue {
  return {
    id: 'test-issue-1',
    title: 'Test Issue Title',
    priority: 2 as Priority,
    created_at: '2026-01-25T00:00:00Z',
    updated_at: '2026-01-25T00:00:00Z',
    blocked_by_count: 1,
    blocked_by: ['blocker-1'],
    ...overrides,
  };
}

/**
 * Setup useBlockedIssues mock with default return values.
 */
function setupMocks(options: {
  data?: BlockedIssue[] | null;
  loading?: boolean;
  error?: Error | null;
  refetch?: Mock;
} = {}) {
  const {
    data = null,
    loading = false,
    error = null,
    refetch = vi.fn(),
  } = options;

  (useBlockedIssues as Mock).mockReturnValue({
    data,
    loading,
    error,
    refetch,
  });

  return { refetch };
}

describe('BlockedSummary', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setupMocks({ data: [] });
  });

  describe('rendering', () => {
    it('renders badge with count of 0', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      expect(screen.getByText('0')).toBeInTheDocument();
    });

    it('renders badge with non-zero count', () => {
      setupMocks({ data: [createBlockedIssue(), createBlockedIssue({ id: 'issue-2' })] });
      render(<BlockedSummary />);

      expect(screen.getByText('2')).toBeInTheDocument();
    });

    it('renders as a button element', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      expect(button.tagName).toBe('BUTTON');
    });

    it('applies custom className', () => {
      setupMocks({ data: [] });
      const { container } = render(<BlockedSummary className="custom-class" />);

      const root = container.firstChild as HTMLElement;
      expect(root).toHaveClass('custom-class');
    });
  });

  describe('styling', () => {
    it('has data-has-blocked="false" when count is 0', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      expect(button).toHaveAttribute('data-has-blocked', 'false');
    });

    it('has data-has-blocked="true" when count is > 0', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      expect(button).toHaveAttribute('data-has-blocked', 'true');
    });
  });

  describe('toggle dropdown', () => {
    it('opens dropdown on click', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByRole('menu')).toBeInTheDocument();
    });

    it('closes dropdown on second click', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);
      expect(screen.getByRole('menu')).toBeInTheDocument();

      fireEvent.click(button);
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('calls onBadgeClick when badge is clicked', () => {
      const onBadgeClick = vi.fn();
      setupMocks({ data: [] });
      render(<BlockedSummary onBadgeClick={onBadgeClick} />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(onBadgeClick).toHaveBeenCalledTimes(1);
    });
  });

  describe('keyboard navigation', () => {
    it('opens dropdown on Enter key', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.keyDown(button, { key: 'Enter' });

      expect(screen.getByRole('menu')).toBeInTheDocument();
    });

    it('opens dropdown on Space key', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.keyDown(button, { key: ' ' });

      expect(screen.getByRole('menu')).toBeInTheDocument();
    });

    it('closes dropdown on Escape key', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);
      expect(screen.getByRole('menu')).toBeInTheDocument();

      fireEvent.keyDown(button, { key: 'Escape' });
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('calls onBadgeClick on Enter key', () => {
      const onBadgeClick = vi.fn();
      setupMocks({ data: [] });
      render(<BlockedSummary onBadgeClick={onBadgeClick} />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.keyDown(button, { key: 'Enter' });

      expect(onBadgeClick).toHaveBeenCalledTimes(1);
    });

    it('calls onBadgeClick on Space key', () => {
      const onBadgeClick = vi.fn();
      setupMocks({ data: [] });
      render(<BlockedSummary onBadgeClick={onBadgeClick} />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.keyDown(button, { key: ' ' });

      expect(onBadgeClick).toHaveBeenCalledTimes(1);
    });
  });

  describe('issue click', () => {
    it('calls onIssueClick with correct ID when issue is clicked', () => {
      const onIssueClick = vi.fn();
      setupMocks({ data: [createBlockedIssue({ id: 'clicked-issue' })] });
      render(<BlockedSummary onIssueClick={onIssueClick} />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      // Click on issue
      const issueItem = screen.getByRole('menuitem', { name: /clicked-issue/i });
      fireEvent.click(issueItem);

      expect(onIssueClick).toHaveBeenCalledWith('clicked-issue');
      expect(onIssueClick).toHaveBeenCalledTimes(1);
    });

    it('closes dropdown after issue click', () => {
      const onIssueClick = vi.fn();
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary onIssueClick={onIssueClick} />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);
      expect(screen.getByRole('menu')).toBeInTheDocument();

      // Click on issue
      const issueItem = screen.getByRole('menuitem', { name: /test-issue-1/i });
      fireEvent.click(issueItem);

      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('calls onIssueClick with "__show_all_blocked__" when footer is clicked', () => {
      const onIssueClick = vi.fn();
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary onIssueClick={onIssueClick} />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      // Click on "Show all blocked" footer
      const footerButton = screen.getByRole('menuitem', { name: /show all blocked/i });
      fireEvent.click(footerButton);

      expect(onIssueClick).toHaveBeenCalledWith('__show_all_blocked__');
    });
  });

  describe('truncation', () => {
    it('shows all issues when count <= maxDisplayed', () => {
      const issues = Array.from({ length: 3 }, (_, i) =>
        createBlockedIssue({ id: `issue-${i + 1}`, title: `Issue ${i + 1}` })
      );
      setupMocks({ data: issues });
      render(<BlockedSummary maxDisplayed={10} />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText(/issue-1/)).toBeInTheDocument();
      expect(screen.getByText(/issue-2/)).toBeInTheDocument();
      expect(screen.getByText(/issue-3/)).toBeInTheDocument();
      expect(screen.queryByText(/and \d+ more/)).not.toBeInTheDocument();
    });

    it('shows "and N more..." when count > maxDisplayed', () => {
      const issues = Array.from({ length: 5 }, (_, i) =>
        createBlockedIssue({ id: `issue-${i + 1}`, title: `Issue ${i + 1}` })
      );
      setupMocks({ data: issues });
      render(<BlockedSummary maxDisplayed={3} />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      // First 3 should be shown
      expect(screen.getByText(/issue-1/)).toBeInTheDocument();
      expect(screen.getByText(/issue-2/)).toBeInTheDocument();
      expect(screen.getByText(/issue-3/)).toBeInTheDocument();

      // Last 2 should not be shown individually
      expect(screen.queryByText(/issue-4/)).not.toBeInTheDocument();
      expect(screen.queryByText(/issue-5/)).not.toBeInTheDocument();

      // "and N more..." should be shown
      expect(screen.getByText('and 2 more...')).toBeInTheDocument();
    });

    it('shows "and 1 more..." when exactly one more than maxDisplayed', () => {
      const issues = Array.from({ length: 4 }, (_, i) =>
        createBlockedIssue({ id: `issue-${i + 1}`, title: `Issue ${i + 1}` })
      );
      setupMocks({ data: issues });
      render(<BlockedSummary maxDisplayed={3} />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('and 1 more...')).toBeInTheDocument();
    });

    it('truncates long issue titles', () => {
      const longTitle = 'This is a very long issue title that should be truncated';
      setupMocks({ data: [createBlockedIssue({ title: longTitle })] });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      // Title should be truncated (default 30 chars)
      const truncatedTitle = screen.getByText(/This is a very long issue tit…/);
      expect(truncatedTitle).toBeInTheDocument();
    });
  });

  describe('loading state', () => {
    it('shows loading indicator when loading and no data', () => {
      setupMocks({ loading: true, data: null });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('Loading...')).toBeInTheDocument();
    });

    it('does not show loading indicator when loading with existing data', () => {
      setupMocks({ loading: true, data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.queryByText('Loading...')).not.toBeInTheDocument();
    });
  });

  describe('error state', () => {
    it('shows error message when error occurs', () => {
      setupMocks({ error: new Error('Failed to fetch'), data: null });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('Failed to load blocked issues')).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    it('shows "No blocked issues" when count is 0', () => {
      setupMocks({ data: [], loading: false, error: null });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('No blocked issues')).toBeInTheDocument();
    });

    it('does not show "Show all blocked" footer when count is 0', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.queryByText(/show all blocked/i)).not.toBeInTheDocument();
    });
  });

  describe('click outside', () => {
    it('closes dropdown when clicking outside', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(
        <div>
          <BlockedSummary />
          <div data-testid="outside">Outside element</div>
        </div>
      );

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);
      expect(screen.getByRole('menu')).toBeInTheDocument();

      // Click outside
      const outside = screen.getByTestId('outside');
      fireEvent.mouseDown(outside);

      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('does not close dropdown when clicking inside', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      // Open dropdown
      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      // Click inside the dropdown (on the header)
      const header = screen.getByText('1 Blocked Issue');
      fireEvent.mouseDown(header);

      expect(screen.getByRole('menu')).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has aria-expanded attribute that reflects open state', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      expect(button).toHaveAttribute('aria-expanded', 'false');

      fireEvent.click(button);
      expect(button).toHaveAttribute('aria-expanded', 'true');

      fireEvent.click(button);
      expect(button).toHaveAttribute('aria-expanded', 'false');
    });

    it('has aria-haspopup="menu" attribute', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      expect(button).toHaveAttribute('aria-haspopup', 'menu');
    });

    it('dropdown has role="menu"', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByRole('menu')).toBeInTheDocument();
    });

    it('issue items have role="menuitem"', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      const menuItems = screen.getAllByRole('menuitem');
      // One for the issue, one for the "Show all blocked" footer
      expect(menuItems.length).toBeGreaterThanOrEqual(1);
    });

    it('has correct aria-label with count', () => {
      setupMocks({ data: [createBlockedIssue(), createBlockedIssue({ id: 'issue-2' })] });
      render(<BlockedSummary />);

      expect(screen.getByLabelText('2 blocked issues')).toBeInTheDocument();
    });

    it('dropdown has aria-label', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByLabelText('Blocked issues list')).toBeInTheDocument();
    });
  });

  describe('issue display', () => {
    it('displays issue ID', () => {
      setupMocks({ data: [createBlockedIssue({ id: 'PROJ-123' })] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('PROJ-123')).toBeInTheDocument();
    });

    it('displays priority badge', () => {
      setupMocks({ data: [createBlockedIssue({ priority: 1 as Priority })] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('P1')).toBeInTheDocument();
    });

    it('displays blocked by count', () => {
      setupMocks({ data: [createBlockedIssue({ blocked_by_count: 3 })] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('Blocked by 3 issues')).toBeInTheDocument();
    });

    it('uses singular "issue" when blocked by 1', () => {
      setupMocks({ data: [createBlockedIssue({ blocked_by_count: 1 })] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('Blocked by 1 issue')).toBeInTheDocument();
    });
  });

  describe('header text', () => {
    it('shows correct singular header for 1 issue', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('1 Blocked Issue')).toBeInTheDocument();
    });

    it('shows correct plural header for multiple issues', () => {
      setupMocks({
        data: [
          createBlockedIssue({ id: 'issue-1' }),
          createBlockedIssue({ id: 'issue-2' }),
        ],
      });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('2 Blocked Issues')).toBeInTheDocument();
    });

    it('shows correct header for 0 issues', () => {
      setupMocks({ data: [] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText('0 Blocked Issues')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles null data gracefully', () => {
      setupMocks({ data: null });
      render(<BlockedSummary />);

      expect(screen.getByText('0')).toBeInTheDocument();
    });

    it('handles rapid open/close without errors', () => {
      setupMocks({ data: [createBlockedIssue()] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });

      // Rapid clicks
      fireEvent.click(button);
      fireEvent.click(button);
      fireEvent.click(button);
      fireEvent.click(button);

      // Should end up closed (even number of clicks)
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });

    it('handles issues with special characters in title', () => {
      setupMocks({
        data: [createBlockedIssue({ title: '<script>alert("xss")</script>' })],
      });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      // React should escape the content
      expect(screen.getByText(/<script>alert\("xss"\)<\/script>/)).toBeInTheDocument();
    });

    it('handles very long issue IDs', () => {
      const longId = 'very-long-issue-id-that-might-cause-display-issues-12345678';
      setupMocks({ data: [createBlockedIssue({ id: longId })] });
      render(<BlockedSummary />);

      const button = screen.getByRole('button', { name: /blocked issues/i });
      fireEvent.click(button);

      expect(screen.getByText(longId)).toBeInTheDocument();
    });
  });
});
