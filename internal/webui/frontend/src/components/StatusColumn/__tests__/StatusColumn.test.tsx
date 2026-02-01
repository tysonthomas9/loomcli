/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for StatusColumn component.
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import { DndContext } from '@dnd-kit/core';

import { StatusColumn } from '../StatusColumn';
import { formatStatusLabel, getStatusColor } from '../utils';

/**
 * Helper to render StatusColumn within a DndContext for droppable tests.
 */
function renderWithDndContext(ui: React.ReactNode) {
  return render(<DndContext>{ui}</DndContext>);
}

describe('StatusColumn', () => {
  describe('rendering', () => {
    it('renders with status and count', () => {
      render(<StatusColumn status="open" count={5} />);

      expect(screen.getByRole('heading', { name: 'Open' })).toBeInTheDocument();
      expect(screen.getByText('5')).toBeInTheDocument();
    });

    it('renders status label correctly formatted', () => {
      render(<StatusColumn status="in_progress" count={3} />);

      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
    });

    it('renders count badge with correct number', () => {
      render(<StatusColumn status="closed" count={42} />);

      expect(screen.getByText('42')).toBeInTheDocument();
    });

    it('renders children when provided', () => {
      render(
        <StatusColumn status="open" count={1}>
          <div data-testid="child-card">Issue Card</div>
        </StatusColumn>
      );

      expect(screen.getByTestId('child-card')).toBeInTheDocument();
    });

    it('renders empty content area when no children', () => {
      render(<StatusColumn status="open" count={0} />);

      const list = screen.getByRole('list');
      expect(list).toBeInTheDocument();
      expect(list).toBeEmptyDOMElement();
    });
  });

  describe('status formatting', () => {
    it('formats single word status correctly', () => {
      render(<StatusColumn status="open" count={1} />);
      expect(screen.getByRole('heading', { name: 'Open' })).toBeInTheDocument();
    });

    it('formats snake_case status to title case', () => {
      render(<StatusColumn status="in_progress" count={1} />);
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
    });

    it('formats blocked status', () => {
      render(<StatusColumn status="blocked" count={1} />);
      expect(screen.getByRole('heading', { name: 'Blocked' })).toBeInTheDocument();
    });

    it('formats custom status correctly', () => {
      render(<StatusColumn status="custom_status_name" count={1} />);
      expect(screen.getByRole('heading', { name: 'Custom Status Name' })).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has section with appropriate aria-label', () => {
      render(<StatusColumn status="open" count={5} />);

      const section = screen.getByRole('region', { name: 'Open issues' });
      expect(section).toBeInTheDocument();
    });

    it('has h2 heading element', () => {
      render(<StatusColumn status="open" count={1} />);

      const heading = screen.getByRole('heading', { level: 2 });
      expect(heading).toBeInTheDocument();
    });

    it('content area has role="list"', () => {
      render(<StatusColumn status="open" count={1} />);

      expect(screen.getByRole('list')).toBeInTheDocument();
    });

    it('count has aria-label with issue count', () => {
      render(<StatusColumn status="open" count={5} />);

      expect(screen.getByLabelText('5 issues')).toBeInTheDocument();
    });

    it('count uses singular form for 1 issue', () => {
      render(<StatusColumn status="open" count={1} />);

      expect(screen.getByLabelText('1 issue')).toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('custom statusLabel overrides formatted status', () => {
      render(<StatusColumn status="in_progress" statusLabel="WIP" count={3} />);

      expect(screen.getByRole('heading', { name: 'WIP' })).toBeInTheDocument();
      expect(screen.queryByText('In Progress')).not.toBeInTheDocument();
    });

    it('className prop is applied to root element', () => {
      const { container } = render(
        <StatusColumn status="open" count={1} className="custom-class" />
      );

      const section = container.querySelector('section');
      expect(section).toHaveClass('custom-class');
    });

    it('data-status attribute matches status prop', () => {
      const { container } = render(<StatusColumn status="in_progress" count={1} />);

      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-status', 'in_progress');
    });

    it('sets data-status for each known status', () => {
      const statuses = ['open', 'in_progress', 'closed', 'blocked', 'deferred'];

      statuses.forEach((status) => {
        const { container, unmount } = render(<StatusColumn status={status} count={1} />);

        const section = container.querySelector('section');
        expect(section).toHaveAttribute('data-status', status);

        unmount();
      });
    });
  });

  describe('columnType prop', () => {
    it('renders with data-column-type="backlog" when columnType is backlog', () => {
      const { container } = render(<StatusColumn status="open" count={1} columnType="backlog" />);

      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-column-type', 'backlog');
    });

    it('renders with data-column-type for each column type', () => {
      const columnTypes = ['ready', 'backlog', 'review', 'default'] as const;

      columnTypes.forEach((columnType) => {
        const { container, unmount } = render(
          <StatusColumn status="open" count={1} columnType={columnType} />
        );

        const section = container.querySelector('section');
        expect(section).toHaveAttribute('data-column-type', columnType);

        unmount();
      });
    });

    it('does not set data-column-type when columnType is undefined', () => {
      const { container } = render(<StatusColumn status="open" count={1} />);

      const section = container.querySelector('section');
      // When columnType is undefined, the attribute value should be undefined/null
      expect(section?.getAttribute('data-column-type')).toBeNull();
    });
  });

  describe('headerIcon prop', () => {
    it('displays headerIcon when provided', () => {
      render(<StatusColumn status="open" count={1} headerIcon="⏳" />);

      // Icon should be in the document
      const icon = screen.getByText('⏳');
      expect(icon).toBeInTheDocument();
      expect(icon).toHaveAttribute('aria-hidden', 'true');
    });

    it('does not show headerIcon when not provided', () => {
      const { container } = render(<StatusColumn status="open" count={1} />);

      // Should not have any columnIcon span
      const iconSpan = container.querySelector('[aria-hidden="true"]');
      expect(iconSpan).not.toBeInTheDocument();
    });

    it('headerIcon is inside the header element', () => {
      const { container } = render(<StatusColumn status="open" count={1} headerIcon="🔍" />);

      const header = container.querySelector('header');
      const icon = screen.getByText('🔍');
      expect(header).toContainElement(icon);
    });

    it('renders headerIcon with different emoji values', () => {
      const icons = ['⏳', '✅', '🔍', '📝'];

      icons.forEach((icon) => {
        const { unmount } = render(<StatusColumn status="open" count={1} headerIcon={icon} />);

        expect(screen.getByText(icon)).toBeInTheDocument();

        unmount();
      });
    });
  });

  describe('data-has-items attribute', () => {
    it('renders data-has-items="true" when count > 0', () => {
      const { container } = render(<StatusColumn status="open" count={5} />);

      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-has-items', 'true');
    });

    it('does not render data-has-items when count = 0', () => {
      const { container } = render(<StatusColumn status="open" count={0} />);

      const section = container.querySelector('section');
      expect(section).not.toHaveAttribute('data-has-items');
    });

    it('renders data-has-items="true" when count = 1', () => {
      const { container } = render(<StatusColumn status="open" count={1} />);

      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-has-items', 'true');
    });

    it('renders data-has-items="true" for large counts', () => {
      const { container } = render(<StatusColumn status="open" count={999} />);

      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-has-items', 'true');
    });

    it('data-has-items attribute is on section element with other data attributes', () => {
      const { container } = render(<StatusColumn status="review" count={3} columnType="review" />);

      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-status', 'review');
      expect(section).toHaveAttribute('data-column-type', 'review');
      expect(section).toHaveAttribute('data-has-items', 'true');
    });
  });

  describe('edge cases', () => {
    it('count of 0 renders correctly', () => {
      render(<StatusColumn status="open" count={0} />);

      expect(screen.getByText('0')).toBeInTheDocument();
      expect(screen.getByLabelText('0 issues')).toBeInTheDocument();
    });

    it('large count renders correctly', () => {
      render(<StatusColumn status="open" count={9999} />);

      expect(screen.getByText('9999')).toBeInTheDocument();
    });

    it('renders with empty string status (edge case)', () => {
      render(<StatusColumn status="" count={1} />);

      // Empty string formatted is empty, but component should still render
      const section = document.querySelector('[data-status=""]');
      expect(section).toBeInTheDocument();
    });
  });

  describe('droppable functionality', () => {
    it('renders with data-droppable-id attribute matching status', () => {
      renderWithDndContext(<StatusColumn status="in_progress" count={0} />);

      const content = document.querySelector('[data-droppable-id="in_progress"]');
      expect(content).toBeInTheDocument();
    });

    it('content area has data-droppable-id for each status', () => {
      const statuses = ['open', 'in_progress', 'closed', 'blocked', 'deferred'];

      statuses.forEach((status) => {
        const { unmount } = renderWithDndContext(<StatusColumn status={status} count={0} />);

        const content = document.querySelector(`[data-droppable-id="${status}"]`);
        expect(content).toBeInTheDocument();
        expect(content).toHaveAttribute('role', 'list');

        unmount();
      });
    });

    it('renders without DndContext (graceful degradation)', () => {
      // Component should work even without DndContext
      render(<StatusColumn status="open" count={0} />);

      const content = document.querySelector('[data-droppable-id="open"]');
      expect(content).toBeInTheDocument();
      expect(content).not.toHaveAttribute('data-is-over');
    });

    it('does not show data-is-over when not being dragged over', () => {
      renderWithDndContext(<StatusColumn status="open" count={0} />);

      const content = document.querySelector('[data-droppable-id="open"]');
      expect(content).not.toHaveAttribute('data-is-over');
    });

    it('renders children inside droppable content area', () => {
      renderWithDndContext(
        <StatusColumn status="open" count={1}>
          <div data-testid="test-card">Test Card</div>
        </StatusColumn>
      );

      const droppableContent = document.querySelector('[data-droppable-id="open"]');
      const card = screen.getByTestId('test-card');

      expect(droppableContent).toContainElement(card);
    });

    it('droppableDisabled prop can disable dropping', () => {
      renderWithDndContext(<StatusColumn status="closed" count={0} droppableDisabled={true} />);

      // Component should still render with droppable ID
      const content = document.querySelector('[data-droppable-id="closed"]');
      expect(content).toBeInTheDocument();
    });

    it('droppableDisabled defaults to false', () => {
      renderWithDndContext(<StatusColumn status="open" count={0} />);

      // Should have droppable ID (indicates it's a drop target)
      const content = document.querySelector('[data-droppable-id="open"]');
      expect(content).toBeInTheDocument();
    });

    it('works with multiple StatusColumns in same DndContext', () => {
      render(
        <DndContext>
          <StatusColumn status="open" count={1} />
          <StatusColumn status="in_progress" count={2} />
          <StatusColumn status="closed" count={0} />
        </DndContext>
      );

      expect(document.querySelector('[data-droppable-id="open"]')).toBeInTheDocument();
      expect(document.querySelector('[data-droppable-id="in_progress"]')).toBeInTheDocument();
      expect(document.querySelector('[data-droppable-id="closed"]')).toBeInTheDocument();
    });
  });

  describe('scrolling behavior', () => {
    it('renders content area with many children', () => {
      const manyChildren = Array.from({ length: 25 }, (_, i) => (
        <div key={i} data-testid={`card-${i}`}>
          Card {i}
        </div>
      ));

      renderWithDndContext(
        <StatusColumn status="open" count={25}>
          {manyChildren}
        </StatusColumn>
      );

      // Verify content area exists and has proper structure
      const contentArea = screen.getByRole('list');
      expect(contentArea).toBeInTheDocument();
      expect(contentArea).toHaveAttribute('data-droppable-id', 'open');

      // Verify all children are rendered within the scrollable content
      for (let i = 0; i < 25; i++) {
        expect(screen.getByTestId(`card-${i}`)).toBeInTheDocument();
      }
    });

    it('empty column has correct structure with count=0', () => {
      renderWithDndContext(<StatusColumn status="open" count={0} />);

      // Verify header renders with count
      expect(screen.getByRole('heading', { name: 'Open' })).toBeInTheDocument();
      expect(screen.getByText('0')).toBeInTheDocument();

      // Verify content area exists and is empty
      const contentArea = screen.getByRole('list');
      expect(contentArea).toBeInTheDocument();
      expect(contentArea).toBeEmptyDOMElement();

      // Verify droppable ID is still set
      const droppable = document.querySelector('[data-droppable-id="open"]');
      expect(droppable).toBeInTheDocument();

      // Verify accessibility label uses plural for 0
      expect(screen.getByLabelText('0 issues')).toBeInTheDocument();
    });

    it('renders with 50+ child elements without breaking layout', () => {
      const manyChildren = Array.from({ length: 55 }, (_, i) => (
        <div key={i} data-testid={`item-${i}`}>
          Item {i}
        </div>
      ));

      renderWithDndContext(
        <StatusColumn status="in_progress" count={55}>
          {manyChildren}
        </StatusColumn>
      );

      // Verify component structure is intact
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
      expect(screen.getByText('55')).toBeInTheDocument();

      // Content area should contain all children
      const contentArea = screen.getByRole('list');
      expect(contentArea).toBeInTheDocument();
      expect(contentArea.children).toHaveLength(55);

      // Verify first, middle, and last items
      expect(screen.getByTestId('item-0')).toBeInTheDocument();
      expect(screen.getByTestId('item-27')).toBeInTheDocument();
      expect(screen.getByTestId('item-54')).toBeInTheDocument();

      // Verify section structure remains valid
      const section = screen.getByRole('region', { name: 'In Progress issues' });
      expect(section).toBeInTheDocument();
      expect(section).toContainElement(contentArea);
    });
  });
});

describe('formatStatusLabel', () => {
  it('formats open correctly', () => {
    expect(formatStatusLabel('open')).toBe('Open');
  });

  it('formats in_progress correctly', () => {
    expect(formatStatusLabel('in_progress')).toBe('In Progress');
  });

  it('formats closed correctly', () => {
    expect(formatStatusLabel('closed')).toBe('Closed');
  });

  it('formats blocked correctly', () => {
    expect(formatStatusLabel('blocked')).toBe('Blocked');
  });

  it('formats deferred correctly', () => {
    expect(formatStatusLabel('deferred')).toBe('Deferred');
  });

  it('formats custom snake_case status', () => {
    expect(formatStatusLabel('custom_status')).toBe('Custom Status');
  });

  it('formats multi-word snake_case status', () => {
    expect(formatStatusLabel('some_long_custom_status')).toBe('Some Long Custom Status');
  });

  it('handles single character', () => {
    expect(formatStatusLabel('a')).toBe('A');
  });

  it('handles uppercase input by converting to title case', () => {
    expect(formatStatusLabel('OPEN')).toBe('Open');
    expect(formatStatusLabel('IN_PROGRESS')).toBe('In Progress');
  });
});

describe('getStatusColor', () => {
  it('returns open status color', () => {
    expect(getStatusColor('open')).toBe('var(--color-status-open)');
  });

  it('returns in_progress status color', () => {
    expect(getStatusColor('in_progress')).toBe('var(--color-status-in-progress)');
  });

  it('returns closed status color', () => {
    expect(getStatusColor('closed')).toBe('var(--color-status-closed)');
  });

  it('returns default color for unknown status', () => {
    expect(getStatusColor('blocked')).toBe('var(--color-text-secondary)');
    expect(getStatusColor('custom')).toBe('var(--color-text-secondary)');
    expect(getStatusColor('deferred')).toBe('var(--color-text-secondary)');
  });
});
