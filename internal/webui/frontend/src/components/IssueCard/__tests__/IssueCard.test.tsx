/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueCard component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import type { Issue } from '@/types';

import { IssueCard } from '../IssueCard';
import styles from '../IssueCard.module.css';

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-issue-abc123',
    title: 'Test Issue Title',
    priority: 2,
    created_at: '2024-01-15T10:30:00Z',
    updated_at: '2024-01-15T10:30:00Z',
    ...overrides,
  };
}

describe('IssueCard', () => {
  describe('rendering', () => {
    it('renders issue title', () => {
      const issue = createTestIssue({ title: 'My Issue Title' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByRole('heading', { name: 'My Issue Title' })).toBeInTheDocument();
    });

    it('renders issue ID (shortened)', () => {
      const issue = createTestIssue({ id: 'beads-abc123def456' });
      render(<IssueCard issue={issue} />);

      // Should show last 7 characters
      expect(screen.getByText('3def456')).toBeInTheDocument();
    });

    it('renders short ID as-is', () => {
      const issue = createTestIssue({ id: 'bd-xyz' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText('bd-xyz')).toBeInTheDocument();
    });

    it('renders priority badge with correct text', () => {
      const issue = createTestIssue({ priority: 1 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText('P1')).toBeInTheDocument();
    });

    it('renders with article element', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      expect(container.querySelector('article')).toBeInTheDocument();
    });
  });

  describe('priority display', () => {
    it.each([0, 1, 2, 3, 4] as const)('renders P%i correctly', (priority) => {
      const issue = createTestIssue({ priority });
      const { container } = render(<IssueCard issue={issue} />);

      expect(screen.getByText(`P${priority}`)).toBeInTheDocument();
      expect(container.querySelector('[data-priority]')).toHaveAttribute(
        'data-priority',
        String(priority)
      );
    });

    /**
     * P2 priority badge contrast fix verification.
     * The CSS uses data-priority="2" to apply dark text color for WCAG AA contrast
     * on the yellow background. This test ensures the attribute is correctly set.
     */
    it('P2 priority badge has data-priority="2" for CSS contrast styling', () => {
      const issue = createTestIssue({ priority: 2 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText('P2');
      expect(priorityBadge).toHaveAttribute('data-priority', '2');
    });

    it('defaults to P4 when priority is undefined', () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      render(<IssueCard issue={issue} />);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });

    it('defaults to P4 for out of range priority (negative)', () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: -1 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });

    it('defaults to P4 for out of range priority (> 4)', () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 5 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });
  });

  describe('priority badge styling', () => {
    it.each([0, 1, 2, 3, 4] as const)(
      'applies priority%i class to priority badge for priority %i',
      (priority) => {
        const issue = createTestIssue({ priority });
        render(<IssueCard issue={issue} />);

        const priorityBadge = screen.getByText(`P${priority}`);
        // CSS Modules hashes class names, so we check for the pattern
        expect(priorityBadge.className).toMatch(new RegExp(`priority${priority}`));
      }
    );

    it('applies both priorityBadge base class and priority-specific class', () => {
      const issue = createTestIssue({ priority: 2 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText('P2');
      // Should have both the base priorityBadge class and priority2 class
      expect(priorityBadge.className).toMatch(/priorityBadge/);
      expect(priorityBadge.className).toMatch(/priority2/);
    });

    it('priority badge has data-priority attribute for backwards compatibility', () => {
      const issue = createTestIssue({ priority: 1 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText('P1');
      expect(priorityBadge).toHaveAttribute('data-priority', '1');
    });

    it.each([0, 1, 2, 3, 4] as const)(
      'priority badge has data-priority="%i" attribute',
      (priority) => {
        const issue = createTestIssue({ priority });
        render(<IssueCard issue={issue} />);

        const priorityBadge = screen.getByText(`P${priority}`);
        expect(priorityBadge).toHaveAttribute('data-priority', String(priority));
      }
    );

    it('applies priority4 class when priority is undefined (default)', () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText('P4');
      expect(priorityBadge.className).toMatch(/priority4/);
      expect(priorityBadge).toHaveAttribute('data-priority', '4');
    });

    it('applies priority4 class for out of range priority', () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 99 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText('P4');
      expect(priorityBadge.className).toMatch(/priority4/);
      expect(priorityBadge).toHaveAttribute('data-priority', '4');
    });
  });

  describe('onClick interaction', () => {
    it('calls onClick when card is clicked', () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.click(screen.getByRole('button'));
      expect(handleClick).toHaveBeenCalledWith(issue);
      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('does not crash when onClick is not provided', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} />);

      // Should not throw when clicked
      const article = document.querySelector('article');
      expect(() => fireEvent.click(article!)).not.toThrow();
    });

    it('calls onClick on Enter key', () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.keyDown(screen.getByRole('button'), { key: 'Enter' });
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it('calls onClick on Space key', () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.keyDown(screen.getByRole('button'), { key: ' ' });
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it('does not call onClick on other keys', () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.keyDown(screen.getByRole('button'), { key: 'Tab' });
      fireEvent.keyDown(screen.getByRole('button'), { key: 'Escape' });
      expect(handleClick).not.toHaveBeenCalled();
    });
  });

  describe('accessibility', () => {
    it('has aria-label with issue title', () => {
      const issue = createTestIssue({ title: 'Test Accessibility' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByLabelText('Issue: Test Accessibility')).toBeInTheDocument();
    });

    it('has button role when onClick is provided', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} onClick={() => {}} />);

      expect(screen.getByRole('button')).toBeInTheDocument();
    });

    it('does not have button role when onClick is not provided', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} />);

      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('is keyboard focusable when onClick is provided', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} onClick={() => {}} />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('tabIndex', '0');
    });

    it('is not keyboard focusable when onClick is not provided', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector('article');
      expect(article).not.toHaveAttribute('tabIndex');
    });

    it('priority badge has aria-label', () => {
      const issue = createTestIssue({ priority: 0 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByLabelText('Priority 0')).toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies className prop to root element', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} className="custom-class" />);

      const article = container.querySelector('article');
      expect(article).toHaveClass('custom-class');
    });

    it('data-priority attribute matches issue priority', () => {
      const issue = createTestIssue({ priority: 3 });
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-priority', '3');
    });

    it('renders data-column attribute with columnId prop value', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} columnId="in_progress" />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-column', 'in_progress');
    });

    it('renders data-column attribute with "review" columnId', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} columnId="review" />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-column', 'review');
    });

    it('renders data-column attribute with "done" columnId', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} columnId="done" />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-column', 'done');
    });

    it('data-column attribute is undefined when no columnId is provided', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector('article');
      expect(article).not.toHaveAttribute('data-column');
    });
  });

  describe('edge cases', () => {
    it('renders "Untitled" for missing title', () => {
      const issue = createTestIssue({ title: '' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByRole('heading', { name: 'Untitled' })).toBeInTheDocument();
    });

    it('renders "unknown" for missing ID', () => {
      const issue = createTestIssue({ id: '' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText('unknown')).toBeInTheDocument();
    });

    it('handles very long title', () => {
      const longTitle = 'A'.repeat(200);
      const issue = createTestIssue({ title: longTitle });
      render(<IssueCard issue={issue} />);

      // Should still render, truncation is handled by CSS
      expect(screen.getByRole('heading', { name: longTitle })).toBeInTheDocument();
    });

    it('renders with minimal issue props', () => {
      // Only required fields
      const issue: Issue = {
        id: 'min-id',
        title: 'Minimal',
        priority: 2,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };
      render(<IssueCard issue={issue} />);

      expect(screen.getByRole('heading', { name: 'Minimal' })).toBeInTheDocument();
      expect(screen.getByText('min-id')).toBeInTheDocument();
      expect(screen.getByText('P2')).toBeInTheDocument();
    });

    it('renders with full issue props', () => {
      const issue = createTestIssue({
        id: 'full-issue-id',
        title: 'Full Issue',
        priority: 0,
        status: 'open',
        description: 'A description',
        assignee: 'user',
        labels: ['bug', 'urgent'],
      });
      render(<IssueCard issue={issue} />);

      expect(screen.getByRole('heading', { name: 'Full Issue' })).toBeInTheDocument();
      expect(screen.getByText('P0')).toBeInTheDocument();
    });
  });

  describe('blocked badge display', () => {
    it('renders BlockedBadge when blockedByCount > 0', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={3} />);

      expect(screen.getByLabelText('Blocked by 3 issues')).toBeInTheDocument();
    });

    it('does not render BlockedBadge when blockedByCount is 0', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={0} />);

      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it('does not render BlockedBadge when blockedByCount is undefined', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} />);

      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it('passes blockedBy array to BlockedBadge', () => {
      const issue = createTestIssue();
      const blockers = ['blocker-1', 'blocker-2'];
      render(<IssueCard issue={issue} blockedByCount={2} blockedBy={blockers} />);

      // Hover to show tooltip
      const badge = screen.getByLabelText('Blocked by 2 issues');
      fireEvent.mouseEnter(badge);

      expect(screen.getByText('blocker-1')).toBeInTheDocument();
      expect(screen.getByText('blocker-2')).toBeInTheDocument();
    });

    it('sets data-blocked attribute to true when blocked', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} blockedByCount={1} />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-blocked', 'true');
    });

    it('does not set data-blocked attribute when not blocked', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector('article');
      expect(article).not.toHaveAttribute('data-blocked');
    });

    it('does not set data-blocked when blockedByCount is 0', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} blockedByCount={0} />);

      const article = container.querySelector('article');
      expect(article).not.toHaveAttribute('data-blocked');
    });

    it('aria-label includes (blocked) when issue is blocked', () => {
      const issue = createTestIssue({ title: 'Blocked Issue' });
      render(<IssueCard issue={issue} blockedByCount={1} />);

      expect(screen.getByLabelText('Issue: Blocked Issue (blocked)')).toBeInTheDocument();
    });

    it('aria-label does not include (blocked) when not blocked', () => {
      const issue = createTestIssue({ title: 'Normal Issue' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByLabelText('Issue: Normal Issue')).toBeInTheDocument();
      expect(screen.queryByLabelText(/blocked/)).not.toBeInTheDocument();
    });

    it('renders BlockedBadge with blockedByCount of 1', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={1} />);

      expect(screen.getByLabelText('Blocked by 1 issue')).toBeInTheDocument();
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    it('renders BlockedBadge with large blockedByCount', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={99} />);

      expect(screen.getByLabelText('Blocked by 99 issues')).toBeInTheDocument();
      expect(screen.getByText('99')).toBeInTheDocument();
    });

    it('renders BlockedBadge without blockedBy array', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={5} />);

      // Badge should still render
      expect(screen.getByLabelText('Blocked by 5 issues')).toBeInTheDocument();

      // Tooltip should not show when hovering (no blockers to display)
      const badge = screen.getByLabelText('Blocked by 5 issues');
      fireEvent.mouseEnter(badge);
      expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    });
  });

  describe('isBacklog prop', () => {
    it('renders with data-in-backlog="true" when isBacklog is true', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} isBacklog={true} />);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-in-backlog', 'true');
    });

    it('does not render data-in-backlog attribute when isBacklog is false', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} isBacklog={false} />);

      const article = container.querySelector('article');
      expect(article).not.toHaveAttribute('data-in-backlog');
    });

    it('does not render data-in-backlog attribute when isBacklog is undefined', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector('article');
      expect(article).not.toHaveAttribute('data-in-backlog');
    });

    it('includes (backlog) in aria-label when isBacklog is true', () => {
      const issue = createTestIssue({ title: 'Backlog Issue' });
      render(<IssueCard issue={issue} isBacklog={true} />);

      expect(screen.getByLabelText('Issue: Backlog Issue (backlog)')).toBeInTheDocument();
    });

    it('aria-label does not include (backlog) when isBacklog is false', () => {
      const issue = createTestIssue({ title: 'Normal Issue' });
      render(<IssueCard issue={issue} isBacklog={false} />);

      expect(screen.getByLabelText('Issue: Normal Issue')).toBeInTheDocument();
    });

    it('aria-label includes both (blocked) and (backlog) when both are true', () => {
      const issue = createTestIssue({ title: 'Complex Issue' });
      render(<IssueCard issue={issue} blockedByCount={1} isBacklog={true} />);

      expect(screen.getByLabelText('Issue: Complex Issue (blocked) (backlog)')).toBeInTheDocument();
    });
  });

  describe('deferred badge', () => {
    it('renders deferred badge when issue status is "deferred"', () => {
      const issue = createTestIssue({ status: 'deferred' });
      render(<IssueCard issue={issue} />);

      const badge = screen.getByLabelText('Deferred');
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveTextContent('Deferred');
      expect(screen.getByText('⏸')).toBeInTheDocument();
    });

    it('does not render deferred badge for non-deferred status', () => {
      const issue = createTestIssue({ status: 'open' });
      render(<IssueCard issue={issue} />);

      expect(screen.queryByLabelText('Deferred')).not.toBeInTheDocument();
    });

    it('deferred badge has aria-label="Deferred"', () => {
      const issue = createTestIssue({ status: 'deferred' });
      render(<IssueCard issue={issue} />);

      expect(screen.getByLabelText('Deferred')).toBeInTheDocument();
    });

    it('deferred badge icon has aria-hidden', () => {
      const issue = createTestIssue({ status: 'deferred' });
      render(<IssueCard issue={issue} />);

      const icon = screen.getByText('⏸');
      expect(icon).toHaveAttribute('aria-hidden', 'true');
    });
  });

  describe('review type badge', () => {
    describe('getReviewType logic', () => {
      it('returns plan when title contains [Need Review]', () => {
        const issue = createTestIssue({ title: '[Need Review] My feature plan' });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText('Plan')).toBeInTheDocument();
        expect(screen.getByLabelText('Plan review')).toBeInTheDocument();
      });

      it('returns code when status is review AND title does not contain [Need Review]', () => {
        const issue = createTestIssue({
          title: 'Implement feature X',
          status: 'review',
        });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText('Code')).toBeInTheDocument();
        expect(screen.getByLabelText('Code review')).toBeInTheDocument();
      });

      it('returns help when status is blocked AND notes field is populated', () => {
        const issue = createTestIssue({
          title: 'Task needing help',
          status: 'blocked',
          notes: 'Stuck on database migration issue',
        });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText('Help')).toBeInTheDocument();
        expect(screen.getByLabelText('Help review')).toBeInTheDocument();
      });

      it('returns null when none of the conditions are met', () => {
        const issue = createTestIssue({
          title: 'Regular task',
          status: 'in_progress',
        });
        render(<IssueCard issue={issue} />);

        expect(screen.queryByText('Plan')).not.toBeInTheDocument();
        expect(screen.queryByText('Code')).not.toBeInTheDocument();
        expect(screen.queryByText('Help')).not.toBeInTheDocument();
      });

      it('returns null for blocked status without notes', () => {
        const issue = createTestIssue({
          title: 'Blocked task without notes',
          status: 'blocked',
        });
        render(<IssueCard issue={issue} />);

        expect(screen.queryByText('Help')).not.toBeInTheDocument();
      });

      it('prioritizes plan over code when title has [Need Review] and status is review', () => {
        const issue = createTestIssue({
          title: '[Need Review] Code review request',
          status: 'review',
        });
        render(<IssueCard issue={issue} />);

        // Plan takes priority over Code when [Need Review] is in title
        expect(screen.getByText('Plan')).toBeInTheDocument();
        expect(screen.queryByText('Code')).not.toBeInTheDocument();
      });
    });

    describe('badge rendering', () => {
      it('shows Plan badge with icon for issues with [Need Review] in title', () => {
        const issue = createTestIssue({ title: '[Need Review] Design proposal' });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText('Plan review');
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent('Plan');
        // Check icon is rendered
        expect(screen.getByText('📝')).toBeInTheDocument();
      });

      it('shows Code badge with icon for issues with review status', () => {
        const issue = createTestIssue({
          title: 'Feature implementation',
          status: 'review',
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText('Code review');
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent('Code');
        // Check icon is rendered
        expect(screen.getByText('🔍')).toBeInTheDocument();
      });

      it('shows Help badge with icon for blocked issues with notes', () => {
        const issue = createTestIssue({
          title: 'Needs assistance',
          status: 'blocked',
          notes: 'Need help with API integration',
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText('Help review');
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent('Help');
        // Check icon is rendered
        expect(screen.getByText('❓')).toBeInTheDocument();
      });

      it('does not show badge for regular issues', () => {
        const issue = createTestIssue({
          title: 'Normal task',
          status: 'open',
        });
        render(<IssueCard issue={issue} />);

        expect(screen.queryByLabelText('Plan review')).not.toBeInTheDocument();
        expect(screen.queryByLabelText('Code review')).not.toBeInTheDocument();
        expect(screen.queryByLabelText('Help review')).not.toBeInTheDocument();
      });

      it('badge icon has aria-hidden attribute', () => {
        const issue = createTestIssue({ title: '[Need Review] Feature' });
        render(<IssueCard issue={issue} />);

        const icon = screen.getByText('📝');
        expect(icon).toHaveAttribute('aria-hidden', 'true');
      });

      it('applies reviewPlan class to Plan badge', () => {
        const issue = createTestIssue({ title: '[Need Review] Plan item' });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText('Plan review');
        expect(badge.className).toMatch(/reviewPlan/);
      });

      it('applies reviewCode class to Code badge', () => {
        const issue = createTestIssue({
          title: 'Code item',
          status: 'review',
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText('Code review');
        expect(badge.className).toMatch(/reviewCode/);
      });

      it('applies reviewHelp class to Help badge', () => {
        const issue = createTestIssue({
          title: 'Help item',
          status: 'blocked',
          notes: 'Need assistance',
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText('Help review');
        expect(badge.className).toMatch(/reviewHelp/);
      });
    });

    describe('edge cases', () => {
      it('handles undefined title gracefully', () => {
        // @ts-expect-error Testing undefined title
        const issue = createTestIssue({ title: undefined });
        render(<IssueCard issue={issue} />);

        // Should not show any review badge
        expect(screen.queryByLabelText(/review/)).not.toBeInTheDocument();
      });

      it('handles empty notes field for blocked status', () => {
        const issue = createTestIssue({
          title: 'Blocked issue',
          status: 'blocked',
          notes: '',
        });
        render(<IssueCard issue={issue} />);

        // Empty string notes should not trigger Help badge
        expect(screen.queryByText('Help')).not.toBeInTheDocument();
      });

      it('[Need Review] detection is case sensitive', () => {
        const issue = createTestIssue({ title: '[need review] lowercase' });
        render(<IssueCard issue={issue} />);

        // Should not match because case is different
        expect(screen.queryByText('Plan')).not.toBeInTheDocument();
      });

      it('[Need Review] can be anywhere in title', () => {
        const issue = createTestIssue({ title: 'My feature [Need Review] for approval' });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText('Plan')).toBeInTheDocument();
      });
    });
  });

  describe('CSS module classes', () => {
    it('renders card with issueCard class from CSS module', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector('article');
      // CSS Modules hashes class names, so we check for the pattern
      expect(article?.className).toMatch(/issueCard/);
    });

    it('selected class exists in CSS module styles object', () => {
      // Verify that the .selected class is defined in the CSS module
      // This ensures the CSS module exports the selected class that can be applied
      expect(styles.selected).toBeDefined();
      expect(styles.selected).toBeTruthy();
    });
  });

  describe('approve/reject actions', () => {
    describe('button visibility', () => {
      it('shows approve button when columnId is "review" and onApprove is provided', () => {
        const issue = createTestIssue();
        const onApprove = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onApprove={onApprove} />);

        expect(screen.getByTestId('approve-button')).toBeInTheDocument();
      });

      it('shows reject button when columnId is "review" and onReject is provided', () => {
        const issue = createTestIssue();
        const onReject = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onReject={onReject} />);

        expect(screen.getByTestId('reject-button')).toBeInTheDocument();
      });

      it('shows both buttons when both callbacks are provided', () => {
        const issue = createTestIssue();
        render(
          <IssueCard issue={issue} columnId="review" onApprove={vi.fn()} onReject={vi.fn()} />
        );

        expect(screen.getByTestId('approve-button')).toBeInTheDocument();
        expect(screen.getByTestId('reject-button')).toBeInTheDocument();
      });

      it('does not show buttons when columnId is not "review"', () => {
        const issue = createTestIssue();
        render(
          <IssueCard issue={issue} columnId="in_progress" onApprove={vi.fn()} onReject={vi.fn()} />
        );

        expect(screen.queryByTestId('approve-button')).not.toBeInTheDocument();
        expect(screen.queryByTestId('reject-button')).not.toBeInTheDocument();
      });

      it('does not show buttons when columnId is undefined', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} onApprove={vi.fn()} onReject={vi.fn()} />);

        expect(screen.queryByTestId('approve-button')).not.toBeInTheDocument();
        expect(screen.queryByTestId('reject-button')).not.toBeInTheDocument();
      });

      it('does not show buttons when callbacks are not provided', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" />);

        expect(screen.queryByTestId('approve-button')).not.toBeInTheDocument();
        expect(screen.queryByTestId('reject-button')).not.toBeInTheDocument();
      });

      it('shows only approve button when only onApprove is provided', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" onApprove={vi.fn()} />);

        expect(screen.getByTestId('approve-button')).toBeInTheDocument();
        expect(screen.queryByTestId('reject-button')).not.toBeInTheDocument();
      });

      it('shows only reject button when only onReject is provided', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" onReject={vi.fn()} />);

        expect(screen.queryByTestId('approve-button')).not.toBeInTheDocument();
        expect(screen.getByTestId('reject-button')).toBeInTheDocument();
      });
    });

    describe('approve button', () => {
      it('calls onApprove with issue when clicked', () => {
        const issue = createTestIssue({ id: 'approve-test-123' });
        const onApprove = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onApprove={onApprove} />);

        fireEvent.click(screen.getByTestId('approve-button'));

        expect(onApprove).toHaveBeenCalledWith(issue);
        expect(onApprove).toHaveBeenCalledTimes(1);
      });

      it('does not propagate click to parent', () => {
        const issue = createTestIssue();
        const onClick = vi.fn();
        const onApprove = vi.fn();
        render(
          <IssueCard issue={issue} columnId="review" onClick={onClick} onApprove={onApprove} />
        );

        fireEvent.click(screen.getByTestId('approve-button'));

        expect(onApprove).toHaveBeenCalled();
        expect(onClick).not.toHaveBeenCalled();
      });

      it('has accessible aria-label', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" onApprove={vi.fn()} />);

        expect(screen.getByLabelText('Approve')).toBeInTheDocument();
      });

      it('shows loading state after click', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" onApprove={vi.fn()} />);

        fireEvent.click(screen.getByTestId('approve-button'));

        expect(screen.getByTestId('approve-button')).toHaveTextContent('...');
      });

      it('disables button after click to prevent double submission', () => {
        const issue = createTestIssue();
        const onApprove = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onApprove={onApprove} />);

        fireEvent.click(screen.getByTestId('approve-button'));
        fireEvent.click(screen.getByTestId('approve-button'));

        expect(onApprove).toHaveBeenCalledTimes(1);
      });
    });

    describe('reject button', () => {
      it('shows reject comment form when clicked', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" onReject={vi.fn()} />);

        fireEvent.click(screen.getByTestId('reject-button'));

        expect(screen.getByTestId('reject-comment-form')).toBeInTheDocument();
      });

      it('hides action buttons when reject form is shown', () => {
        const issue = createTestIssue();
        render(
          <IssueCard issue={issue} columnId="review" onApprove={vi.fn()} onReject={vi.fn()} />
        );

        fireEvent.click(screen.getByTestId('reject-button'));

        expect(screen.queryByTestId('approve-button')).not.toBeInTheDocument();
        expect(screen.queryByTestId('reject-button')).not.toBeInTheDocument();
      });

      it('does not propagate click to parent', () => {
        const issue = createTestIssue();
        const onClick = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onClick={onClick} onReject={vi.fn()} />);

        fireEvent.click(screen.getByTestId('reject-button'));

        expect(onClick).not.toHaveBeenCalled();
      });

      it('has accessible aria-label', () => {
        const issue = createTestIssue();
        render(<IssueCard issue={issue} columnId="review" onReject={vi.fn()} />);

        expect(screen.getByLabelText('Reject')).toBeInTheDocument();
      });
    });

    describe('reject comment form interaction', () => {
      it('calls onReject with issue and comment when form is submitted', () => {
        const issue = createTestIssue({ id: 'reject-test-456' });
        const onReject = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onReject={onReject} />);

        // Show reject form
        fireEvent.click(screen.getByTestId('reject-button'));

        // Enter comment and submit
        const textarea = screen.getByTestId('reject-textarea');
        fireEvent.change(textarea, { target: { value: 'needs more work' } });
        fireEvent.click(screen.getByTestId('reject-submit'));

        expect(onReject).toHaveBeenCalledWith(issue, 'needs more work');
        expect(onReject).toHaveBeenCalledTimes(1);
      });

      it('hides reject form and shows buttons when cancel is clicked', () => {
        const issue = createTestIssue();
        render(
          <IssueCard issue={issue} columnId="review" onApprove={vi.fn()} onReject={vi.fn()} />
        );

        // Show reject form
        fireEvent.click(screen.getByTestId('reject-button'));
        expect(screen.getByTestId('reject-comment-form')).toBeInTheDocument();

        // Click cancel
        fireEvent.click(screen.getByTestId('reject-cancel'));

        // Form should be hidden, buttons should be visible again
        expect(screen.queryByTestId('reject-comment-form')).not.toBeInTheDocument();
        expect(screen.getByTestId('approve-button')).toBeInTheDocument();
        expect(screen.getByTestId('reject-button')).toBeInTheDocument();
      });

      it('passes issue ID to reject form for accessibility', () => {
        const issue = createTestIssue({ id: 'issue-for-form' });
        render(<IssueCard issue={issue} columnId="review" onReject={vi.fn()} />);

        fireEvent.click(screen.getByTestId('reject-button'));

        // Check that form has aria-label with issue ID
        expect(
          screen.getByLabelText('Rejection feedback for issue issue-for-form')
        ).toBeInTheDocument();
      });

      it('prevents double submission when rejecting', () => {
        const issue = createTestIssue();
        const onReject = vi.fn();
        render(<IssueCard issue={issue} columnId="review" onReject={onReject} />);

        // Show reject form
        fireEvent.click(screen.getByTestId('reject-button'));

        // Enter comment and submit twice
        const textarea = screen.getByTestId('reject-textarea');
        fireEvent.change(textarea, { target: { value: 'feedback' } });
        fireEvent.click(screen.getByTestId('reject-submit'));
        fireEvent.click(screen.getByTestId('reject-submit'));

        // onReject should only be called once due to isRejecting state
        expect(onReject).toHaveBeenCalledTimes(1);
      });
    });

    describe('column-specific behavior', () => {
      it.each(['open', 'in_progress', 'blocked', 'done', 'backlog'])(
        'does not show action buttons for columnId="%s"',
        (columnId) => {
          const issue = createTestIssue();
          render(
            <IssueCard issue={issue} columnId={columnId} onApprove={vi.fn()} onReject={vi.fn()} />
          );

          expect(screen.queryByTestId('approve-button')).not.toBeInTheDocument();
          expect(screen.queryByTestId('reject-button')).not.toBeInTheDocument();
        }
      );
    });
  });
});
