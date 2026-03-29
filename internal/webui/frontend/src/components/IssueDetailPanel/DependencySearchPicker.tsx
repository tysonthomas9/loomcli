/**
 * DependencySearchPicker component.
 * Enhanced dependency add form with search/autocomplete for issue IDs and titles.
 */

import {
  useState,
  useCallback,
  useRef,
  useEffect,
  type KeyboardEvent,
} from "react";

import { useDebounce, useIssueSearch } from "@/hooks";
import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";

import styles from "./DependencySearchPicker.module.css";

export interface DependencySearchPickerProps {
  /** Current issue ID (excluded from results) */
  issueId: string;
  /** Already-added dependency IDs (excluded from results) */
  existingDependencyIds: string[];
  /** Callback when an issue is selected */
  onSelect: (issueId: string) => void;
  /** Callback when the picker is cancelled */
  onCancel: () => void;
  /** Whether the picker is in a saving state */
  isSaving?: boolean;
}

export function DependencySearchPicker({
  issueId,
  existingDependencyIds,
  onSelect,
  onCancel,
  isSaving = false,
}: DependencySearchPickerProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const [inputValue, setInputValue] = useState("");
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const [showDropdown, setShowDropdown] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const dropdownRef = useRef<HTMLUListElement>(null);

  const { results, isLoading, search } = useIssueSearch(workspaceId);
  const debouncedQuery = useDebounce(inputValue, 200);

  // Trigger search when debounced query changes
  useEffect(() => {
    search(debouncedQuery);
    setShowDropdown(debouncedQuery.trim().length > 0);
    setFocusedIndex(-1);
  }, [debouncedQuery, search]);

  // Focus input on mount
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // Filter out current issue and existing dependencies
  const filteredResults = results.filter(
    (issue) =>
      issue.id !== issueId && !existingDependencyIds.includes(issue.id),
  );

  const handleSelectIssue = useCallback(
    (selectedId: string) => {
      onSelect(selectedId);
      setInputValue("");
      setShowDropdown(false);
    },
    [onSelect],
  );

  const handleSubmit = useCallback(() => {
    // If there's a focused result, select it
    if (focusedIndex >= 0 && focusedIndex < filteredResults.length) {
      const focused = filteredResults[focusedIndex];
      if (focused) {
        handleSelectIssue(focused.id);
      }
      return;
    }

    // Otherwise, use direct ID entry
    const trimmed = inputValue.trim();
    if (trimmed) {
      onSelect(trimmed);
      setInputValue("");
      setShowDropdown(false);
    }
  }, [focusedIndex, filteredResults, handleSelectIssue, inputValue, onSelect]);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Escape") {
        e.preventDefault();
        if (showDropdown) {
          setShowDropdown(false);
        } else {
          onCancel();
        }
        return;
      }

      if (e.key === "Enter") {
        e.preventDefault();
        handleSubmit();
        return;
      }

      if (e.key === "ArrowDown") {
        e.preventDefault();
        if (filteredResults.length > 0) {
          setFocusedIndex((prev) =>
            prev < filteredResults.length - 1 ? prev + 1 : 0,
          );
        }
        return;
      }

      if (e.key === "ArrowUp") {
        e.preventDefault();
        if (filteredResults.length > 0) {
          setFocusedIndex((prev) =>
            prev > 0 ? prev - 1 : filteredResults.length - 1,
          );
        }
      }
    },
    [showDropdown, onCancel, handleSubmit, filteredResults.length],
  );

  // Scroll focused item into view
  useEffect(() => {
    if (focusedIndex >= 0 && dropdownRef.current) {
      const items = dropdownRef.current.querySelectorAll(
        "[data-testid^='search-result-']",
      );
      items[focusedIndex]?.scrollIntoView({ block: "nearest" });
    }
  }, [focusedIndex]);

  return (
    <div className={styles.searchPicker} data-testid="dependency-search-picker">
      <input
        ref={inputRef}
        type="text"
        className={styles.input}
        value={inputValue}
        onChange={(e) => setInputValue(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Search by ID or title..."
        disabled={isSaving}
        aria-label="Search issues"
        data-testid="dependency-search-input"
        autoComplete="off"
      />

      {/* Dropdown results */}
      {showDropdown && !isSaving && (
        <ul
          ref={dropdownRef}
          className={styles.dropdown}
          role="listbox"
          data-testid="search-results-dropdown"
        >
          {isLoading ? (
            <li className={styles.noResults}>Loading...</li>
          ) : filteredResults.length > 0 ? (
            filteredResults.slice(0, 10).map((issue, index) => (
              <li
                key={issue.id}
                className={`${styles.dropdownItem} ${index === focusedIndex ? styles.dropdownItemFocused : ""}`}
                onClick={() => handleSelectIssue(issue.id)}
                role="option"
                aria-selected={index === focusedIndex}
                tabIndex={-1}
                data-testid={`search-result-${issue.id}`}
              >
                <span className={styles.resultId}>{issue.id}</span>
                <span className={styles.resultTitle}>{issue.title}</span>
              </li>
            ))
          ) : (
            <li className={styles.noResults} data-testid="no-search-results">
              No matching issues. Press Enter to add by ID.
            </li>
          )}
        </ul>
      )}

      <div className={styles.formActions}>
        <button
          type="button"
          className={styles.cancelButton}
          onClick={onCancel}
          disabled={isSaving}
          data-testid="cancel-add-dependency"
        >
          Cancel
        </button>
        <button
          type="button"
          className={styles.confirmButton}
          onClick={handleSubmit}
          disabled={isSaving || !inputValue.trim()}
          data-testid="confirm-add-dependency"
        >
          {isSaving ? "Adding..." : "Add"}
        </button>
      </div>
    </div>
  );
}
