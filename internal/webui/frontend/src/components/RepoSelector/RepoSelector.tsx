/**
 * RepoSelector — multi-checkbox dropdown for filtering by repository.
 * Follows the Labels dropdown pattern from FilterBar.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { RepoBadge } from "@/components/RepoBadge";

import filterBarStyles from "../FilterBar/FilterBar.module.css";
import styles from "./RepoSelector.module.css";

export interface RepoSelectorProps {
  /** Available repository names */
  availableRepos: string[];
  /** Currently selected repository names (empty = all) */
  selectedRepos: string[];
  /** Callback when selection changes */
  onChange: (repos: string[]) => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Multi-checkbox dropdown for selecting repositories.
 * Renders nothing when fewer than 2 repos are available.
 */
export function RepoSelector({
  availableRepos,
  selectedRepos,
  onChange,
  className,
}: RepoSelectorProps): JSX.Element | null {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  // Close dropdown on outside click
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(event.target as Node)
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

  const toggleDropdown = useCallback(() => {
    setIsOpen((prev) => !prev);
  }, []);

  const handleToggleRepo = useCallback(
    (repo: string) => {
      const isSelected = selectedRepos.includes(repo);
      if (isSelected) {
        onChange(selectedRepos.filter((r) => r !== repo));
      } else {
        onChange([...selectedRepos, repo]);
      }
    },
    [selectedRepos, onChange],
  );

  // Guard after all hooks (Rules of Hooks compliance)
  if (availableRepos.length < 2) return null;

  const triggerLabel =
    selectedRepos.length > 0 ? `Repos (${selectedRepos.length})` : "Repos";

  const rootClassName = [filterBarStyles.filterGroup, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={rootClassName} ref={dropdownRef}>
      <div className={filterBarStyles.dropdownContainer}>
        <button
          type="button"
          className={filterBarStyles.dropdownTrigger}
          onClick={toggleDropdown}
          aria-expanded={isOpen}
          aria-haspopup="listbox"
          aria-label="Filter by repository"
          data-testid="repo-filter-trigger"
        >
          {triggerLabel}
          <span className={filterBarStyles.dropdownArrow} aria-hidden="true">
            ▼
          </span>
        </button>
        {isOpen && (
          <div
            className={`${filterBarStyles.dropdownMenu} ${styles.repoMenu}`}
            role="listbox"
            aria-multiselectable="true"
            aria-label="Select repositories"
            data-testid="repo-filter-menu"
          >
            {availableRepos.map((repo) => {
              const isSelected = selectedRepos.includes(repo);
              return (
                <label
                  key={repo}
                  className={`${filterBarStyles.dropdownItem} ${styles.repoItem}`}
                >
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => handleToggleRepo(repo)}
                    data-testid={`repo-option-${repo}`}
                  />
                  <RepoBadge repoName={repo} />
                </label>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
