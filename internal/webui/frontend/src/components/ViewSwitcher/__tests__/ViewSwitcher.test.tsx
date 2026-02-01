/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ViewSwitcher component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import { ViewSwitcher, DEFAULT_VIEW } from '../ViewSwitcher';

describe('ViewSwitcher', () => {
  describe('rendering', () => {
    it('renders all four view tabs', () => {
      render(<ViewSwitcher activeView="kanban" onChange={() => {}} />);

      expect(screen.getByTestId('view-tab-kanban')).toBeInTheDocument();
      expect(screen.getByTestId('view-tab-table')).toBeInTheDocument();
      expect(screen.getByTestId('view-tab-graph')).toBeInTheDocument();
      expect(screen.getByTestId('view-tab-monitor')).toBeInTheDocument();
    });

    it('renders with correct labels', () => {
      render(<ViewSwitcher activeView="kanban" onChange={() => {}} />);

      expect(screen.getByText('Kanban')).toBeInTheDocument();
      expect(screen.getByText('Table')).toBeInTheDocument();
      expect(screen.getByText('Graph')).toBeInTheDocument();
      expect(screen.getByText('Monitor')).toBeInTheDocument();
    });

    it('marks active tab with aria-selected=true', () => {
      render(<ViewSwitcher activeView="table" onChange={() => {}} />);

      expect(screen.getByTestId('view-tab-kanban')).toHaveAttribute(
        'aria-selected',
        'false'
      );
      expect(screen.getByTestId('view-tab-table')).toHaveAttribute(
        'aria-selected',
        'true'
      );
      expect(screen.getByTestId('view-tab-graph')).toHaveAttribute(
        'aria-selected',
        'false'
      );
    });

    it('applies active class to current tab', () => {
      render(<ViewSwitcher activeView="graph" onChange={() => {}} />);

      // CSS Modules mangles class names, so we check for partial match
      const graphTab = screen.getByTestId('view-tab-graph');
      const kanbanTab = screen.getByTestId('view-tab-kanban');

      expect(graphTab.className).toMatch(/active/);
      expect(kanbanTab.className).not.toMatch(/active/);
    });

    it('applies custom className', () => {
      render(
        <ViewSwitcher
          activeView="kanban"
          onChange={() => {}}
          className="custom-class"
        />
      );

      expect(screen.getByTestId('view-switcher')).toHaveClass('custom-class');
    });
  });

  describe('interactions', () => {
    it('calls onChange with view id when tab clicked', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByTestId('view-tab-table'));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith('table');
    });

    it('calls onChange when clicking already active tab', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByTestId('view-tab-kanban'));

      // It will still be called (no internal gating), parent can handle this
      expect(onChange).toHaveBeenCalledWith('kanban');
    });

    it('does not call onChange when disabled', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} disabled />);

      fireEvent.click(screen.getByTestId('view-tab-table'));

      expect(onChange).not.toHaveBeenCalled();
    });

    it('disables all tabs when disabled prop is true', () => {
      render(
        <ViewSwitcher activeView="kanban" onChange={() => {}} disabled />
      );

      expect(screen.getByTestId('view-tab-kanban')).toBeDisabled();
      expect(screen.getByTestId('view-tab-table')).toBeDisabled();
      expect(screen.getByTestId('view-tab-graph')).toBeDisabled();
      expect(screen.getByTestId('view-tab-monitor')).toBeDisabled();
    });
  });

  describe('keyboard navigation', () => {
    it('navigates to next tab with ArrowRight', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'ArrowRight' });

      expect(onChange).toHaveBeenCalledWith('table');
    });

    it('navigates to previous tab with ArrowLeft', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="table" onChange={onChange} />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'ArrowLeft' });

      expect(onChange).toHaveBeenCalledWith('kanban');
    });

    it('wraps around at the end', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="monitor" onChange={onChange} />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'ArrowRight' });

      expect(onChange).toHaveBeenCalledWith('kanban');
    });

    it('wraps around at the beginning', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'ArrowLeft' });

      expect(onChange).toHaveBeenCalledWith('monitor');
    });

    it('navigates to first tab with Home', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="graph" onChange={onChange} />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'Home' });

      expect(onChange).toHaveBeenCalledWith('kanban');
    });

    it('navigates to last tab with End', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'End' });

      expect(onChange).toHaveBeenCalledWith('monitor');
    });

    it('ignores keyboard navigation when disabled', () => {
      const onChange = vi.fn();
      render(<ViewSwitcher activeView="kanban" onChange={onChange} disabled />);

      const switcher = screen.getByTestId('view-switcher');
      fireEvent.keyDown(switcher, { key: 'ArrowRight' });

      expect(onChange).not.toHaveBeenCalled();
    });
  });

  describe('accessibility', () => {
    it('has role=tablist', () => {
      render(<ViewSwitcher activeView="kanban" onChange={() => {}} />);

      expect(screen.getByRole('tablist')).toBeInTheDocument();
    });

    it('has aria-label for the tab list', () => {
      render(<ViewSwitcher activeView="kanban" onChange={() => {}} />);

      expect(screen.getByRole('tablist')).toHaveAttribute(
        'aria-label',
        'View selector'
      );
    });

    it('tabs have role=tab', () => {
      render(<ViewSwitcher activeView="kanban" onChange={() => {}} />);

      const tabs = screen.getAllByRole('tab');
      expect(tabs).toHaveLength(4);
    });

    it('tabs have aria-controls pointing to main-content', () => {
      render(<ViewSwitcher activeView="kanban" onChange={() => {}} />);

      const tabs = screen.getAllByRole('tab');
      tabs.forEach((tab) => {
        expect(tab).toHaveAttribute('aria-controls', 'main-content');
      });
    });

    it('only active tab has tabIndex=0', () => {
      render(<ViewSwitcher activeView="table" onChange={() => {}} />);

      expect(screen.getByTestId('view-tab-kanban')).toHaveAttribute(
        'tabIndex',
        '-1'
      );
      expect(screen.getByTestId('view-tab-table')).toHaveAttribute(
        'tabIndex',
        '0'
      );
      expect(screen.getByTestId('view-tab-graph')).toHaveAttribute(
        'tabIndex',
        '-1'
      );
    });
  });

  describe('exports', () => {
    it('DEFAULT_VIEW is kanban', () => {
      expect(DEFAULT_VIEW).toBe('kanban');
    });
  });
});
