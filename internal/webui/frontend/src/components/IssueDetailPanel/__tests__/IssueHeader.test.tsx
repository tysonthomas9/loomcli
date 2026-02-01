/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueHeader component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { IssueHeader } from '../IssueHeader';
import type { Issue } from '@/types';

const mockIssue: Issue = {
  id: 'test-123',
  title: 'Test Issue Title',
  status: 'in_progress',
  priority: 2,
  created_at: '2026-01-23T00:00:00Z',
  updated_at: '2026-01-23T00:00:00Z',
};

describe('IssueHeader', () => {
  it('renders issue ID', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByTestId('issue-id')).toHaveTextContent('test-123');
  });

  it('renders issue title', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByTestId('issue-title')).toHaveTextContent('Test Issue Title');
  });

  it('renders status badge with formatted text', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    const badge = screen.getByTestId('issue-status-badge');
    expect(badge).toHaveTextContent('In Progress');
    expect(badge).toHaveAttribute('data-status', 'in_progress');
  });

  it('renders status badge with role="status"', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('defaults to "Open" when status is undefined', () => {
    const issueNoStatus = { ...mockIssue, status: undefined };
    render(<IssueHeader issue={issueNoStatus} onClose={() => {}} />);
    expect(screen.getByTestId('issue-status-badge')).toHaveTextContent('Open');
  });

  it('calls onClose when close button clicked', () => {
    const onClose = vi.fn();
    render(<IssueHeader issue={mockIssue} onClose={onClose} />);
    fireEvent.click(screen.getByTestId('header-close-button'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('close button has accessible label', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByLabelText('Close panel')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} className="custom" />);
    expect(screen.getByTestId('issue-header')).toHaveClass('custom');
  });

  it('renders open status with correct data attribute', () => {
    const openIssue = { ...mockIssue, status: 'open' };
    render(<IssueHeader issue={openIssue} onClose={() => {}} />);
    const badge = screen.getByTestId('issue-status-badge');
    expect(badge).toHaveTextContent('Open');
    expect(badge).toHaveAttribute('data-status', 'open');
  });

  it('renders closed status with correct data attribute', () => {
    const closedIssue = { ...mockIssue, status: 'closed' };
    render(<IssueHeader issue={closedIssue} onClose={() => {}} />);
    const badge = screen.getByTestId('issue-status-badge');
    expect(badge).toHaveTextContent('Closed');
    expect(badge).toHaveAttribute('data-status', 'closed');
  });

  it('renders blocked status with correct data attribute', () => {
    const blockedIssue = { ...mockIssue, status: 'blocked' };
    render(<IssueHeader issue={blockedIssue} onClose={() => {}} />);
    const badge = screen.getByTestId('issue-status-badge');
    expect(badge).toHaveTextContent('Blocked');
    expect(badge).toHaveAttribute('data-status', 'blocked');
  });

  describe('priority badge', () => {
    it('shows priority badge when showPriority is true', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} showPriority={true} />);
      expect(screen.getByTestId('header-priority-badge')).toBeInTheDocument();
    });

    it('does not show priority badge when showPriority is false', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} showPriority={false} />);
      expect(screen.queryByTestId('header-priority-badge')).not.toBeInTheDocument();
    });

    it('does not show priority badge when showPriority is not provided', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('header-priority-badge')).not.toBeInTheDocument();
    });

    it('displays correct priority label for P0', () => {
      const p0Issue = { ...mockIssue, priority: 0 };
      render(<IssueHeader issue={p0Issue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveTextContent('P0');
      expect(badge).toHaveAttribute('data-priority', '0');
    });

    it('displays correct priority label for P1', () => {
      const p1Issue = { ...mockIssue, priority: 1 };
      render(<IssueHeader issue={p1Issue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveTextContent('P1');
      expect(badge).toHaveAttribute('data-priority', '1');
    });

    it('displays correct priority label for P2', () => {
      const p2Issue = { ...mockIssue, priority: 2 };
      render(<IssueHeader issue={p2Issue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveTextContent('P2');
      expect(badge).toHaveAttribute('data-priority', '2');
    });

    it('displays correct priority label for P3', () => {
      const p3Issue = { ...mockIssue, priority: 3 };
      render(<IssueHeader issue={p3Issue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveTextContent('P3');
      expect(badge).toHaveAttribute('data-priority', '3');
    });

    it('displays correct priority label for P4', () => {
      const p4Issue = { ...mockIssue, priority: 4 };
      render(<IssueHeader issue={p4Issue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveTextContent('P4');
      expect(badge).toHaveAttribute('data-priority', '4');
    });

    it('has accessible aria-label with full priority description', () => {
      const p0Issue = { ...mockIssue, priority: 0 };
      render(<IssueHeader issue={p0Issue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveAttribute('aria-label', 'Priority: P0 - Critical');
    });

    it('calls onPriorityClick when priority badge is clicked', () => {
      const onPriorityClick = vi.fn();
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          showPriority={true}
          onPriorityClick={onPriorityClick}
        />
      );
      fireEvent.click(screen.getByTestId('header-priority-badge'));
      expect(onPriorityClick).toHaveBeenCalledTimes(1);
    });

    it('defaults to P2 for unknown priority values', () => {
      const unknownPriorityIssue = { ...mockIssue, priority: 99 };
      render(<IssueHeader issue={unknownPriorityIssue} onClose={() => {}} showPriority={true} />);
      const badge = screen.getByTestId('header-priority-badge');
      expect(badge).toHaveTextContent('P2');
    });
  });

  describe('sticky mode', () => {
    it('applies sticky class when sticky prop is true', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} sticky={true} />);
      const header = screen.getByTestId('issue-header');
      // CSS modules mangle class names, so check for pattern containing 'sticky'
      expect(header.className).toMatch(/sticky/i);
    });

    it('does not apply sticky class when sticky prop is false', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} sticky={false} />);
      const header = screen.getByTestId('issue-header');
      expect(header.className).not.toMatch(/_sticky_/);
    });

    it('does not apply sticky class when sticky prop is not provided', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
      const header = screen.getByTestId('issue-header');
      expect(header.className).not.toMatch(/_sticky_/);
    });
  });

  describe('fullscreen button', () => {
    it('renders fullscreen button when onToggleFullscreen is provided', () => {
      render(
        <IssueHeader issue={mockIssue} onClose={() => {}} onToggleFullscreen={() => {}} />
      );
      expect(screen.getByTestId('header-fullscreen-button')).toBeInTheDocument();
    });

    it('does not render fullscreen button when onToggleFullscreen is not provided', () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('header-fullscreen-button')).not.toBeInTheDocument();
    });

    it('calls onToggleFullscreen when clicked', () => {
      const onToggleFullscreen = vi.fn();
      render(
        <IssueHeader issue={mockIssue} onClose={() => {}} onToggleFullscreen={onToggleFullscreen} />
      );
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));
      expect(onToggleFullscreen).toHaveBeenCalledTimes(1);
    });

    it('shows expand icon when not fullscreen', () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          onToggleFullscreen={() => {}}
          isFullscreen={false}
        />
      );
      const button = screen.getByTestId('header-fullscreen-button');
      expect(button).toHaveAttribute('aria-label', 'Expand to fullscreen');
    });

    it('shows collapse icon when fullscreen', () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          onToggleFullscreen={() => {}}
          isFullscreen={true}
        />
      );
      const button = screen.getByTestId('header-fullscreen-button');
      expect(button).toHaveAttribute('aria-label', 'Collapse to panel');
    });

    it('has correct aria-label based on fullscreen state', () => {
      const { rerender } = render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          onToggleFullscreen={() => {}}
          isFullscreen={false}
        />
      );
      expect(screen.getByTestId('header-fullscreen-button')).toHaveAttribute(
        'aria-label',
        'Expand to fullscreen'
      );

      rerender(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          onToggleFullscreen={() => {}}
          isFullscreen={true}
        />
      );
      expect(screen.getByTestId('header-fullscreen-button')).toHaveAttribute(
        'aria-label',
        'Collapse to panel'
      );
    });
  });

  describe('review action buttons', () => {
    describe('visibility', () => {
      it('renders approve and reject buttons when isReviewItem=true and callbacks provided', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
            onReject={vi.fn()}
          />
        );

        expect(screen.getByTestId('header-review-actions')).toBeInTheDocument();
        expect(screen.getByTestId('header-approve-button')).toBeInTheDocument();
        expect(screen.getByTestId('header-reject-button')).toBeInTheDocument();
      });

      it('does not render review actions when isReviewItem=false', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={false}
            onApprove={vi.fn()}
            onReject={vi.fn()}
          />
        );

        expect(screen.queryByTestId('header-review-actions')).not.toBeInTheDocument();
      });

      it('does not render review actions when isReviewItem is undefined', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            onApprove={vi.fn()}
            onReject={vi.fn()}
          />
        );

        expect(screen.queryByTestId('header-review-actions')).not.toBeInTheDocument();
      });

      it('does not render review actions when no callbacks are provided', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
          />
        );

        expect(screen.queryByTestId('header-review-actions')).not.toBeInTheDocument();
      });

      it('renders only approve button when only onApprove is provided', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
          />
        );

        expect(screen.getByTestId('header-review-actions')).toBeInTheDocument();
        expect(screen.getByTestId('header-approve-button')).toBeInTheDocument();
        expect(screen.queryByTestId('header-reject-button')).not.toBeInTheDocument();
      });

      it('renders only reject button when only onReject is provided', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onReject={vi.fn()}
          />
        );

        expect(screen.getByTestId('header-review-actions')).toBeInTheDocument();
        expect(screen.queryByTestId('header-approve-button')).not.toBeInTheDocument();
        expect(screen.getByTestId('header-reject-button')).toBeInTheDocument();
      });
    });

    describe('approve button', () => {
      it('calls onApprove when clicked', () => {
        const onApprove = vi.fn();
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={onApprove}
          />
        );

        fireEvent.click(screen.getByTestId('header-approve-button'));

        expect(onApprove).toHaveBeenCalledTimes(1);
      });

      it('shows checkmark by default', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
          />
        );

        expect(screen.getByTestId('header-approve-button')).toHaveTextContent('\u2713');
      });

      it('shows "..." when isApproving=true', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
            isApproving={true}
          />
        );

        expect(screen.getByTestId('header-approve-button')).toHaveTextContent('...');
      });

      it('is disabled when isApproving=true', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
            isApproving={true}
          />
        );

        expect(screen.getByTestId('header-approve-button')).toBeDisabled();
      });

      it('is not disabled when isApproving=false', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
            isApproving={false}
          />
        );

        expect(screen.getByTestId('header-approve-button')).not.toBeDisabled();
      });

      it('has accessible aria-label "Approve"', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onApprove={vi.fn()}
          />
        );

        expect(screen.getByLabelText('Approve')).toBeInTheDocument();
      });
    });

    describe('reject button', () => {
      it('calls onReject when clicked', () => {
        const onReject = vi.fn();
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onReject={onReject}
          />
        );

        fireEvent.click(screen.getByTestId('header-reject-button'));

        expect(onReject).toHaveBeenCalledTimes(1);
      });

      it('shows X mark', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onReject={vi.fn()}
          />
        );

        const button = screen.getByTestId('header-reject-button');
        expect(button.textContent?.trim()).toBe('\u2717');
      });

      it('has accessible aria-label "Reject"', () => {
        render(
          <IssueHeader
            issue={mockIssue}
            onClose={() => {}}
            isReviewItem={true}
            onReject={vi.fn()}
          />
        );

        expect(screen.getByLabelText('Reject')).toBeInTheDocument();
      });
    });
  });
});
