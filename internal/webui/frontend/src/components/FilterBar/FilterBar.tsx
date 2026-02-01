/**
 * FilterBar component.
 * UI component for filtering issues by priority and type.
 * Integrates with useFilterState hook for URL synchronization.
 */

import { useState, useCallback, useEffect, useRef } from 'react';
import type { Priority, IssueType } from '@/types';
import { type FilterState, type FilterActions, isEmptyFilter } from '@/hooks/useFilterState';
import styles from './FilterBar.module.css';

/**
 * Priority option for the dropdown.
 */
interface PriorityOption {
  /** Display label */
  label: string;
  /** Value (undefined = all) */
  value: Priority | undefined;
}

/**
 * Priority options for the dropdown.
 * Order: All, then P0-P4.
 */
const PRIORITY_OPTIONS: PriorityOption[] = [
  { label: 'All priorities', value: undefined },
  { label: 'P0 (Critical)', value: 0 },
  { label: 'P1 (High)', value: 1 },
  { label: 'P2 (Medium)', value: 2 },
  { label: 'P3 (Normal)', value: 3 },
  { label: 'P4 (Backlog)', value: 4 },
];

/**
 * Type option for the dropdown.
 */
interface TypeOption {
  /** Display label */
  label: string;
  /** Value (undefined = all) */
  value: IssueType | undefined;
}

/**
 * Type options for the dropdown.
 * Order: All, then known types in display order.
 */
const TYPE_OPTIONS: TypeOption[] = [
  { label: 'All types', value: undefined },
  { label: 'Bug', value: 'bug' },
  { label: 'Feature', value: 'feature' },
  { label: 'Task', value: 'task' },
  { label: 'Epic', value: 'epic' },
  { label: 'Chore', value: 'chore' },
];

/**
 * Group by option for swim lane grouping.
 */
export type GroupByOption = 'none' | 'epic' | 'assignee' | 'priority' | 'type' | 'label';

/**
 * Group by option for the dropdown.
 */
interface GroupByOptionItem {
  /** Display label */
  label: string;
  /** Value */
  value: GroupByOption;
}

/**
 * Group by options for the dropdown.
 */
const GROUP_BY_OPTIONS: GroupByOptionItem[] = [
  { label: 'None', value: 'none' },
  { label: 'Epic', value: 'epic' },
  { label: 'Assignee', value: 'assignee' },
  { label: 'Priority', value: 'priority' },
  { label: 'Type', value: 'type' },
  { label: 'Label', value: 'label' },
];

/**
 * Props for the FilterBar component.
 */
export interface FilterBarProps {
  /** Current filter state */
  filters: FilterState;
  /** Filter actions */
  actions: FilterActions;
  /** Additional CSS class name */
  className?: string;
  /** Show/hide clear button (defaults to auto-detect from filters) */
  showClear?: boolean;
  /** Available labels for the label filter dropdown */
  availableLabels?: string[];
  /** Current group by selection */
  groupBy?: GroupByOption;
  /** Callback when group by changes */
  onGroupByChange?: (value: GroupByOption) => void;
}

/**
 * FilterBar renders a filter bar with priority and type dropdowns.
 *
 * @example
 * ```tsx
 * const [filters, actions] = useFilterState();
 * <FilterBar filters={filters} actions={actions} />
 * ```
 */
