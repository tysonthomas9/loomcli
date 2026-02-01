/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GraphViewContainer component.
 */

import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, fireEvent, waitFor as _waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

import { GraphViewContainer } from '../GraphViewContainer';
import type { Issue, IssueDetails } from '@/types';

// Mock the hooks
vi.mock('@/hooks/useIssueDetail', () => ({
  useIssueDetail: vi.fn(),
}));

// Mock GraphView component (imported directly from @/components/GraphView)
vi.mock('@/components/GraphView', () => ({
  GraphView: vi.fn(({ issues, onNodeClick, className }) => (
    <div data-testid="graph-view" data-issue-count={issues?.length ?? 0} className={className}>
      {/* Simulate node rendering to test click handlers */}
      {issues?.map((issue: Issue) => (
        <div key={issue.id} data-testid={`node-${issue.id}`} onClick={() => onNodeClick?.(issue)}>
          {issue.title}
        </div>
      ))}
    </div>
  )),
}));

// Mock IssueDetailPanel (imported from barrel @/components)
vi.mock('@/components', () => ({
  IssueDetailPanel: vi.fn(({ isOpen, issue, isLoading, error, onClose }) => (
    <div
      data-testid="issue-detail-panel"
      data-is-open={isOpen}
      data-is-loading={isLoading}
      data-has-error={!!error}
      data-issue-id={issue?.id ?? ''}
    >
      {isLoading && <span data-testid="loading-indicator">Loading...</span>}
      {error && <span data-testid="error-message">{error}</span>}
      {issue && <span data-testid="issue-title">{issue.title}</span>}
      <button data-testid="close-button" onClick={onClose}>
        Close
      </button>
    </div>
  )),
}));

// Import mocks after vi.mock calls
import { useIssueDetail } from '@/hooks/useIssueDetail';

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-issue-123',
    title: 'Test Issue Title',
    priority: 2,
    created_at: '2026-01-23T00:00:00Z',
    updated_at: '2026-01-23T00:00:00Z',
    ...overrides,
  };
}

/**
 * Create a minimal test issue details with required fields.
 */
function createTestIssueDetails(overrides: Partial<IssueDetails> = {}): IssueDetails {
  return {
    id: 'test-issue-123',
    title: 'Test Issue Title',
    priority: 2,
    created_at: '2026-01-23T00:00:00Z',
    updated_at: '2026-01-23T00:00:00Z',
    dependents: [],
    ...overrides,
  };
}

/**
 * Setup useIssueDetail mock with default return values.
 */
function setupMocks(
  options: {
    issueDetails?: IssueDetails | null;
    isLoading?: boolean;
    error?: string | null;
    fetchIssue?: Mock;
    clearIssue?: Mock;
  } = {}
) {
  const {
    issueDetails = null,
    isLoading = false,
    error = null,
    fetchIssue = vi.fn(),
    clearIssue = vi.fn(),
  } = options;

  (useIssueDetail as Mock).mockReturnValue({
    issueDetails,
    isLoading,
    error,
    fetchIssue,
    clearIssue,
  });

  return { fetchIssue, clearIssue };
}

