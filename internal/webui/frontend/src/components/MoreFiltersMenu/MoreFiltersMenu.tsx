/**
 * MoreFiltersMenu component.
 * A "..." button with a popover containing secondary filter controls (GroupBy).
 */

import { useState, useCallback, useEffect, useRef } from "react";

import type { GroupByOption } from "@/components/FilterBar";

import styles from "./MoreFiltersMenu.module.css";

const GROUP_BY_OPTIONS: { label: string; value: GroupByOption }[] = [
  { label: "All", value: "none" },
  { label: "Epic", value: "epic" },
  { label: "Assignee", value: "assignee" },
  { label: "Priority", value: "priority" },
  { label: "Type", value: "type" },
  { label: "Label", value: "label" },
  { label: "Repo", value: "repo" },
];

export interface MoreFiltersMenuProps {
  /** Current group by value */
  groupBy: GroupByOption;
  /** Callback when group by changes */
  onGroupByChange: (value: GroupByOption) => void;
}

export function MoreFiltersMenu({
  groupBy,
  onGroupByChange,
}: MoreFiltersMenuProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const hasActiveGroupBy = groupBy !== "none";

  // Close on outside click
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    }
    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      return () =>
        document.removeEventListener("mousedown", handleClickOutside);
    }
  }, [isOpen]);

  const handleToggle = useCallback(() => {
    setIsOpen((prev) => !prev);
  }, []);

  const handleGroupByChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      onGroupByChange(event.target.value as GroupByOption);
    },
    [onGroupByChange],
  );

  const triggerClassName = [styles.trigger, isOpen ? styles.active : ""]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={styles.container} ref={containerRef}>
      <button
        type="button"
        className={triggerClassName}
        onClick={handleToggle}
        aria-expanded={isOpen}
        aria-haspopup="dialog"
        aria-label="More filters"
        data-testid="more-filters-trigger"
      >
        &#x2026;
      </button>
      {hasActiveGroupBy && <span className={styles.indicator} />}
      {isOpen && (
        <div className={styles.menu} data-testid="more-filters-menu">
          <div className={styles.menuItem}>
            <label htmlFor="more-filters-groupby" className={styles.menuLabel}>
              Group by
            </label>
            <select
              id="more-filters-groupby"
              className={styles.menuSelect}
              value={groupBy}
              onChange={handleGroupByChange}
              aria-label="Group issues by"
              data-testid="more-filters-groupby"
            >
              {GROUP_BY_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        </div>
      )}
    </div>
  );
}