export function FilterBar({
  filters,
  actions,
  className,
  showClear,
  availableLabels,
  groupBy,
  onGroupByChange,
}: FilterBarProps): JSX.Element {
  // Determine if clear button should be visible
  const hasActiveFilters = !isEmptyFilter(filters);
  const shouldShowClear = showClear ?? hasActiveFilters;

  // Handle priority change
  const handlePriorityChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const value = event.target.value;
      if (value === '') {
        actions.setPriority(undefined);
      } else {
        const priority = parseInt(value, 10) as Priority;
        actions.setPriority(priority);
      }
    },
    [actions]
  );

  // Handle type change
  const handleTypeChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const value = event.target.value;
      if (value === '') {
        actions.setType(undefined);
      } else {
        actions.setType(value as IssueType);
      }
    },
    [actions]
  );

  // Handle clear all
  const handleClearAll = useCallback(() => {
    actions.clearAll();
  }, [actions]);

  // Label dropdown state
  const [labelDropdownOpen, setLabelDropdownOpen] = useState(false);
  const labelDropdownRef = useRef<HTMLDivElement>(null);

  // Close dropdown on outside click
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (labelDropdownRef.current && !labelDropdownRef.current.contains(event.target as Node)) {
        setLabelDropdownOpen(false);
      }
    }
    if (labelDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [labelDropdownOpen]);

  // Handle label toggle
  const handleLabelToggle = useCallback(
    (label: string) => {
      const current = filters.labels ?? [];
      const newLabels = current.includes(label)
        ? current.filter((l) => l !== label)
        : [...current, label];
      actions.setLabels(newLabels.length > 0 ? newLabels : undefined);
    },
    [filters.labels, actions]
  );

  // Toggle label dropdown
  const toggleLabelDropdown = useCallback(() => {
    setLabelDropdownOpen((prev) => !prev);
  }, []);

  // Handle group by change
  const handleGroupByChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const value = event.target.value as GroupByOption;
      onGroupByChange?.(value);
    },
    [onGroupByChange]
  );

  const rootClassName = className ? `${styles.filterBar} ${className}` : styles.filterBar;

  return (
    <div className={rootClassName} data-testid="filter-bar">
      <div className={styles.filters}>
        {/* Priority dropdown */}
        <div className={styles.filterGroup}>
          <label htmlFor="priority-filter" className={styles.label}>
            Priority
          </label>
          <select
            id="priority-filter"
            className={styles.select}
            value={filters.priority ?? ''}
            onChange={handlePriorityChange}
            aria-label="Filter by priority"
            data-testid="priority-filter"
          >
            {PRIORITY_OPTIONS.map((option) => (
              <option key={option.value ?? 'all'} value={option.value ?? ''}>
                {option.label}
              </option>
            ))}
          </select>
        </div>

        {/* Type dropdown */}
        <div className={styles.filterGroup}>
          <label htmlFor="type-filter" className={styles.label}>
            Type
          </label>
          <select
            id="type-filter"
            className={styles.select}
            value={filters.type ?? ''}
            onChange={handleTypeChange}
            aria-label="Filter by type"
            data-testid="type-filter"
          >
            {TYPE_OPTIONS.map((option) => (
              <option key={option.value ?? 'all'} value={option.value ?? ''}>
                {option.label}
              </option>
            ))}
          </select>
        </div>

        {/* Label filter dropdown */}
        {availableLabels && availableLabels.length > 0 && (
          <div className={styles.filterGroup} ref={labelDropdownRef}>
            <span className={styles.label}>Labels</span>
            <div className={styles.dropdownContainer}>
              <button
                type="button"
                className={styles.dropdownTrigger}
                onClick={toggleLabelDropdown}
                aria-expanded={labelDropdownOpen}
                aria-haspopup="true"
                aria-label="Filter by labels"
                data-testid="label-filter-trigger"
              >
                {filters.labels && filters.labels.length > 0
                  ? `${filters.labels.length} selected`
                  : 'All labels'}
                <span className={styles.dropdownArrow} aria-hidden="true">
                  ▼
                </span>
              </button>
              {labelDropdownOpen && (
                <div
                  className={styles.dropdownMenu}
                  role="group"
                  aria-label="Select labels"
                  data-testid="label-filter-menu"
                >
                  {availableLabels.map((label) => {
                    const isSelected = filters.labels?.includes(label) ?? false;
                    return (
                      <label key={label} className={styles.dropdownItem}>
                        <input
                          type="checkbox"
                          checked={isSelected}
                          onChange={() => handleLabelToggle(label)}
                          data-testid={`label-option-${label}`}
                        />
                        <span>{label}</span>
                      </label>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        )}

        {/* Group by dropdown */}
        {onGroupByChange && (
          <div className={styles.filterGroup}>
            <label htmlFor="groupby-filter" className={styles.label}>
              Group by
            </label>
            <select
              id="groupby-filter"
              className={styles.select}
              value={groupBy ?? 'none'}
              onChange={handleGroupByChange}
              aria-label="Group issues by"
              data-testid="groupby-filter"
            >
              {GROUP_BY_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>

      {/* Clear button */}
      {shouldShowClear && (
        <button
          type="button"
          className={styles.clearButton}
          onClick={handleClearAll}
          aria-label="Clear all filters"
          data-testid="clear-filters"
        >
          Clear filters
        </button>
      )}
    </div>
  );
}
