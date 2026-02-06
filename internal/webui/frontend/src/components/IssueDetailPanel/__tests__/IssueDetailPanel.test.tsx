/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueDetailPanel component.
 */

import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach as _beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import type { Issue, IssueDetails, IssueWithDependencyMetadata } from '@/types';

import { IssueDetailPanel } from '../IssueDetailPanel';

// Create hoisted mocks
const { mockGetTaskLogPhases, mockUseLogStream } = vi.hoisted(() => ({
  mockGetTaskLogPhases: vi.fn().mockResolvedValue([]),
  mockUseLogStream: vi.fn(() => ({
    lines: [],
    state: 'disconnected' as const,
    isConnected: false,
    reconnectAttempts: 0,
    lastError: null,
    clearLines: vi.fn(),
    connect: vi.fn(),
    disconnect: vi.fn(),
  })),
}));

// Mock the API module
vi.mock('@/api', () => ({
  updateIssue: vi.fn(),
  addDependency: vi.fn(),
  removeDependency: vi.fn(),
  getTaskLogPhases: mockGetTaskLogPhases,
  getTaskLogStreamUrl: vi.fn().mockReturnValue('/api/tasks/test/logs/planning/stream'),
}));

// Mock the useLogStream hook
vi.mock('@/hooks', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/hooks')>();
  return {
    ...orig,
    useLogStream: mockUseLogStream,
  };
});

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-123',
    title: 'Test Issue',
    priority: 2,
    created_at: '2026-01-23T00:00:00Z',
    updated_at: '2026-01-23T00:00:00Z',
    ...overrides,
  };
}

/**
 * Create a test issue with full details (IssueDetails type).
 */
function createTestIssueDetails(overrides: Partial<IssueDetails> = {}): IssueDetails {
  return {
    id: 'test-123',
    title: 'Test Issue',
    priority: 2,
    created_at: '2026-01-23T00:00:00Z',
    updated_at: '2026-01-23T00:00:00Z',
    comments: [],
    dependencies: [],
    dependents: [],
    ...overrides,
  };
}

/**
 * Create a test dependency issue.
 */
function createTestDependency(
  overrides: Partial<IssueWithDependencyMetadata> = {}
): IssueWithDependencyMetadata {
  return {
    id: 'dep-456',
    title: 'Dependency Issue',
    priority: 2,
    created_at: '2026-01-23T00:00:00Z',
    updated_at: '2026-01-23T00:00:00Z',
    status: 'open',
    dependency_type: 'blocks',
    ...overrides,
  };
}

