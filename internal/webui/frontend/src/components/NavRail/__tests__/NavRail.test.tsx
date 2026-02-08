/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for NavRail component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';

import '@testing-library/jest-dom';
import { NavRail } from '../NavRail';

describe('NavRail', () => {
  describe('rendering', () => {
    it('renders a Kanban button', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText('Kanban')).toBeInTheDocument();
    });

    it('renders a Settings button', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText('Settings')).toBeInTheDocument();
    });

    it('does not render a Monitor button', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText('Monitor')).not.toBeInTheDocument();
    });

    it('renders exactly two navigation buttons', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole('button');
      expect(buttons).toHaveLength(2);
    });

    it('renders tooltips for each button', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByRole('tooltip', { name: 'Kanban' })).toBeInTheDocument();
      expect(screen.getByRole('tooltip', { name: 'Settings' })).toBeInTheDocument();
    });

    it('has navigation landmark with aria-label', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument();
    });

    it('marks active view with data-active attribute', () => {
      render(<NavRail activeView="settings" onChange={() => {}} />);

      const settingsButton = screen.getByLabelText('Settings');
      const kanbanButton = screen.getByLabelText('Kanban');

      expect(settingsButton).toHaveAttribute('data-active');
      expect(kanbanButton).not.toHaveAttribute('data-active');
    });

    it('marks kanban as active when activeView is kanban', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const kanbanButton = screen.getByLabelText('Kanban');
      const settingsButton = screen.getByLabelText('Settings');

      expect(kanbanButton).toHaveAttribute('data-active');
      expect(settingsButton).not.toHaveAttribute('data-active');
    });

    it('applies custom className', () => {
      render(<NavRail activeView="kanban" onChange={() => {}} className="custom-class" />);

      expect(screen.getByRole('navigation')).toHaveClass('custom-class');
    });
  });

  describe('interactions', () => {
    it('calls onChange with "kanban" when Kanban button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="settings" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText('Kanban'));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith('kanban');
    });

    it('calls onChange with "settings" when Settings button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText('Settings'));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith('settings');
    });

    it('calls onChange when clicking already active button', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText('Kanban'));

      expect(onChange).toHaveBeenCalledWith('kanban');
    });
  });
});
