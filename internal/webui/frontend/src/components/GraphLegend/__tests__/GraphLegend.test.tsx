/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GraphLegend component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { GraphLegend } from '../GraphLegend';

describe('GraphLegend', () => {
  describe('rendering', () => {
    it('renders without crashing', () => {
      render(<GraphLegend />);
      expect(screen.getByTestId('graph-legend')).toBeInTheDocument();
    });

    it('renders as aside element', () => {
      render(<GraphLegend />);
      const legend = screen.getByTestId('graph-legend');
      expect(legend.tagName).toBe('ASIDE');
    });

    it('renders "Legend" text in header', () => {
      render(<GraphLegend />);
      expect(screen.getByText('Legend')).toBeInTheDocument();
    });

    it('renders collapsed by default', () => {
      render(<GraphLegend />);
      expect(screen.queryByText('Priority')).not.toBeInTheDocument();
    });
  });

  describe('collapse/expand behavior', () => {
    it('shows content when collapsed is false', () => {
      render(<GraphLegend collapsed={false} />);
      expect(screen.getByText('Priority')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
      expect(screen.getByText('Edges')).toBeInTheDocument();
    });

    it('hides content when collapsed is true', () => {
      render(<GraphLegend collapsed={true} />);
      expect(screen.queryByText('Priority')).not.toBeInTheDocument();
    });

    it('calls onToggle when header is clicked', () => {
      const onToggle = vi.fn();
      render(<GraphLegend onToggle={onToggle} />);
      fireEvent.click(screen.getByRole('button'));
      expect(onToggle).toHaveBeenCalledTimes(1);
    });

    it('header button has correct aria-expanded when collapsed', () => {
      render(<GraphLegend collapsed={true} />);
      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('aria-expanded', 'false');
    });

    it('header button has correct aria-expanded when expanded', () => {
      render(<GraphLegend collapsed={false} />);
      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('aria-expanded', 'true');
    });

    it('header button has aria-controls pointing to content', () => {
      render(<GraphLegend />);
      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('aria-controls', 'legend-content');
    });
  });

  describe('priority items', () => {
    it('shows all priority levels when expanded', () => {
      render(<GraphLegend collapsed={false} />);
      expect(screen.getByText('P0 - Critical')).toBeInTheDocument();
      expect(screen.getByText('P1 - High')).toBeInTheDocument();
      expect(screen.getByText('P2 - Medium')).toBeInTheDocument();
      expect(screen.getByText('P3 - Normal')).toBeInTheDocument();
      expect(screen.getByText('P4 - Low')).toBeInTheDocument();
    });

    it('renders priority color swatches', () => {
      const { container } = render(<GraphLegend collapsed={false} />);
      const swatches = container.querySelectorAll('dt[aria-hidden="true"]');
      // 5 priority + 4 status + 2 edge = 11 swatches
      expect(swatches.length).toBeGreaterThanOrEqual(5);
    });
  });

  describe('status items', () => {
    it('shows all status states when expanded', () => {
      render(<GraphLegend collapsed={false} />);
      expect(screen.getByText('Open')).toBeInTheDocument();
      expect(screen.getByText('In Progress')).toBeInTheDocument();
      expect(screen.getByText('Blocked')).toBeInTheDocument();
      expect(screen.getByText('Closed')).toBeInTheDocument();
    });
  });

  describe('edge items', () => {
    it('shows both edge types when expanded', () => {
      render(<GraphLegend collapsed={false} />);
      expect(screen.getByText('Blocking')).toBeInTheDocument();
      expect(screen.getByText('Dependency')).toBeInTheDocument();
    });

    it('renders edge swatches with correct data-style attributes', () => {
      const { container } = render(<GraphLegend collapsed={false} />);
      const dashedSwatch = container.querySelector('[data-style="dashed"]');
      const solidSwatch = container.querySelector('[data-style="solid"]');
      expect(dashedSwatch).toBeInTheDocument();
      expect(solidSwatch).toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies className prop to root element', () => {
      const { container } = render(<GraphLegend className="custom-class" />);
      const root = container.firstChild as HTMLElement;
      expect(root).toHaveClass('custom-class');
    });

    it('default collapsed value is true', () => {
      render(<GraphLegend />);
      expect(screen.queryByText('Priority')).not.toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has data-testid attribute', () => {
      render(<GraphLegend />);
      expect(screen.getByTestId('graph-legend')).toBeInTheDocument();
    });

    it('header is a button element for keyboard accessibility', () => {
      render(<GraphLegend />);
      const button = screen.getByRole('button');
      expect(button).toHaveTextContent('Legend');
    });

    it('swatches have aria-hidden attribute', () => {
      const { container } = render(<GraphLegend collapsed={false} />);
      const swatches = container.querySelectorAll('dt[aria-hidden="true"]');
      expect(swatches.length).toBeGreaterThan(0);
    });

    it('content has id matching aria-controls', () => {
      const { container } = render(<GraphLegend collapsed={false} />);
      const content = container.querySelector('#legend-content');
      expect(content).toBeInTheDocument();
    });
  });

  describe('chevron rotation', () => {
    it('chevron has data-collapsed="true" when collapsed', () => {
      const { container } = render(<GraphLegend collapsed={true} />);
      const chevron = container.querySelector('[data-collapsed="true"]');
      expect(chevron).toBeInTheDocument();
    });

    it('chevron has data-collapsed="false" when expanded', () => {
      const { container } = render(<GraphLegend collapsed={false} />);
      const chevron = container.querySelector('[data-collapsed="false"]');
      expect(chevron).toBeInTheDocument();
    });
  });
});