describe('IssueDetailPanel', () => {
  // Reset body overflow after each test
  afterEach(() => {
    document.body.style.overflow = '';
  });

  describe('rendering', () => {
    it('renders when open', () => {
      const mockIssue = createTestIssue();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.getByTestId('issue-detail-panel')).toBeInTheDocument();
    });

    it('renders children in content area', () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}}>
          <div data-testid="child-content">Child Content</div>
        </IssueDetailPanel>
      );
      expect(screen.getByTestId('child-content')).toBeInTheDocument();
    });

    it('applies open class when isOpen is true', () => {
      const mockIssue = createTestIssue();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const overlay = screen.getByTestId('issue-detail-overlay');
      // CSS modules mangle class names, so check for pattern containing 'open'
      expect(overlay.className).toMatch(/open/i);
    });

    it('does not apply open class when isOpen is false', () => {
      render(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);
      const overlay = screen.getByTestId('issue-detail-overlay');
      // CSS modules mangle class names, so check that 'open' pattern is not present
      expect(overlay.className).not.toMatch(/_open_/);
    });

    it('renders even when closed (for animation)', () => {
      render(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);
      expect(screen.getByTestId('issue-detail-panel')).toBeInTheDocument();
    });
  });

  describe('close interactions', () => {
    it('calls onClose when clicking overlay', () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />);
      fireEvent.click(screen.getByTestId('issue-detail-overlay'));
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does not call onClose when clicking panel', () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />);
      fireEvent.click(screen.getByTestId('issue-detail-panel'));
      expect(onClose).not.toHaveBeenCalled();
    });

    it('calls onClose when pressing Escape', () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />);
      fireEvent.keyDown(document, { key: 'Escape' });
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does not call onClose on Escape when closed', () => {
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={false} issue={null} onClose={onClose} />);
      fireEvent.keyDown(document, { key: 'Escape' });
      expect(onClose).not.toHaveBeenCalled();
    });

    it('does not call onClose on other keys', () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />);
      fireEvent.keyDown(document, { key: 'Enter' });
      fireEvent.keyDown(document, { key: 'Tab' });
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe('accessibility', () => {
    it('has correct ARIA attributes when open with issue', () => {
      const mockIssue = createTestIssue({ title: 'Test Issue' });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toHaveAttribute('role', 'dialog');
      expect(panel).toHaveAttribute('aria-modal', 'true');
      expect(panel).toHaveAttribute('aria-label', 'Details for Test Issue');
    });

    it('has default aria-label when issue is null', () => {
      render(<IssueDetailPanel isOpen={true} issue={null} onClose={() => {}} />);
      const panel = screen.getByTestId('issue-detail-panel');
      expect(panel).toHaveAttribute('aria-label', 'Issue details');
    });

    it('sets aria-hidden on overlay when closed', () => {
      render(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);
      const overlay = screen.getByTestId('issue-detail-overlay');
      expect(overlay).toHaveAttribute('aria-hidden', 'true');
    });

    it('clears aria-hidden on overlay when open', () => {
      const mockIssue = createTestIssue();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const overlay = screen.getByTestId('issue-detail-overlay');
      expect(overlay).toHaveAttribute('aria-hidden', 'false');
    });
  });

  describe('body scroll lock', () => {
    it('locks body scroll when open', () => {
      const mockIssue = createTestIssue();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(document.body.style.overflow).toBe('hidden');
    });

    it('restores body scroll when closed', () => {
      const mockIssue = createTestIssue();
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />
      );
      expect(document.body.style.overflow).toBe('hidden');

      rerender(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);
      expect(document.body.style.overflow).toBe('');
    });

    it('does not lock body scroll when initially closed', () => {
      render(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);
      expect(document.body.style.overflow).toBe('');
    });
  });

  describe('className prop', () => {
    it('applies custom className to overlay', () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          className="custom-class"
        />
      );
      const overlay = screen.getByTestId('issue-detail-overlay');
      expect(overlay).toHaveClass('custom-class');
    });

    it('combines custom className with open class', () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          className="custom-class"
        />
      );
      const overlay = screen.getByTestId('issue-detail-overlay');
      // CSS modules mangle class names, so check for pattern containing 'open'
      expect(overlay.className).toMatch(/open/i);
      expect(overlay).toHaveClass('custom-class');
    });
  });

  describe('cleanup', () => {
    it('removes keydown listener on unmount when open', () => {
      const onClose = vi.fn();
      const mockIssue = createTestIssue();
      const { unmount } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />
      );

      unmount();

      // Escape key should not trigger onClose after unmount
      fireEvent.keyDown(document, { key: 'Escape' });
      expect(onClose).not.toHaveBeenCalled();
    });

    it('restores body scroll on unmount when open', () => {
      const mockIssue = createTestIssue();
      const { unmount } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />
      );
      expect(document.body.style.overflow).toBe('hidden');

      unmount();
      expect(document.body.style.overflow).toBe('');
    });
  });

  describe('edge cases', () => {
    it('handles null issue when open', () => {
      render(<IssueDetailPanel isOpen={true} issue={null} onClose={() => {}} />);
      expect(screen.getByTestId('issue-detail-panel')).toBeInTheDocument();
    });

    it('handles rapid open/close', () => {
      const mockIssue = createTestIssue();
      const { rerender } = render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />
      );

      // Rapidly toggle
      rerender(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      rerender(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);
      rerender(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);

      // CSS modules mangle class names, so check for pattern containing 'open'
      expect(screen.getByTestId('issue-detail-overlay').className).toMatch(/open/i);
      expect(document.body.style.overflow).toBe('hidden');
    });

    it('stops propagation on panel click to prevent overlay close', () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose}>
          <button data-testid="inner-button">Click me</button>
        </IssueDetailPanel>
      );

      // Click on the inner button - should not close
      fireEvent.click(screen.getByTestId('inner-button'));
      expect(onClose).not.toHaveBeenCalled();

      // Click on the panel itself - should not close
      fireEvent.click(screen.getByTestId('issue-detail-panel'));
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe('CollapsibleSection', () => {
    it('renders design section expanded by default for short content', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Short design text',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const designSection = screen.getByTestId('design-section');
      const button = within(designSection).getByRole('button');
      expect(button).toHaveAttribute('aria-expanded', 'true');
    });

    it('renders design section collapsed by default for long content', () => {
      const longDesign = 'A'.repeat(250); // More than 200 chars
      const mockIssue = createTestIssueDetails({
        design: longDesign,
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const designSection = screen.getByTestId('design-section');
      const button = within(designSection).getByRole('button');
      expect(button).toHaveAttribute('aria-expanded', 'false');
    });

    it('toggles expanded state when section header is clicked', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some design content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const designSection = screen.getByTestId('design-section');
      const button = within(designSection).getByRole('button');

      // Initially expanded
      expect(button).toHaveAttribute('aria-expanded', 'true');

      // Click to collapse
      fireEvent.click(button);
      expect(button).toHaveAttribute('aria-expanded', 'false');

      // Click to expand again
      fireEvent.click(button);
      expect(button).toHaveAttribute('aria-expanded', 'true');
    });

    it('shows collapsible section title', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Design content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const designSection = screen.getByTestId('design-section');
      expect(within(designSection).getByText('Design')).toBeInTheDocument();
    });

    it('hides content when collapsed', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Visible design content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const designSection = screen.getByTestId('design-section');
      const button = within(designSection).getByRole('button');

      // Content visible when expanded
      expect(screen.getByText('Visible design content')).toBeInTheDocument();

      // Collapse the section
      fireEvent.click(button);

      // Content should be hidden
      expect(screen.queryByText('Visible design content')).not.toBeInTheDocument();
    });

    it('renders notes section when notes provided', () => {
      const mockIssue = createTestIssueDetails({
        notes: 'Some notes content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.getByTestId('notes-section')).toBeInTheDocument();
      expect(screen.getByText('Notes')).toBeInTheDocument();
    });
  });

  describe('BlockingBanner', () => {
    it('shows blocking banner when issue has open dependencies', () => {
      const mockIssue = createTestIssueDetails({
        dependencies: [
          createTestDependency({ id: 'dep-1', status: 'open' }),
          createTestDependency({ id: 'dep-2', status: 'open' }),
        ],
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const banner = screen.getByTestId('blocking-banner');
      expect(banner).toBeInTheDocument();
      expect(banner).toHaveTextContent('Blocked by 2 issues');
    });

    it('shows singular text when blocked by 1 issue', () => {
      const mockIssue = createTestIssueDetails({
        dependencies: [createTestDependency({ id: 'dep-1', status: 'open' })],
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const banner = screen.getByTestId('blocking-banner');
      expect(banner).toHaveTextContent('Blocked by 1 issue');
    });

    it('does not show banner when all dependencies are closed', () => {
      const mockIssue = createTestIssueDetails({
        dependencies: [
          createTestDependency({ id: 'dep-1', status: 'closed' }),
          createTestDependency({ id: 'dep-2', status: 'closed' }),
        ],
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('blocking-banner')).not.toBeInTheDocument();
    });

    it('does not show banner when no dependencies', () => {
      const mockIssue = createTestIssueDetails({
        dependencies: [],
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('blocking-banner')).not.toBeInTheDocument();
    });

    it('counts only open dependencies (excludes closed)', () => {
      const mockIssue = createTestIssueDetails({
        dependencies: [
          createTestDependency({ id: 'dep-1', status: 'open' }),
          createTestDependency({ id: 'dep-2', status: 'closed' }),
          createTestDependency({ id: 'dep-3', status: 'in_progress' }),
        ],
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const banner = screen.getByTestId('blocking-banner');
      // Only open and in_progress count as blockers (not closed)
      expect(banner).toHaveTextContent('Blocked by 2 issues');
    });
  });

  describe('Metadata bar', () => {
    it('renders issue type', () => {
      const mockIssue = createTestIssueDetails({
        issue_type: 'bug',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const typeItem = screen.getByTestId('metadata-type');
      expect(typeItem).toHaveTextContent('Bug');
    });

    it('defaults to Task when issue_type is undefined', () => {
      const mockIssue = createTestIssueDetails({
        issue_type: undefined,
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const typeItem = screen.getByTestId('metadata-type');
      expect(typeItem).toHaveTextContent('Task');
    });

    it('renders owner when provided', () => {
      const mockIssue = createTestIssueDetails({
        owner: 'john-doe',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const ownerItem = screen.getByTestId('metadata-owner');
      expect(ownerItem).toHaveTextContent('john-doe');
    });

    it('does not render owner when not provided', () => {
      const mockIssue = createTestIssueDetails({
        owner: undefined,
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('metadata-owner')).not.toBeInTheDocument();
    });

    it('renders assignee with @ prefix', () => {
      const mockIssue = createTestIssueDetails({
        assignee: 'jane-smith',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const assigneeItem = screen.getByTestId('metadata-assignee');
      expect(assigneeItem).toHaveTextContent('@jane-smith');
    });

    it('does not render assignee when not provided', () => {
      const mockIssue = createTestIssueDetails({
        assignee: undefined,
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('metadata-assignee')).not.toBeInTheDocument();
    });

    it('renders created date formatted correctly', () => {
      const mockIssue = createTestIssueDetails({
        created_at: '2026-01-15T10:30:00Z',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const createdItem = screen.getByTestId('metadata-created');
      expect(createdItem).toHaveTextContent('Created: Jan 15, 2026');
    });

    it('renders all issue types correctly', () => {
      const testCases = [
        { type: 'epic', expected: 'Epic' },
        { type: 'feature', expected: 'Feature' },
        { type: 'bug', expected: 'Bug' },
        { type: 'task', expected: 'Task' },
      ] as const;

      for (const { type, expected } of testCases) {
        const mockIssue = createTestIssueDetails({ issue_type: type });
        const { unmount } = render(
          <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />
        );
        expect(screen.getByTestId('metadata-type')).toHaveTextContent(expected);
        unmount();
      }
    });
  });

  describe('fullscreen mode', () => {
    it('renders fullscreen button in header', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some design content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.getByTestId('header-fullscreen-button')).toBeInTheDocument();
    });

    it('clicking fullscreen button adds fullscreen class to overlay', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some design content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const overlay = screen.getByTestId('issue-detail-overlay');

      // Initially no fullscreen class
      expect(overlay.className).not.toMatch(/fullscreen/i);

      // Click the fullscreen button
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));

      // Now fullscreen class should be present
      expect(overlay.className).toMatch(/fullscreen/i);
    });

    it('ESC in fullscreen mode returns to panel mode (does not close)', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some design content',
      });
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />);

      // Enter fullscreen
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));
      const overlay = screen.getByTestId('issue-detail-overlay');
      expect(overlay.className).toMatch(/fullscreen/i);

      // Press ESC - should exit fullscreen, not close
      fireEvent.keyDown(document, { key: 'Escape' });
      expect(onClose).not.toHaveBeenCalled();
      expect(overlay.className).not.toMatch(/fullscreen/i);
    });

    it('ESC in panel mode closes panel (existing behavior preserved)', () => {
      const mockIssue = createTestIssueDetails();
      const onClose = vi.fn();
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />);

      // Press ESC in normal panel mode
      fireEvent.keyDown(document, { key: 'Escape' });
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('renders two-column layout when issue has design content in fullscreen', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Design content here',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);

      // Enter fullscreen
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));

      // Two-column layout should be rendered
      const panel = screen.getByTestId('issue-detail-panel');
      const twoColumnLayout = panel.querySelector('[class*="twoColumnLayout"]');
      expect(twoColumnLayout).toBeInTheDocument();
    });

    it('renders single-column layout when no design in fullscreen', () => {
      const mockIssue = createTestIssueDetails({
        design: undefined,
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);

      // Enter fullscreen
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));

      // No two-column layout should be rendered
      const panel = screen.getByTestId('issue-detail-panel');
      const twoColumnLayout = panel.querySelector('[class*="twoColumnLayout"]');
      expect(twoColumnLayout).not.toBeInTheDocument();
    });

    it('renders collapse button in fullscreen header', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some design content',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);

      // Enter fullscreen
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));

      // The button should have collapse aria-label
      const fullscreenButton = screen.getByTestId('header-fullscreen-button');
      expect(fullscreenButton).toHaveAttribute('aria-label', 'Collapse to panel');
    });

    it('fullscreen resets when panel closes', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some design content',
      });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />
      );

      // Enter fullscreen
      fireEvent.click(screen.getByTestId('header-fullscreen-button'));
      expect(screen.getByTestId('issue-detail-overlay').className).toMatch(/fullscreen/i);

      // Close the panel
      rerender(<IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />);

      // Re-open the panel
      rerender(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);

      // Fullscreen should have been reset
      expect(screen.getByTestId('issue-detail-overlay').className).not.toMatch(/fullscreen/i);
    });
  });

  describe('Design section with MarkdownRenderer', () => {
    it('renders design content using MarkdownRenderer', () => {
      const mockIssue = createTestIssueDetails({
        design: 'Some **bold** design text',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      // MarkdownRenderer uses data-testid="markdown-content"
      expect(screen.getByTestId('markdown-content')).toBeInTheDocument();
    });

    it('does not render design section when design is empty', () => {
      const mockIssue = createTestIssueDetails({
        design: undefined,
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      expect(screen.queryByTestId('design-section')).not.toBeInTheDocument();
    });

    it('renders markdown formatting in design content', () => {
      const mockIssue = createTestIssueDetails({
        design: '# Heading\n\n- List item 1\n- List item 2',
      });
      render(<IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />);
      const designSection = screen.getByTestId('design-section');
      // Check that markdown was rendered (heading becomes h1)
      expect(within(designSection).getByRole('heading', { level: 1 })).toHaveTextContent('Heading');
    });
  });
});