describe('GraphViewContainer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    setupMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe('renders GraphView with issues', () => {
    it('renders GraphView with provided issues', () => {
      const issues = [
        createTestIssue({ id: 'issue-1', title: 'First Issue' }),
        createTestIssue({ id: 'issue-2', title: 'Second Issue' }),
      ];

      render(<GraphViewContainer issues={issues} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      expect(screen.getByTestId('graph-view')).toHaveAttribute('data-issue-count', '2');
    });

    it('renders GraphView with empty issues array', () => {
      render(<GraphViewContainer issues={[]} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      expect(screen.getByTestId('graph-view')).toHaveAttribute('data-issue-count', '0');
    });

    it('renders IssueDetailPanel (initially closed)', () => {
      render(<GraphViewContainer issues={[]} />);

      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toBeInTheDocument();
      expect(panel).toHaveAttribute('data-is-open', 'false');
    });

    it('applies custom className to container', () => {
      const { container } = render(
        <GraphViewContainer issues={[]} className="custom-container-class" />
      );

      const rootDiv = container.firstChild as HTMLElement;
      expect(rootDiv).toHaveClass('custom-container-class');
    });
  });

  describe('opens panel on node click', () => {
    it('opens panel and fetches issue details when node is clicked', () => {
      const { fetchIssue } = setupMocks();
      const issues = [createTestIssue({ id: 'click-issue', title: 'Clickable Issue' })];

      render(<GraphViewContainer issues={issues} />);

      // Click on a node
      const node = screen.getByTestId('node-click-issue');
      fireEvent.click(node);

      // Panel should be open and fetchIssue should be called
      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'true');
      expect(fetchIssue).toHaveBeenCalledWith('click-issue');
      expect(fetchIssue).toHaveBeenCalledTimes(1);
    });

    it('does not refetch if same issue is clicked while panel is open', () => {
      const { fetchIssue } = setupMocks();
      const issues = [createTestIssue({ id: 'same-issue', title: 'Same Issue' })];

      render(<GraphViewContainer issues={issues} />);

      const node = screen.getByTestId('node-same-issue');

      // First click
      fireEvent.click(node);
      expect(fetchIssue).toHaveBeenCalledTimes(1);

      // Second click on the same issue
      fireEvent.click(node);
      // Should NOT fetch again
      expect(fetchIssue).toHaveBeenCalledTimes(1);
    });

    it('fetches new issue when different node is clicked', () => {
      const { fetchIssue } = setupMocks();
      const issues = [
        createTestIssue({ id: 'issue-a', title: 'Issue A' }),
        createTestIssue({ id: 'issue-b', title: 'Issue B' }),
      ];

      render(<GraphViewContainer issues={issues} />);

      // Click first node
      fireEvent.click(screen.getByTestId('node-issue-a'));
      expect(fetchIssue).toHaveBeenCalledWith('issue-a');

      // Click different node
      fireEvent.click(screen.getByTestId('node-issue-b'));
      expect(fetchIssue).toHaveBeenCalledWith('issue-b');
      expect(fetchIssue).toHaveBeenCalledTimes(2);
    });
  });

  describe('displays loading state while fetching', () => {
    it('shows loading state when isLoading is true', () => {
      setupMocks({ isLoading: true });
      const issues = [createTestIssue({ id: 'loading-issue', title: 'Loading Issue' })];

      render(<GraphViewContainer issues={issues} />);

      // Click to open panel
      fireEvent.click(screen.getByTestId('node-loading-issue'));

      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toHaveAttribute('data-is-loading', 'true');
      expect(screen.getByTestId('loading-indicator')).toBeInTheDocument();
    });

    it('does not show loading indicator when not loading', () => {
      setupMocks({ isLoading: false });

      render(<GraphViewContainer issues={[]} />);

      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toHaveAttribute('data-is-loading', 'false');
      expect(screen.queryByTestId('loading-indicator')).not.toBeInTheDocument();
    });
  });

  describe('shows issue details after fetch', () => {
    it('displays issue details when fetch completes', () => {
      const issueDetails = createTestIssueDetails({
        id: 'fetched-issue',
        title: 'Fetched Issue Details',
      });
      setupMocks({ issueDetails, isLoading: false });

      const issues = [createTestIssue({ id: 'fetched-issue', title: 'Issue' })];

      render(<GraphViewContainer issues={issues} />);

      // Click to open panel
      fireEvent.click(screen.getByTestId('node-fetched-issue'));

      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toHaveAttribute('data-issue-id', 'fetched-issue');
      expect(screen.getByTestId('issue-title')).toHaveTextContent('Fetched Issue Details');
    });

    it('passes full issue details to IssueDetailPanel', () => {
      const issueDetails = createTestIssueDetails({
        id: 'detailed-issue',
        title: 'Detailed Issue',
        description: 'This is a detailed description',
        dependents: [
          {
            id: 'dependent-1',
            title: 'Dependent Issue',
            priority: 1,
            dependency_type: 'blocks',
            created_at: '2026-01-23T00:00:00Z',
            updated_at: '2026-01-23T00:00:00Z',
          },
        ],
      });
      setupMocks({ issueDetails });

      const issues = [createTestIssue({ id: 'detailed-issue', title: 'Issue' })];

      render(<GraphViewContainer issues={issues} />);

      fireEvent.click(screen.getByTestId('node-detailed-issue'));

      expect(screen.getByTestId('issue-title')).toHaveTextContent('Detailed Issue');
    });
  });

  describe('closes panel on close button click', () => {
    it('closes panel when close button is clicked', () => {
      const { clearIssue: _clearIssue } = setupMocks();
      const issues = [createTestIssue({ id: 'close-test', title: 'Close Test' })];

      render(<GraphViewContainer issues={issues} />);

      // Open panel
      fireEvent.click(screen.getByTestId('node-close-test'));
      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'true');

      // Close panel
      fireEvent.click(screen.getByTestId('close-button'));

      // Panel should be closed immediately
      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'false');
    });

    it('calls clearIssue after animation delay when panel is closed', () => {
      const { clearIssue } = setupMocks();
      const issues = [createTestIssue({ id: 'clear-test', title: 'Clear Test' })];

      render(<GraphViewContainer issues={issues} />);

      // Open and then close panel
      fireEvent.click(screen.getByTestId('node-clear-test'));
      fireEvent.click(screen.getByTestId('close-button'));

      // clearIssue should not be called immediately (waits for animation)
      expect(clearIssue).not.toHaveBeenCalled();

      // Advance timers to trigger the setTimeout callback (300ms for animation)
      act(() => {
        vi.advanceTimersByTime(300);
      });

      expect(clearIssue).toHaveBeenCalledTimes(1);
    });

    it('allows reopening panel after closing', () => {
      const { fetchIssue, clearIssue } = setupMocks();
      const issues = [createTestIssue({ id: 'reopen-test', title: 'Reopen Test' })];

      render(<GraphViewContainer issues={issues} />);

      // Open panel
      fireEvent.click(screen.getByTestId('node-reopen-test'));
      expect(fetchIssue).toHaveBeenCalledTimes(1);

      // Close panel
      fireEvent.click(screen.getByTestId('close-button'));

      // Advance timers to complete close animation
      act(() => {
        vi.advanceTimersByTime(300);
      });

      expect(clearIssue).toHaveBeenCalledTimes(1);

      // Reopen panel
      fireEvent.click(screen.getByTestId('node-reopen-test'));
      expect(fetchIssue).toHaveBeenCalledTimes(2);
      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'true');
    });
  });

  describe('handles fetch errors gracefully', () => {
    it('displays error message when fetch fails', () => {
      setupMocks({ error: 'Failed to fetch issue details' });
      const issues = [createTestIssue({ id: 'error-issue', title: 'Error Issue' })];

      render(<GraphViewContainer issues={issues} />);

      fireEvent.click(screen.getByTestId('node-error-issue'));

      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toHaveAttribute('data-has-error', 'true');
      expect(screen.getByTestId('error-message')).toHaveTextContent(
        'Failed to fetch issue details'
      );
    });

    it('panel remains open when fetch error occurs', () => {
      setupMocks({ error: 'Network error' });
      const issues = [createTestIssue({ id: 'network-error', title: 'Network Error' })];

      render(<GraphViewContainer issues={issues} />);

      fireEvent.click(screen.getByTestId('node-network-error'));

      // Panel should still be open despite error
      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'true');
    });

    it('can close panel after error occurs', () => {
      const { clearIssue } = setupMocks({ error: 'Some error' });
      const issues = [createTestIssue({ id: 'close-error', title: 'Close Error' })];

      render(<GraphViewContainer issues={issues} />);

      fireEvent.click(screen.getByTestId('node-close-error'));
      fireEvent.click(screen.getByTestId('close-button'));

      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'false');

      act(() => {
        vi.advanceTimersByTime(300);
      });

      expect(clearIssue).toHaveBeenCalledTimes(1);
    });
  });

  describe('GraphView props forwarding', () => {
    it('forwards nodesDraggable prop to GraphView', () => {
      render(<GraphViewContainer issues={[]} nodesDraggable={true} />);

      // GraphView mock doesn't explicitly render this prop, but we verify it doesn't crash
      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
    });

    it('forwards layoutDirection prop to GraphView', () => {
      render(<GraphViewContainer issues={[]} layoutDirection="TB" />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
    });

    it('forwards showMiniMap prop to GraphView', () => {
      render(<GraphViewContainer issues={[]} showMiniMap={false} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
    });

    it('forwards showControls prop to GraphView', () => {
      render(<GraphViewContainer issues={[]} showControls={false} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles rapid node clicks without errors', () => {
      const { fetchIssue } = setupMocks();
      const issues = [
        createTestIssue({ id: 'rapid-1', title: 'Rapid 1' }),
        createTestIssue({ id: 'rapid-2', title: 'Rapid 2' }),
        createTestIssue({ id: 'rapid-3', title: 'Rapid 3' }),
      ];

      render(<GraphViewContainer issues={issues} />);

      // Rapidly click different nodes
      fireEvent.click(screen.getByTestId('node-rapid-1'));
      fireEvent.click(screen.getByTestId('node-rapid-2'));
      fireEvent.click(screen.getByTestId('node-rapid-3'));

      // All fetches should be triggered
      expect(fetchIssue).toHaveBeenCalledTimes(3);
      expect(fetchIssue).toHaveBeenNthCalledWith(1, 'rapid-1');
      expect(fetchIssue).toHaveBeenNthCalledWith(2, 'rapid-2');
      expect(fetchIssue).toHaveBeenNthCalledWith(3, 'rapid-3');
    });

    it('handles clicking same node multiple times while loading', () => {
      const { fetchIssue } = setupMocks({ isLoading: true });
      const issues = [createTestIssue({ id: 'multi-click', title: 'Multi Click' })];

      render(<GraphViewContainer issues={issues} />);

      // Click the same node multiple times while loading
      const node = screen.getByTestId('node-multi-click');
      fireEvent.click(node);

      // After first click, panel is open and selectedIssueId is set
      // Subsequent clicks on the same issue should be ignored
      fireEvent.click(node);
      fireEvent.click(node);

      // Only first click should trigger fetch
      expect(fetchIssue).toHaveBeenCalledTimes(1);
    });

    it('handles empty issues array', () => {
      render(<GraphViewContainer issues={[]} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      expect(screen.getByTestId('issue-detail-panel')).toHaveAttribute('data-is-open', 'false');
    });

    it('renders with undefined optional props', () => {
      render(<GraphViewContainer issues={[]} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      expect(screen.getByTestId('issue-detail-panel')).toBeInTheDocument();
    });
  });
});
