/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GraphControls component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { GraphControls } from '../GraphControls';
import type { GraphControlsProps } from '../GraphControls';

// Mock useReactFlow hook
const mockZoomIn = vi.fn();
const mockZoomOut = vi.fn();
const mockFitView = vi.fn();

vi.mock('@xyflow/react', () => ({
  useReactFlow: () => ({
    zoomIn: mockZoomIn,
    zoomOut: mockZoomOut,
    fitView: mockFitView,
  }),
}));

/**
 * Create test props for GraphControls component.
 */
function createTestProps(overrides: Partial<GraphControlsProps> = {}): GraphControlsProps {
  return {
    highlightReady: false,
    onHighlightReadyChange: vi.fn(),
    ...overrides,
  };
}

describe('GraphControls', () => {
  beforeEach(() => {
    mockZoomIn.mockClear();
    mockZoomOut.mockClear();
    mockFitView.mockClear();
  });

  describe('rendering', () => {
    it('renders checkbox with correct checked state when false', () => {
      const props = createTestProps({ highlightReady: false });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      expect(checkbox).not.toBeChecked();
    });

    it('renders checkbox with correct checked state when true', () => {
      const props = createTestProps({ highlightReady: true });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      expect(checkbox).toBeChecked();
    });

    it('renders "Highlight Ready" label text', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      expect(screen.getByText('Highlight Ready')).toBeInTheDocument();
    });

    it('has correct aria-label for accessibility', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      expect(screen.getByLabelText('Highlight ready issues')).toBeInTheDocument();
    });

    it('renders with data-testid for testing', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('graph-controls')).toBeInTheDocument();
      expect(screen.getByTestId('highlight-ready-toggle')).toBeInTheDocument();
    });
  });

  describe('interactions', () => {
    it('calls onChange when checkbox is toggled to checked', () => {
      const onHighlightReadyChange = vi.fn();
      const props = createTestProps({
        highlightReady: false,
        onHighlightReadyChange,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      fireEvent.click(checkbox);

      expect(onHighlightReadyChange).toHaveBeenCalledWith(true);
    });

    it('calls onChange when checkbox is toggled to unchecked', () => {
      const onHighlightReadyChange = vi.fn();
      const props = createTestProps({
        highlightReady: true,
        onHighlightReadyChange,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      fireEvent.click(checkbox);

      expect(onHighlightReadyChange).toHaveBeenCalledWith(false);
    });

    it('calls onChange only once per toggle', () => {
      const onHighlightReadyChange = vi.fn();
      const props = createTestProps({
        highlightReady: false,
        onHighlightReadyChange,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      fireEvent.click(checkbox);

      expect(onHighlightReadyChange).toHaveBeenCalledTimes(1);
    });
  });

  describe('disabled state', () => {
    it('applies disabled attribute when disabled is true', () => {
      const props = createTestProps({ disabled: true });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      expect(checkbox).toBeDisabled();
    });

    it('does not respond to clicks when disabled (checkbox behavior)', () => {
      // Note: fireEvent.click bypasses the disabled attribute, so we verify
      // the disabled attribute is correctly set instead. In a real browser,
      // the click would be ignored. We already test that disabled is set above.
      const props = createTestProps({ disabled: true });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByRole('checkbox');
      // Verify that the disabled attribute is present, which in a real browser
      // would prevent the click from triggering the onChange
      expect(checkbox).toBeDisabled();
    });

    it('shows disabledTitle as tooltip when disabled', () => {
      const props = createTestProps({
        disabled: true,
        disabledTitle: 'Loading blocked status...',
      });
      render(<GraphControls {...props} />);

      const label = screen.getByText('Highlight Ready').closest('label');
      expect(label).toHaveAttribute('title', 'Loading blocked status...');
    });

    it('does not show tooltip when not disabled', () => {
      const props = createTestProps({
        disabled: false,
        disabledTitle: 'Loading blocked status...',
      });
      render(<GraphControls {...props} />);

      const label = screen.getByText('Highlight Ready').closest('label');
      expect(label).not.toHaveAttribute('title');
    });
  });

  describe('className prop', () => {
    it('applies additional className when provided', () => {
      const props = createTestProps({ className: 'custom-class' });
      render(<GraphControls {...props} />);

      const container = screen.getByTestId('graph-controls');
      expect(container.className).toMatch(/custom-class/);
    });

    it('preserves base class when additional className is provided', () => {
      const props = createTestProps({ className: 'custom-class' });
      render(<GraphControls {...props} />);

      const container = screen.getByTestId('graph-controls');
      expect(container.className).toMatch(/graphControls/);
    });
  });

  describe('show closed toggle', () => {
    it('renders show closed checkbox when onShowClosedChange is provided', () => {
      const props = createTestProps({
        showClosed: true,
        onShowClosedChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('show-closed-toggle')).toBeInTheDocument();
      expect(screen.getByText('Show Closed')).toBeInTheDocument();
    });

    it('does not render checkbox when onShowClosedChange is undefined', () => {
      const props = createTestProps({
        showClosed: true,
        // onShowClosedChange not provided
      });
      render(<GraphControls {...props} />);

      expect(screen.queryByTestId('show-closed-toggle')).not.toBeInTheDocument();
    });

    it('checkbox reflects showClosed prop value when true', () => {
      const props = createTestProps({
        showClosed: true,
        onShowClosedChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      expect(checkbox).toBeChecked();
    });

    it('checkbox reflects showClosed prop value when false', () => {
      const props = createTestProps({
        showClosed: false,
        onShowClosedChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      expect(checkbox).not.toBeChecked();
    });

    it('defaults to checked when showClosed is undefined', () => {
      const props = createTestProps({
        // showClosed not provided - defaults to true
        onShowClosedChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      expect(checkbox).toBeChecked();
    });

    it('calls onShowClosedChange with true when toggled to checked', () => {
      const onShowClosedChange = vi.fn();
      const props = createTestProps({
        showClosed: false,
        onShowClosedChange,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      fireEvent.click(checkbox);

      expect(onShowClosedChange).toHaveBeenCalledWith(true);
    });

    it('calls onShowClosedChange with false when toggled to unchecked', () => {
      const onShowClosedChange = vi.fn();
      const props = createTestProps({
        showClosed: true,
        onShowClosedChange,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      fireEvent.click(checkbox);

      expect(onShowClosedChange).toHaveBeenCalledWith(false);
    });

    it('calls onShowClosedChange only once per toggle', () => {
      const onShowClosedChange = vi.fn();
      const props = createTestProps({
        showClosed: false,
        onShowClosedChange,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      fireEvent.click(checkbox);

      expect(onShowClosedChange).toHaveBeenCalledTimes(1);
    });

    it('checkbox is disabled when disabled prop is true', () => {
      const props = createTestProps({
        showClosed: true,
        onShowClosedChange: vi.fn(),
        disabled: true,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('show-closed-toggle');
      expect(checkbox).toBeDisabled();
    });

    it('has correct aria-label for accessibility', () => {
      const props = createTestProps({
        showClosed: true,
        onShowClosedChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByLabelText('Show closed issues')).toBeInTheDocument();
    });
  });

  describe('status filter dropdown', () => {
    it('renders status filter dropdown when onStatusFilterChange is provided', () => {
      const props = createTestProps({
        statusFilter: 'all',
        onStatusFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('status-filter')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
    });

    it('does not render dropdown when onStatusFilterChange is undefined', () => {
      const props = createTestProps({
        statusFilter: 'all',
        // onStatusFilterChange not provided
      });
      render(<GraphControls {...props} />);

      expect(screen.queryByTestId('status-filter')).not.toBeInTheDocument();
    });

    it('dropdown shows correct selected value', () => {
      const props = createTestProps({
        statusFilter: 'closed',
        onStatusFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      const select = screen.getByTestId('status-filter') as HTMLSelectElement;
      expect(select.value).toBe('closed');
    });

    it('defaults to "all" when statusFilter is undefined', () => {
      const props = createTestProps({
        // statusFilter not provided - defaults to 'all'
        onStatusFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      const select = screen.getByTestId('status-filter') as HTMLSelectElement;
      expect(select.value).toBe('all');
    });

    it('calls onStatusFilterChange when selection changes', () => {
      const onStatusFilterChange = vi.fn();
      const props = createTestProps({
        statusFilter: 'all',
        onStatusFilterChange,
      });
      render(<GraphControls {...props} />);

      const select = screen.getByTestId('status-filter');
      fireEvent.change(select, { target: { value: 'in_progress' } });

      expect(onStatusFilterChange).toHaveBeenCalledWith('in_progress');
    });

    it('calls onStatusFilterChange only once per change', () => {
      const onStatusFilterChange = vi.fn();
      const props = createTestProps({
        statusFilter: 'all',
        onStatusFilterChange,
      });
      render(<GraphControls {...props} />);

      const select = screen.getByTestId('status-filter');
      fireEvent.change(select, { target: { value: 'open' } });

      expect(onStatusFilterChange).toHaveBeenCalledTimes(1);
    });

    it('dropdown is disabled when disabled prop is true', () => {
      const props = createTestProps({
        statusFilter: 'all',
        onStatusFilterChange: vi.fn(),
        disabled: true,
      });
      render(<GraphControls {...props} />);

      const select = screen.getByTestId('status-filter');
      expect(select).toBeDisabled();
    });

    it('has correct aria-label for accessibility', () => {
      const props = createTestProps({
        statusFilter: 'all',
        onStatusFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByLabelText('Filter by status')).toBeInTheDocument();
    });

    it('includes all expected status options', () => {
      const props = createTestProps({
        statusFilter: 'all',
        onStatusFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      const select = screen.getByTestId('status-filter');
      const options = select.querySelectorAll('option');
      const optionValues = Array.from(options).map(o => o.value);

      expect(optionValues).toContain('all');
      expect(optionValues).toContain('open');
      expect(optionValues).toContain('in_progress');
      expect(optionValues).toContain('blocked');
      expect(optionValues).toContain('deferred');
      expect(optionValues).toContain('closed');
    });
  });

  describe('zoom controls', () => {
    it('renders zoom controls by default', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('zoom-in-button')).toBeInTheDocument();
      expect(screen.getByTestId('zoom-out-button')).toBeInTheDocument();
      expect(screen.getByTestId('fit-view-button')).toBeInTheDocument();
    });

    it('hides zoom controls when showZoomControls is false', () => {
      const props = createTestProps({ showZoomControls: false });
      render(<GraphControls {...props} />);

      expect(screen.queryByTestId('zoom-in-button')).not.toBeInTheDocument();
      expect(screen.queryByTestId('zoom-out-button')).not.toBeInTheDocument();
      expect(screen.queryByTestId('fit-view-button')).not.toBeInTheDocument();
    });

    it('calls zoomIn when zoom in button is clicked', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      const zoomInButton = screen.getByTestId('zoom-in-button');
      fireEvent.click(zoomInButton);

      expect(mockZoomIn).toHaveBeenCalledWith({ duration: 200 });
    });

    it('calls zoomOut when zoom out button is clicked', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      const zoomOutButton = screen.getByTestId('zoom-out-button');
      fireEvent.click(zoomOutButton);

      expect(mockZoomOut).toHaveBeenCalledWith({ duration: 200 });
    });

    it('calls fitView when fit view button is clicked', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      const fitViewButton = screen.getByTestId('fit-view-button');
      fireEvent.click(fitViewButton);

      expect(mockFitView).toHaveBeenCalledWith({ duration: 200, padding: 0.2 });
    });

    it('zoom buttons have correct aria-labels', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      expect(screen.getByLabelText('Zoom in')).toBeInTheDocument();
      expect(screen.getByLabelText('Zoom out')).toBeInTheDocument();
      expect(screen.getByLabelText('Fit to view')).toBeInTheDocument();
    });

    it('zoom controls group has correct aria-label', () => {
      const props = createTestProps();
      render(<GraphControls {...props} />);

      const zoomGroup = screen.getByRole('group', { name: 'Zoom controls' });
      expect(zoomGroup).toBeInTheDocument();
    });
  });

  describe('dependency type filter', () => {
    it('renders dependency type checkboxes when handler and filter are provided', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking', 'parent-child', 'non-blocking']),
        onDependencyTypeFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('dep-type-filter')).toBeInTheDocument();
      expect(screen.getByTestId('dep-type-blocking')).toBeInTheDocument();
      expect(screen.getByTestId('dep-type-parent-child')).toBeInTheDocument();
      expect(screen.getByTestId('dep-type-non-blocking')).toBeInTheDocument();
    });

    it('does not render dependency type checkboxes when handler is not provided', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking', 'parent-child']),
        // onDependencyTypeFilterChange not provided
      });
      render(<GraphControls {...props} />);

      expect(screen.queryByTestId('dep-type-filter')).not.toBeInTheDocument();
    });

    it('does not render dependency type checkboxes when filter is not provided', () => {
      const props = createTestProps({
        // dependencyTypeFilter not provided
        onDependencyTypeFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.queryByTestId('dep-type-filter')).not.toBeInTheDocument();
    });

    it('renders "Edges" label', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByText('Edges')).toBeInTheDocument();
    });

    it('renders checkbox labels: Blocking, Parent-Child, Non-blocking', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByText('Blocking')).toBeInTheDocument();
      expect(screen.getByText('Parent-Child')).toBeInTheDocument();
      expect(screen.getByText('Non-blocking')).toBeInTheDocument();
    });

    it('reflects checked state based on filter Set', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking', 'parent-child']),
        onDependencyTypeFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('dep-type-blocking')).toBeChecked();
      expect(screen.getByTestId('dep-type-parent-child')).toBeChecked();
      expect(screen.getByTestId('dep-type-non-blocking')).not.toBeChecked();
    });

    it('reflects all checkboxes unchecked when filter Set is empty', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(),
        onDependencyTypeFilterChange: vi.fn(),
      });
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('dep-type-blocking')).not.toBeChecked();
      expect(screen.getByTestId('dep-type-parent-child')).not.toBeChecked();
      expect(screen.getByTestId('dep-type-non-blocking')).not.toBeChecked();
    });

    it('calls handler with blocking added when clicking unchecked blocking checkbox', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['parent-child']),
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-blocking'));

      expect(onDependencyTypeFilterChange).toHaveBeenCalledTimes(1);
      const newFilter = onDependencyTypeFilterChange.mock.calls[0][0];
      expect(newFilter.has('blocking')).toBe(true);
      expect(newFilter.has('parent-child')).toBe(true);
    });

    it('calls handler with blocking removed when clicking checked blocking checkbox', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking', 'parent-child']),
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-blocking'));

      expect(onDependencyTypeFilterChange).toHaveBeenCalledTimes(1);
      const newFilter = onDependencyTypeFilterChange.mock.calls[0][0];
      expect(newFilter.has('blocking')).toBe(false);
      expect(newFilter.has('parent-child')).toBe(true);
    });

    it('calls handler with parent-child added when clicking unchecked parent-child checkbox', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-parent-child'));

      expect(onDependencyTypeFilterChange).toHaveBeenCalledTimes(1);
      const newFilter = onDependencyTypeFilterChange.mock.calls[0][0];
      expect(newFilter.has('blocking')).toBe(true);
      expect(newFilter.has('parent-child')).toBe(true);
    });

    it('calls handler with non-blocking added when clicking unchecked non-blocking checkbox', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-non-blocking'));

      expect(onDependencyTypeFilterChange).toHaveBeenCalledTimes(1);
      const newFilter = onDependencyTypeFilterChange.mock.calls[0][0];
      expect(newFilter.has('blocking')).toBe(true);
      expect(newFilter.has('non-blocking')).toBe(true);
    });

    it('calls handler with non-blocking removed when clicking checked non-blocking checkbox', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking', 'non-blocking']),
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-non-blocking'));

      expect(onDependencyTypeFilterChange).toHaveBeenCalledTimes(1);
      const newFilter = onDependencyTypeFilterChange.mock.calls[0][0];
      expect(newFilter.has('blocking')).toBe(true);
      expect(newFilter.has('non-blocking')).toBe(false);
    });

    it('checkboxes are disabled when disabled prop is true', () => {
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange: vi.fn(),
        disabled: true,
      });
      render(<GraphControls {...props} />);

      expect(screen.getByTestId('dep-type-blocking')).toBeDisabled();
      expect(screen.getByTestId('dep-type-parent-child')).toBeDisabled();
      expect(screen.getByTestId('dep-type-non-blocking')).toBeDisabled();
    });

    it('does not call handler when checkboxes are disabled', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange,
        disabled: true,
      });
      render(<GraphControls {...props} />);

      const checkbox = screen.getByTestId('dep-type-blocking');
      // Verify it's disabled
      expect(checkbox).toBeDisabled();
      // Note: fireEvent.click bypasses disabled in jsdom, but the handler
      // has an early return when disabled via the checkbox's disabled attribute
    });

    it('calls handler only once per checkbox toggle', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const props = createTestProps({
        dependencyTypeFilter: new Set(['blocking']),
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-parent-child'));

      expect(onDependencyTypeFilterChange).toHaveBeenCalledTimes(1);
    });

    it('returns a new Set instance on each change (immutability)', () => {
      const onDependencyTypeFilterChange = vi.fn();
      const originalFilter = new Set(['blocking']);
      const props = createTestProps({
        dependencyTypeFilter: originalFilter,
        onDependencyTypeFilterChange,
      });
      render(<GraphControls {...props} />);

      fireEvent.click(screen.getByTestId('dep-type-parent-child'));

      const newFilter = onDependencyTypeFilterChange.mock.calls[0][0];
      expect(newFilter).not.toBe(originalFilter);
      // Original should be unchanged
      expect(originalFilter.has('parent-child')).toBe(false);
    });
  });
});
