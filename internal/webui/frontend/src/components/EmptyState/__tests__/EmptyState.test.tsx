/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EmptyState component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { EmptyState } from '../EmptyState';

describe('EmptyState', () => {
  describe('rendering', () => {
    it('renders with default no-results variant', () => {
      render(<EmptyState />);

      expect(screen.getByTestId('empty-state')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'No results found' })).toBeInTheDocument();
      expect(screen.getByText('Try adjusting your search or filter criteria.')).toBeInTheDocument();
    });

    it('renders with no-issues variant', () => {
      render(<EmptyState variant="no-issues" />);

      expect(screen.getByRole('heading', { name: 'No issues yet' })).toBeInTheDocument();
      expect(screen.getByText('Create your first issue to get started.')).toBeInTheDocument();
    });

    it('renders with custom variant', () => {
      render(<EmptyState variant="custom" />);

      expect(screen.getByRole('heading', { name: 'Nothing to show' })).toBeInTheDocument();
    });

    it('renders with status role for accessibility', () => {
      render(<EmptyState />);

      expect(screen.getByRole('status')).toBeInTheDocument();
    });

    it('renders default search icon', () => {
      const { container } = render(<EmptyState />);

      const svg = container.querySelector('svg');
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute('aria-hidden', 'true');
    });
  });

  describe('custom content', () => {
    it('renders custom title', () => {
      render(<EmptyState title="Custom Title" />);

      expect(screen.getByRole('heading', { name: 'Custom Title' })).toBeInTheDocument();
    });

    it('renders custom description', () => {
      render(<EmptyState description="Custom description text" />);

      expect(screen.getByText('Custom description text')).toBeInTheDocument();
    });

    it('renders custom title and description together', () => {
      render(<EmptyState title="My Title" description="My description" />);

      expect(screen.getByRole('heading', { name: 'My Title' })).toBeInTheDocument();
      expect(screen.getByText('My description')).toBeInTheDocument();
    });

    it('custom title overrides variant default', () => {
      render(<EmptyState variant="no-results" title="Override Title" />);

      expect(screen.getByRole('heading', { name: 'Override Title' })).toBeInTheDocument();
      expect(screen.queryByText('No results found')).not.toBeInTheDocument();
    });

    it('custom description overrides variant default', () => {
      render(<EmptyState variant="no-results" description="Override desc" />);

      expect(screen.getByText('Override desc')).toBeInTheDocument();
      expect(
        screen.queryByText('Try adjusting your search or filter criteria.')
      ).not.toBeInTheDocument();
    });

    it('renders custom icon', () => {
      const CustomIcon = () => <span data-testid="custom-icon">Icon</span>;
      render(<EmptyState icon={<CustomIcon />} />);

      expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
    });
  });

  describe('action', () => {
    it('renders action element', () => {
      render(<EmptyState action={<button>Clear filters</button>} />);

      expect(screen.getByRole('button', { name: 'Clear filters' })).toBeInTheDocument();
    });

    it('action button is clickable', () => {
      const handleClick = vi.fn();
      render(<EmptyState action={<button onClick={handleClick}>Click me</button>} />);

      fireEvent.click(screen.getByRole('button', { name: 'Click me' }));
      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('does not render action wrapper when action is not provided', () => {
      const { container } = render(<EmptyState />);

      // Action wrapper should not be in the DOM
      const actionElements = container.querySelectorAll('[class*="action"]');
      expect(actionElements.length).toBe(0);
    });

    it('renders complex action element', () => {
      render(
        <EmptyState
          action={
            <div>
              <button>Primary action</button>
              <button>Secondary action</button>
            </div>
          }
        />
      );

      expect(screen.getByRole('button', { name: 'Primary action' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Secondary action' })).toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies className prop to root element', () => {
      const { container } = render(<EmptyState className="custom-class" />);

      const root = container.querySelector('[data-testid="empty-state"]');
      expect(root).toHaveClass('custom-class');
    });

    it('applies data-variant attribute', () => {
      render(<EmptyState variant="no-issues" />);

      expect(screen.getByTestId('empty-state')).toHaveAttribute('data-variant', 'no-issues');
    });

    it('applies data-variant for custom variant', () => {
      render(<EmptyState variant="custom" />);

      expect(screen.getByTestId('empty-state')).toHaveAttribute('data-variant', 'custom');
    });
  });

  describe('edge cases', () => {
    it('renders empty description when variant has empty default', () => {
      render(<EmptyState variant="custom" />);

      // custom variant has empty description
      const description = screen.queryByText('Nothing to show');
      expect(description).not.toBeNull();
    });

    it('does not render description paragraph when description is empty', () => {
      // Custom variant has empty description by default
      const { container } = render(<EmptyState variant="custom" />);

      const paragraphs = container.querySelectorAll('p');
      expect(paragraphs.length).toBe(0);
    });

    it('renders with all optional props', () => {
      const CustomIcon = () => <span data-testid="custom-icon">Icon</span>;
      render(
        <EmptyState
          variant="custom"
          title="All props title"
          description="All props description"
          icon={<CustomIcon />}
          action={<button>Action</button>}
          className="custom-all-props"
        />
      );

      expect(screen.getByRole('heading', { name: 'All props title' })).toBeInTheDocument();
      expect(screen.getByText('All props description')).toBeInTheDocument();
      expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Action' })).toBeInTheDocument();
      expect(screen.getByTestId('empty-state')).toHaveClass('custom-all-props');
    });

    it('renders correctly when description is undefined', () => {
      render(<EmptyState title="Title only" description={undefined} />);

      expect(screen.getByRole('heading', { name: 'Title only' })).toBeInTheDocument();
      // Should still show default description
      expect(screen.getByText('Try adjusting your search or filter criteria.')).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has status role for screen readers', () => {
      render(<EmptyState />);

      const element = screen.getByRole('status');
      expect(element).toBeInTheDocument();
    });

    it('icon is hidden from screen readers', () => {
      const { container } = render(<EmptyState />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('aria-hidden', 'true');
    });

    it('title is a heading element', () => {
      render(<EmptyState title="Test heading" />);

      const heading = screen.getByRole('heading', { name: 'Test heading' });
      expect(heading.tagName).toBe('H3');
    });
  });
});
