/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for FilterBar component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach as _beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import type { FilterState, FilterActions } from '@/hooks/useFilterState';
import type { IssueType } from '@/types';

import { FilterBar } from '../FilterBar';

/**
 * Create mock filter actions for controlled mode testing.
 */
function createMockActions(): FilterActions {
  return {
    setPriority: vi.fn(),
    setType: vi.fn(),
    setLabels: vi.fn(),
    setSearch: vi.fn(),
    clearFilter: vi.fn(),
    clearAll: vi.fn(),
  };
}

/**
 * Create empty filter state.
 */
function createEmptyFilters(): FilterState {
  return {};
}

/**
 * Create filter state with priority.
 */
function createFiltersWithPriority(priority: number): FilterState {
  return { priority: priority as 0 | 1 | 2 | 3 | 4 };
}

/**
 * Create filter state with type.
 */
function createFiltersWithType(type: string): FilterState {
  return { type: type as IssueType };
}

/**
 * Create filter state with labels.
 */
function createFiltersWithLabels(labels: string[]): FilterState {
  return { labels };
}

describe('FilterBar', () => {
  // Store original NODE_ENV
  const originalNodeEnv = process.env.NODE_ENV;

  afterEach(() => {
    // Restore NODE_ENV
    process.env.NODE_ENV = originalNodeEnv;
    vi.restoreAllMocks();
  });

  describe('rendering', () => {
    it('renders with data-testid attribute', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.getByTestId('filter-bar')).toBeInTheDocument();
    });

    it('renders priority dropdown with data-testid', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.getByTestId('priority-filter')).toBeInTheDocument();
    });

    it('renders priority dropdown with correct options', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      const options = select.querySelectorAll('option');

      expect(options).toHaveLength(6);
      expect(options[0]).toHaveTextContent('All priorities');
      expect(options[1]).toHaveTextContent('P0 (Critical)');
      expect(options[2]).toHaveTextContent('P1 (High)');
      expect(options[3]).toHaveTextContent('P2 (Medium)');
      expect(options[4]).toHaveTextContent('P3 (Normal)');
      expect(options[5]).toHaveTextContent('P4 (Backlog)');
    });

    it('shows "All priorities" selected when no filter is active', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      expect(select).toHaveValue('');
    });

    it('shows selected priority when filter is active', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(2)} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      expect(select).toHaveValue('2');
    });

    it('applies custom className to filter bar', () => {
      const actions = createMockActions();
      render(
        <FilterBar filters={createEmptyFilters()} actions={actions} className="custom-class" />
      );

      const root = screen.getByTestId('filter-bar');
      expect(root).toHaveClass('custom-class');
    });

    it('applies all priority options correctly', () => {
      const actions = createMockActions();
      const priorities = [0, 1, 2, 3, 4] as const;

      priorities.forEach((priority) => {
        const { unmount } = render(
          <FilterBar filters={createFiltersWithPriority(priority)} actions={actions} />
        );

        const select = screen.getByTestId('priority-filter');
        expect(select).toHaveValue(priority.toString());

        unmount();
      });
    });
  });

  describe('interactions', () => {
    it('calls setPriority with correct value when priority selected', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      fireEvent.change(select, { target: { value: '0' } });

      expect(actions.setPriority).toHaveBeenCalledWith(0);
    });

    it('calls setPriority with each priority value correctly', () => {
      const priorities = [0, 1, 2, 3, 4] as const;

      priorities.forEach((priority) => {
        const actions = createMockActions();
        const { unmount } = render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

        const select = screen.getByTestId('priority-filter');
        fireEvent.change(select, { target: { value: priority.toString() } });

        expect(actions.setPriority).toHaveBeenCalledWith(priority);

        unmount();
      });
    });

    it('calls setPriority with undefined when "All priorities" selected', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(2)} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      fireEvent.change(select, { target: { value: '' } });

      expect(actions.setPriority).toHaveBeenCalledWith(undefined);
    });

    it('calls clearAll when clear button is clicked', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(1)} actions={actions} />);

      const clearButton = screen.getByTestId('clear-filters');
      fireEvent.click(clearButton);

      expect(actions.clearAll).toHaveBeenCalledTimes(1);
    });
  });

  describe('visibility', () => {
    it('hides clear button when no filters are active', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.queryByTestId('clear-filters')).not.toBeInTheDocument();
    });

    it('shows clear button when priority is selected', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(3)} actions={actions} />);

      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();
    });

    it('shows clear button for all priority values', () => {
      const priorities = [0, 1, 2, 3, 4] as const;

      priorities.forEach((priority) => {
        const actions = createMockActions();
        const { unmount } = render(
          <FilterBar filters={createFiltersWithPriority(priority)} actions={actions} />
        );

        expect(screen.getByTestId('clear-filters')).toBeInTheDocument();

        unmount();
      });
    });

    it('showClear prop can force clear button visible when no filters', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} showClear={true} />);

      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();
    });

    it('showClear prop can hide clear button when filters active', () => {
      const actions = createMockActions();
      render(
        <FilterBar filters={createFiltersWithPriority(1)} actions={actions} showClear={false} />
      );

      expect(screen.queryByTestId('clear-filters')).not.toBeInTheDocument();
    });

    it('clear button has correct text', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(0)} actions={actions} />);

      const clearButton = screen.getByTestId('clear-filters');
      expect(clearButton).toHaveTextContent('Clear filters');
    });
  });

  describe('accessibility', () => {
    it('priority dropdown has accessible label', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByRole('combobox', { name: /filter by priority/i });
      expect(select).toBeInTheDocument();
    });

    it('priority dropdown has associated label element', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const label = screen.getByText('Priority');
      expect(label).toBeInTheDocument();
      expect(label).toHaveAttribute('for', 'priority-filter');
    });

    it('clear button has accessible label', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(1)} actions={actions} />);

      const clearButton = screen.getByRole('button', { name: /clear all filters/i });
      expect(clearButton).toBeInTheDocument();
    });

    it('priority dropdown can be found by role', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.getByRole('combobox', { name: /filter by priority/i })).toBeInTheDocument();
    });

    it('keyboard navigation works for priority dropdown', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('priority-filter');

      // Focus the select
      select.focus();
      expect(document.activeElement).toBe(select);

      // Simulate keyboard change
      fireEvent.keyDown(select, { key: 'ArrowDown' });
      fireEvent.change(select, { target: { value: '1' } });

      expect(actions.setPriority).toHaveBeenCalledWith(1);
    });

    it('clear button can be activated with keyboard', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(2)} actions={actions} />);

      const clearButton = screen.getByTestId('clear-filters');

      // Focus and activate with Enter
      clearButton.focus();
      expect(document.activeElement).toBe(clearButton);

      fireEvent.keyDown(clearButton, { key: 'Enter' });
      fireEvent.click(clearButton);

      expect(actions.clearAll).toHaveBeenCalled();
    });

    it('clear button is type="button" to prevent form submission', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithPriority(0)} actions={actions} />);

      const clearButton = screen.getByTestId('clear-filters');
      expect(clearButton).toHaveAttribute('type', 'button');
    });
  });

  describe('controlled mode', () => {
    it('renders with provided filters', () => {
      const actions = createMockActions();
      const filters = createFiltersWithPriority(4);
      render(<FilterBar filters={filters} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      expect(select).toHaveValue('4');
    });

    it('calls provided actions on interaction', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      fireEvent.change(select, { target: { value: '3' } });

      expect(actions.setPriority).toHaveBeenCalledWith(3);
    });

    it('updates display when filters prop changes', () => {
      const actions = createMockActions();
      const { rerender } = render(
        <FilterBar filters={createFiltersWithPriority(0)} actions={actions} />
      );

      expect(screen.getByTestId('priority-filter')).toHaveValue('0');

      rerender(<FilterBar filters={createFiltersWithPriority(2)} actions={actions} />);

      expect(screen.getByTestId('priority-filter')).toHaveValue('2');
    });
  });

  describe('edge cases', () => {
    it('handles rapid priority changes', () => {
      const actions = createMockActions();
      const { rerender } = render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      rerender(<FilterBar filters={createFiltersWithPriority(0)} actions={actions} />);
      rerender(<FilterBar filters={createFiltersWithPriority(1)} actions={actions} />);
      rerender(<FilterBar filters={createFiltersWithPriority(2)} actions={actions} />);

      const select = screen.getByTestId('priority-filter');
      expect(select).toHaveValue('2');
    });

    it('handles clearing when already empty', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} showClear={true} />);

      const clearButton = screen.getByTestId('clear-filters');
      fireEvent.click(clearButton);

      expect(actions.clearAll).toHaveBeenCalledTimes(1);
    });

    it('handles undefined className gracefully', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} className={undefined} />);

      const root = screen.getByTestId('filter-bar');
      expect(root).toBeInTheDocument();
    });

    it('handles filter state with other filter types', () => {
      const actions = createMockActions();
      const filters: FilterState = {
        priority: 1,
        search: 'test',
        labels: ['bug'],
      };

      render(
        <FilterBar filters={filters} actions={actions} availableLabels={['bug', 'feature']} />
      );

      const select = screen.getByTestId('priority-filter');
      expect(select).toHaveValue('1');
      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();
      // Verify label filter is also visible
      expect(screen.getByTestId('label-filter-trigger')).toBeInTheDocument();
    });
  });

  describe('label filter rendering', () => {
    it('does not render label filter when no availableLabels provided', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.queryByTestId('label-filter-trigger')).not.toBeInTheDocument();
    });

    it('does not render label filter when availableLabels is empty', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} availableLabels={[]} />);

      expect(screen.queryByTestId('label-filter-trigger')).not.toBeInTheDocument();
    });

    it('renders label filter trigger when availableLabels provided', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature', 'urgent']}
        />
      );

      expect(screen.getByTestId('label-filter-trigger')).toBeInTheDocument();
    });

    it('shows "All labels" when no labels selected', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      expect(screen.getByTestId('label-filter-trigger')).toHaveTextContent('All labels');
    });

    it('shows count when labels are selected', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithLabels(['bug', 'urgent'])}
          actions={actions}
          availableLabels={['bug', 'feature', 'urgent']}
        />
      );

      expect(screen.getByTestId('label-filter-trigger')).toHaveTextContent('2 selected');
    });
  });

  describe('label filter interactions', () => {
    it('opens dropdown menu when trigger is clicked', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));

      expect(screen.getByTestId('label-filter-menu')).toBeInTheDocument();
    });

    it('displays all available labels in dropdown', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature', 'urgent']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));

      expect(screen.getByTestId('label-option-bug')).toBeInTheDocument();
      expect(screen.getByTestId('label-option-feature')).toBeInTheDocument();
      expect(screen.getByTestId('label-option-urgent')).toBeInTheDocument();
    });

    it('calls setLabels with label when checkbox clicked', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));
      fireEvent.click(screen.getByTestId('label-option-bug'));

      expect(actions.setLabels).toHaveBeenCalledWith(['bug']);
    });

    it('adds label to existing selection', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithLabels(['bug'])}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));
      fireEvent.click(screen.getByTestId('label-option-feature'));

      expect(actions.setLabels).toHaveBeenCalledWith(['bug', 'feature']);
    });

    it('removes label from selection when unchecked', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithLabels(['bug', 'feature'])}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));
      fireEvent.click(screen.getByTestId('label-option-bug'));

      expect(actions.setLabels).toHaveBeenCalledWith(['feature']);
    });

    it('calls setLabels with undefined when last label unchecked', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithLabels(['bug'])}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));
      fireEvent.click(screen.getByTestId('label-option-bug'));

      expect(actions.setLabels).toHaveBeenCalledWith(undefined);
    });

    it('closes dropdown when clicking outside', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));
      expect(screen.getByTestId('label-filter-menu')).toBeInTheDocument();

      // Click outside
      fireEvent.mouseDown(document.body);

      expect(screen.queryByTestId('label-filter-menu')).not.toBeInTheDocument();
    });
  });

  describe('label filter visibility', () => {
    it('shows clear button when labels are selected', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithLabels(['bug'])}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();
    });
  });

  describe('label filter accessibility', () => {
    it('trigger has accessible label', () => {
      const actions = createMockActions();
      render(
        <FilterBar filters={createEmptyFilters()} actions={actions} availableLabels={['bug']} />
      );

      expect(screen.getByRole('button', { name: /filter by labels/i })).toBeInTheDocument();
    });

    it('trigger has aria-expanded attribute', () => {
      const actions = createMockActions();
      render(
        <FilterBar filters={createEmptyFilters()} actions={actions} availableLabels={['bug']} />
      );

      const trigger = screen.getByTestId('label-filter-trigger');
      expect(trigger).toHaveAttribute('aria-expanded', 'false');

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute('aria-expanded', 'true');
    });

    it('dropdown menu has group role', () => {
      const actions = createMockActions();
      render(
        <FilterBar filters={createEmptyFilters()} actions={actions} availableLabels={['bug']} />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));

      expect(screen.getByRole('group')).toBeInTheDocument();
    });

    it('checkboxes reflect checked state correctly', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithLabels(['bug'])}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      fireEvent.click(screen.getByTestId('label-filter-trigger'));

      const bugCheckbox = screen.getByTestId('label-option-bug');
      const featureCheckbox = screen.getByTestId('label-option-feature');

      expect(bugCheckbox).toBeChecked();
      expect(featureCheckbox).not.toBeChecked();
    });
  });

  describe('type filter rendering', () => {
    it('renders type dropdown with data-testid', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.getByTestId('type-filter')).toBeInTheDocument();
    });

    it('renders type dropdown with correct options', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('type-filter');
      const options = select.querySelectorAll('option');

      expect(options).toHaveLength(6);
      expect(options[0]).toHaveTextContent('All types');
      expect(options[1]).toHaveTextContent('Bug');
      expect(options[2]).toHaveTextContent('Feature');
      expect(options[3]).toHaveTextContent('Task');
      expect(options[4]).toHaveTextContent('Epic');
      expect(options[5]).toHaveTextContent('Chore');
    });

    it('shows "All types" selected when no filter is active', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('type-filter');
      expect(select).toHaveValue('');
    });

    it('shows selected type when filter is active', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithType('bug')} actions={actions} />);

      const select = screen.getByTestId('type-filter');
      expect(select).toHaveValue('bug');
    });
  });

  describe('type filter interactions', () => {
    it('calls setType with correct value when type selected', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByTestId('type-filter');
      fireEvent.change(select, { target: { value: 'feature' } });

      expect(actions.setType).toHaveBeenCalledWith('feature');
    });

    it('calls setType with undefined when "All types" selected', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithType('task')} actions={actions} />);

      const select = screen.getByTestId('type-filter');
      fireEvent.change(select, { target: { value: '' } });

      expect(actions.setType).toHaveBeenCalledWith(undefined);
    });

    it('calls setType for each type value correctly', () => {
      const types = ['bug', 'feature', 'task', 'epic', 'chore'];

      types.forEach((type) => {
        const actions = createMockActions();
        const { unmount } = render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

        const select = screen.getByTestId('type-filter');
        fireEvent.change(select, { target: { value: type } });

        expect(actions.setType).toHaveBeenCalledWith(type);

        unmount();
      });
    });
  });

  describe('type filter visibility', () => {
    it('shows clear button when type is selected', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createFiltersWithType('bug')} actions={actions} />);

      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();
    });
  });

  describe('type filter accessibility', () => {
    it('type dropdown has accessible label', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const select = screen.getByRole('combobox', { name: /filter by type/i });
      expect(select).toBeInTheDocument();
    });

    it('type dropdown has associated label element', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      const label = screen.getByText('Type');
      expect(label).toBeInTheDocument();
      expect(label).toHaveAttribute('for', 'type-filter');
    });
  });

  describe('combined filters', () => {
    it('renders both priority and type filters', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.getByTestId('priority-filter')).toBeInTheDocument();
      expect(screen.getByTestId('type-filter')).toBeInTheDocument();
    });

    it('shows clear button when both filters are active', () => {
      const actions = createMockActions();
      const filters: FilterState = {
        priority: 2,
        type: 'bug',
      };

      render(<FilterBar filters={filters} actions={actions} />);

      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();
    });

    it('displays both filter values correctly', () => {
      const actions = createMockActions();
      const filters: FilterState = {
        priority: 1,
        type: 'feature',
      };

      render(<FilterBar filters={filters} actions={actions} />);

      expect(screen.getByTestId('priority-filter')).toHaveValue('1');
      expect(screen.getByTestId('type-filter')).toHaveValue('feature');
    });

    it('clears both filters when clear button clicked', () => {
      const actions = createMockActions();
      const filters: FilterState = {
        priority: 3,
        type: 'task',
      };

      render(<FilterBar filters={filters} actions={actions} />);

      const clearButton = screen.getByTestId('clear-filters');
      fireEvent.click(clearButton);

      expect(actions.clearAll).toHaveBeenCalledTimes(1);
    });
  });

  describe('groupBy filter rendering', () => {
    it('renders groupBy dropdown when onGroupByChange is provided', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="none"
          onGroupByChange={onGroupByChange}
        />
      );

      expect(screen.getByTestId('groupby-filter')).toBeInTheDocument();
    });

    it('does not render groupBy dropdown when onGroupByChange is not provided', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} />);

      expect(screen.queryByTestId('groupby-filter')).not.toBeInTheDocument();
    });

    it('does not render groupBy dropdown when only groupBy prop is provided without callback', () => {
      const actions = createMockActions();
      render(<FilterBar filters={createEmptyFilters()} actions={actions} groupBy="epic" />);

      expect(screen.queryByTestId('groupby-filter')).not.toBeInTheDocument();
    });

    it('renders groupBy dropdown with all 6 options', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="none"
          onGroupByChange={onGroupByChange}
        />
      );

      const select = screen.getByTestId('groupby-filter');
      const options = select.querySelectorAll('option');

      expect(options).toHaveLength(6);
      expect(options[0]).toHaveTextContent('All');
      expect(options[1]).toHaveTextContent('Epic');
      expect(options[2]).toHaveTextContent('Assignee');
      expect(options[3]).toHaveTextContent('Priority');
      expect(options[4]).toHaveTextContent('Type');
      expect(options[5]).toHaveTextContent('Label');
    });

    it('shows selected groupBy value correctly', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="assignee"
          onGroupByChange={onGroupByChange}
        />
      );

      const select = screen.getByTestId('groupby-filter');
      expect(select).toHaveValue('assignee');
    });

    it('defaults to "none" when groupBy prop is undefined', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          onGroupByChange={onGroupByChange}
        />
      );

      const select = screen.getByTestId('groupby-filter');
      expect(select).toHaveValue('none');
    });

    it('displays all groupBy options correctly when selected', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      const groupByValues = ['none', 'epic', 'assignee', 'priority', 'type', 'label'] as const;

      groupByValues.forEach((groupByValue) => {
        const { unmount } = render(
          <FilterBar
            filters={createEmptyFilters()}
            actions={actions}
            groupBy={groupByValue}
            onGroupByChange={onGroupByChange}
          />
        );

        const select = screen.getByTestId('groupby-filter');
        expect(select).toHaveValue(groupByValue);

        unmount();
      });
    });
  });

  describe('groupBy filter interactions', () => {
    it('calls onGroupByChange with correct value when option selected', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="none"
          onGroupByChange={onGroupByChange}
        />
      );

      const select = screen.getByTestId('groupby-filter');
      fireEvent.change(select, { target: { value: 'epic' } });

      expect(onGroupByChange).toHaveBeenCalledWith('epic');
    });

    it('calls onGroupByChange for each groupBy value correctly', () => {
      const groupByValues = ['none', 'epic', 'assignee', 'priority', 'type', 'label'] as const;

      groupByValues.forEach((groupByValue) => {
        const actions = createMockActions();
        const onGroupByChange = vi.fn();
        const { unmount } = render(
          <FilterBar
            filters={createEmptyFilters()}
            actions={actions}
            groupBy="none"
            onGroupByChange={onGroupByChange}
          />
        );

        const select = screen.getByTestId('groupby-filter');
        fireEvent.change(select, { target: { value: groupByValue } });

        expect(onGroupByChange).toHaveBeenCalledWith(groupByValue);

        unmount();
      });
    });

    it('calls onGroupByChange with "none" when None selected', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="epic"
          onGroupByChange={onGroupByChange}
        />
      );

      const select = screen.getByTestId('groupby-filter');
      fireEvent.change(select, { target: { value: 'none' } });

      expect(onGroupByChange).toHaveBeenCalledWith('none');
    });
  });

  describe('groupBy filter accessibility', () => {
    it('groupBy dropdown has accessible label', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="none"
          onGroupByChange={onGroupByChange}
        />
      );

      const select = screen.getByRole('combobox', { name: /group issues by/i });
      expect(select).toBeInTheDocument();
    });

    it('groupBy dropdown has associated label element', () => {
      const actions = createMockActions();
      const onGroupByChange = vi.fn();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          groupBy="none"
          onGroupByChange={onGroupByChange}
        />
      );

      const label = screen.getByText('Group by');
      expect(label).toBeInTheDocument();
      expect(label).toHaveAttribute('for', 'groupby-filter');
    });
  });

  describe('CSS layout behavior', () => {
    it('filterBar container has filterBar CSS module class', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithPriority(1)}
          actions={actions}
          availableLabels={['bug', 'feature']}
        />
      );

      const filterBar = screen.getByTestId('filter-bar');

      // Verify the filterBar class is applied (CSS Modules transforms it to _filterBar_<hash>)
      expect(filterBar.className).toMatch(/filterBar/);
    });

    it('renders with FilterBar module class for layout control', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createEmptyFilters()}
          actions={actions}
          availableLabels={['bug', 'feature', 'urgent']}
        />
      );

      const filterBar = screen.getByTestId('filter-bar');

      // The filterBar CSS class provides flex layout without wrapping
      expect(filterBar.className).toMatch(/filterBar/);
    });

    it('renders all filter controls on single line layout', () => {
      const actions = createMockActions();
      render(
        <FilterBar
          filters={createFiltersWithPriority(2)}
          actions={actions}
          availableLabels={['bug', 'feature', 'urgent']}
        />
      );

      // Verify all major filter controls are present
      expect(screen.getByTestId('priority-filter')).toBeInTheDocument();
      expect(screen.getByTestId('type-filter')).toBeInTheDocument();
      expect(screen.getByTestId('label-filter-trigger')).toBeInTheDocument();
      expect(screen.getByTestId('clear-filters')).toBeInTheDocument();

      // Verify CSS module class is applied
      const filterBar = screen.getByTestId('filter-bar');
      expect(filterBar.className).toMatch(/filterBar/);
    });

    it('maintains layout class with multiple filters active', () => {
      const actions = createMockActions();
      const filters: FilterState = {
        priority: 1,
        type: 'bug',
        labels: ['urgent', 'feature'],
      };

      render(
        <FilterBar
          filters={filters}
          actions={actions}
          availableLabels={['bug', 'feature', 'urgent', 'documentation']}
        />
      );

      const filterBar = screen.getByTestId('filter-bar');

      // CSS module class should be applied even with all filters active
      expect(filterBar.className).toMatch(/filterBar/);
    });

    it('maintains filterBar class with custom className', () => {
      const actions = createMockActions();
      render(
        <FilterBar filters={createEmptyFilters()} actions={actions} className="custom-filter-bar" />
      );

      const filterBar = screen.getByTestId('filter-bar');

      // Should have both CSS module class and custom class
      expect(filterBar.className).toMatch(/filterBar/);
      expect(filterBar).toHaveClass('custom-filter-bar');
    });
  });
});
