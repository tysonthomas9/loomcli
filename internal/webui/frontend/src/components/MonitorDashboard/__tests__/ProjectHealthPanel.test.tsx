/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ProjectHealthPanel component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { ProjectHealthPanel } from '../ProjectHealthPanel';
import type { LoomStats, BlockedIssue, Priority } from '@/types';

/**
 * Create default stats for testing.
 */
function createStats(overrides: Partial<LoomStats> = {}): LoomStats {
  return {
    open: 10,
    closed: 5,
    total: 15,
    completion: 33.3,
    ...overrides,
  };
}

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

describe('ProjectHealthPanel', () => {
  describe('completion progress bar', () => {
    it('renders progress bar with correct percentage', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 75.5 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      const progressBar = screen.getByRole('progressbar', { name: /project completion/i });
      expect(progressBar).toBeInTheDocument();
      expect(progressBar).toHaveAttribute('aria-valuenow', '76'); // Rounded
      expect(progressBar).toHaveAttribute('aria-valuemin', '0');
      expect(progressBar).toHaveAttribute('aria-valuemax', '100');
    });

    it('displays completion percentage as text', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 42.7 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('43%')).toBeInTheDocument();
    });

    it('renders progress bar fill with correct width', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 50 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveStyle({ width: '50%' });
    });

    it('handles 0% completion', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 0 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('0%')).toBeInTheDocument();
      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '0');
    });

    it('handles 100% completion', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 100 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('100%')).toBeInTheDocument();
      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '100');
    });
  });

  describe('issue counts', () => {
    it('renders open issue count', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ open: 25 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('25')).toBeInTheDocument();
      expect(screen.getByText('Open')).toBeInTheDocument();
    });

    it('renders closed issue count', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ closed: 12 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('12')).toBeInTheDocument();
      expect(screen.getByText('Closed')).toBeInTheDocument();
    });

    it('renders total issue count', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ total: 37 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('37')).toBeInTheDocument();
      expect(screen.getByText('Total')).toBeInTheDocument();
    });

    it('displays all issue counts correctly', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ open: 8, closed: 4, total: 12 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('8')).toBeInTheDocument();
      expect(screen.getByText('4')).toBeInTheDocument();
      expect(screen.getByText('12')).toBeInTheDocument();
    });
  });

  describe('bottleneck detection', () => {
    it('detects bottlenecks from issues blocking multiple others', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1', 'other-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-3', blocked_by: ['bottleneck-1', 'bottleneck-2'] }),
        createBlockedIssue({ id: 'blocked-4', blocked_by: ['bottleneck-2'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      // bottleneck-1 blocks 3 issues, bottleneck-2 blocks 2 issues
      expect(screen.getByText('bottleneck-1')).toBeInTheDocument();
      expect(screen.getByText('blocks 3')).toBeInTheDocument();
      expect(screen.getByText('bottleneck-2')).toBeInTheDocument();
      expect(screen.getByText('blocks 2')).toBeInTheDocument();
    });

    it('shows bottleneck count in section header', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      expect(screen.getByText('(1)')).toBeInTheDocument();
    });

    it('sorts bottlenecks by blocking count descending', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['small-blocker'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['small-blocker', 'big-blocker'] }),
        createBlockedIssue({ id: 'blocked-3', blocked_by: ['big-blocker'] }),
        createBlockedIssue({ id: 'blocked-4', blocked_by: ['big-blocker'] }),
        createBlockedIssue({ id: 'blocked-5', blocked_by: ['big-blocker'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      const listItems = screen.getAllByRole('listitem');
      // big-blocker (blocks 4) should come before small-blocker (blocks 2)
      expect(listItems[0]).toHaveTextContent('big-blocker');
      expect(listItems[1]).toHaveTextContent('small-blocker');
    });

    it('limits bottlenecks to top 5', () => {
      // Create 6 different bottlenecks that each block 2+ issues
      const blockedIssues = [
        createBlockedIssue({
          id: 'b1',
          blocked_by: ['bn-1', 'bn-2', 'bn-3', 'bn-4', 'bn-5', 'bn-6'],
        }),
        createBlockedIssue({
          id: 'b2',
          blocked_by: ['bn-1', 'bn-2', 'bn-3', 'bn-4', 'bn-5', 'bn-6'],
        }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      const listItems = screen.getAllByRole('listitem');
      expect(listItems).toHaveLength(5);
    });
  });

  describe('empty bottleneck state', () => {
    it('shows "No bottlenecks detected" when there are no blocked issues', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={[]} isLoading={false} />);

      expect(screen.getByText('No bottlenecks detected')).toBeInTheDocument();
    });

    it('shows "No bottlenecks detected" when blockedIssues is null', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={null} isLoading={false} />);

      expect(screen.getByText('No bottlenecks detected')).toBeInTheDocument();
    });

    it('shows "No bottlenecks detected" when no issue blocks multiple others', () => {
      // Each blocker only blocks one issue
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['blocker-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['blocker-2'] }),
        createBlockedIssue({ id: 'blocked-3', blocked_by: ['blocker-3'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      expect(screen.getByText('No bottlenecks detected')).toBeInTheDocument();
    });

    it('does not show bottleneck count when no bottlenecks exist', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={[]} isLoading={false} />);

      expect(screen.queryByText(/\(\d+\)/)).not.toBeInTheDocument();
    });
  });

  describe('bottleneck click handling', () => {
    it('calls onBottleneckClick with correct issue when bottleneck is clicked', () => {
      const onBottleneckClick = vi.fn();
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
      ];

      render(
        <ProjectHealthPanel
          stats={createStats()}
          blockedIssues={blockedIssues}
          isLoading={false}
          onBottleneckClick={onBottleneckClick}
        />
      );

      const bottleneckButton = screen.getByRole('button', { name: /bottleneck-1/i });
      fireEvent.click(bottleneckButton);

      expect(onBottleneckClick).toHaveBeenCalledTimes(1);
      expect(onBottleneckClick).toHaveBeenCalledWith({
        id: 'bottleneck-1',
        title: 'bottleneck-1', // Falls back to ID when title not available
      });
    });

    it('does not throw when clicking bottleneck without onBottleneckClick', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      const bottleneckButton = screen.getByRole('button', { name: /bottleneck-1/i });
      expect(() => fireEvent.click(bottleneckButton)).not.toThrow();
    });

    it('disables bottleneck buttons when onBottleneckClick is not provided', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      const bottleneckButton = screen.getByRole('button', { name: /bottleneck-1/i });
      expect(bottleneckButton).toBeDisabled();
    });

    it('enables bottleneck buttons when onBottleneckClick is provided', () => {
      const onBottleneckClick = vi.fn();
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
      ];

      render(
        <ProjectHealthPanel
          stats={createStats()}
          blockedIssues={blockedIssues}
          isLoading={false}
          onBottleneckClick={onBottleneckClick}
        />
      );

      const bottleneckButton = screen.getByRole('button', { name: /bottleneck-1/i });
      expect(bottleneckButton).not.toBeDisabled();
    });
  });

  describe('loading state', () => {
    it('shows loading indicator in bottlenecks section when isLoading is true', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={null} isLoading={true} />);

      expect(screen.getByText('Loading...')).toBeInTheDocument();
    });

    it('does not show "No bottlenecks detected" while loading', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={null} isLoading={true} />);

      expect(screen.queryByText('No bottlenecks detected')).not.toBeInTheDocument();
    });

    it('still shows stats while loading', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ open: 15, closed: 5, total: 20, completion: 25 })}
          blockedIssues={null}
          isLoading={true}
        />
      );

      expect(screen.getByText('15')).toBeInTheDocument();
      expect(screen.getByText('5')).toBeInTheDocument();
      expect(screen.getByText('20')).toBeInTheDocument();
      expect(screen.getByText('25%')).toBeInTheDocument();
    });
  });

  describe('custom className', () => {
    it('applies custom className to root element', () => {
      render(
        <ProjectHealthPanel
          stats={createStats()}
          blockedIssues={[]}
          isLoading={false}
          className="custom-class"
        />
      );

      const panel = screen.getByTestId('project-health-panel');
      expect(panel).toHaveClass('custom-class');
    });

    it('preserves base styles when custom className is applied', () => {
      const { container } = render(
        <ProjectHealthPanel
          stats={createStats()}
          blockedIssues={[]}
          isLoading={false}
          className="custom-class"
        />
      );

      const panel = container.firstChild as HTMLElement;
      // Should have both the module CSS class and custom class
      expect(panel.classList.length).toBeGreaterThan(1);
    });
  });

  describe('edge cases', () => {
    it('handles zero issues correctly', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ open: 0, closed: 0, total: 0, completion: 0 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      // Should show 0 for all counts
      const zeros = screen.getAllByText('0');
      expect(zeros.length).toBeGreaterThanOrEqual(3); // open, closed, total
      expect(screen.getByText('0%')).toBeInTheDocument();
    });

    it('handles single blocker (not a bottleneck) correctly', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['single-blocker'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      // Single blocker should not be shown as bottleneck
      expect(screen.queryByText('single-blocker')).not.toBeInTheDocument();
      expect(screen.getByText('No bottlenecks detected')).toBeInTheDocument();
    });

    it('handles issue blocked by multiple blockers where only one is a bottleneck', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck', 'single-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck', 'single-2'] }),
      ];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      // Only 'bottleneck' should appear (blocks 2 issues)
      expect(screen.getByText('bottleneck')).toBeInTheDocument();
      expect(screen.queryByText('single-1')).not.toBeInTheDocument();
      expect(screen.queryByText('single-2')).not.toBeInTheDocument();
    });

    it('handles empty blocked_by array', () => {
      const blockedIssues = [createBlockedIssue({ id: 'blocked-1', blocked_by: [] })];

      render(
        <ProjectHealthPanel stats={createStats()} blockedIssues={blockedIssues} isLoading={false} />
      );

      expect(screen.getByText('No bottlenecks detected')).toBeInTheDocument();
    });

    it('handles completion values that need rounding', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 33.33333 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('33%')).toBeInTheDocument();
    });

    it('handles large numbers correctly', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ open: 9999, closed: 8888, total: 18887 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      expect(screen.getByText('9999')).toBeInTheDocument();
      expect(screen.getByText('8888')).toBeInTheDocument();
      expect(screen.getByText('18887')).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has accessible progress bar', () => {
      render(
        <ProjectHealthPanel
          stats={createStats({ completion: 50 })}
          blockedIssues={[]}
          isLoading={false}
        />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-label', 'Project completion');
      expect(progressBar).toHaveAttribute('aria-valuenow', '50');
      expect(progressBar).toHaveAttribute('aria-valuemin', '0');
      expect(progressBar).toHaveAttribute('aria-valuemax', '100');
    });

    it('has data-testid for e2e tests', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={[]} isLoading={false} />);

      expect(screen.getByTestId('project-health-panel')).toBeInTheDocument();
    });

    it('bottleneck buttons have accessible type attribute', () => {
      const blockedIssues = [
        createBlockedIssue({ id: 'blocked-1', blocked_by: ['bottleneck-1'] }),
        createBlockedIssue({ id: 'blocked-2', blocked_by: ['bottleneck-1'] }),
      ];

      render(
        <ProjectHealthPanel
          stats={createStats()}
          blockedIssues={blockedIssues}
          isLoading={false}
          onBottleneckClick={vi.fn()}
        />
      );

      const button = screen.getByRole('button', { name: /bottleneck-1/i });
      expect(button).toHaveAttribute('type', 'button');
    });
  });

  describe('section headings', () => {
    it('renders Bottlenecks heading', () => {
      render(<ProjectHealthPanel stats={createStats()} blockedIssues={[]} isLoading={false} />);

      expect(screen.getByText('Bottlenecks')).toBeInTheDocument();
    });

    it('highlights the top bottleneck with distinct styling', () => {
      const blockedIssues = [
        { id: 'issue-1', title: 'Issue 1', blocked_by: ['blocker-a', 'blocker-b'] },
        { id: 'issue-2', title: 'Issue 2', blocked_by: ['blocker-a', 'blocker-b'] },
        { id: 'issue-3', title: 'Issue 3', blocked_by: ['blocker-a'] },
      ];

      render(
        <ProjectHealthPanel
          stats={createStats()}
          blockedIssues={blockedIssues}
          isLoading={false}
          onBottleneckClick={() => {}}
        />
      );

      const buttons = screen.getAllByRole('button');
      // First bottleneck button should have highlighted class
      expect(buttons[0].className).toContain('bottleneckButtonHighlighted');
      // Second bottleneck button should have normal class
      expect(buttons[1].className).toContain('bottleneckButton');
      expect(buttons[1].className).not.toContain('Highlighted');
    });
  });
});
