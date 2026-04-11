/**
 * RepoDropdown component.
 * Interactive dropdown for assigning an issue to a repo via repo:X labels.
 */

import { useState, useCallback, useRef, useEffect, useMemo } from "react";

import styles from "./RepoDropdown.module.css";

/**
 * Props for the RepoDropdown component.
 */
export interface RepoDropdownProps {
  /** Current repo name extracted from labels, or null if unassigned */
  currentRepo: string | null;
  /** Available repo names from workspace */
  repos: string[];
  /** Callback when repo is saved - receives new repo name or null to unassign */
  onSave: (newRepo: string | null) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/** Option in the dropdown: null value = "None" (unassign). */
interface RepoOption {
  value: string | null;
  label: string;
}

/**
 * RepoDropdown renders an interactive dropdown for assigning an issue to a repo.
 * Features:
 * - Keyboard navigation (Arrow keys, Enter, Escape)
 * - Optimistic updates with error rollback
 * - Loading state during save
 */
export function RepoDropdown({
  currentRepo,
  repos,
  onSave,
  isSaving,
  disabled,
  className,
}: RepoDropdownProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticRepo, setOptimisticRepo] = useState<string | null>(
    currentRepo,
  );
  const [focusedIndex, setFocusedIndex] = useState<number>(-1);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  // Build options: "None" first, then each repo
  const options: RepoOption[] = useMemo(
    () => [
      { value: null, label: "None" },
      ...repos.map((r) => ({ value: r, label: r })),
    ],
    [repos],
  );

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticRepo(currentRepo);
  }, [currentRepo]);

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        setFocusedIndex(-1);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Handle escape key to close (local handler with stopPropagation)
  const handleDropdownKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      setIsOpen(false);
      setFocusedIndex(-1);
      triggerRef.current?.focus();
    }
  }, []);

  const handleTriggerClick = useCallback(() => {
    if (disabled || isSaving) return;
    setError(null);
    setIsOpen((prev) => !prev);
    if (!isOpen) {
      const currentIndex = options.findIndex(
        (opt) => opt.value === optimisticRepo,
      );
      setFocusedIndex(currentIndex >= 0 ? currentIndex : 0);
    }
  }, [disabled, isSaving, isOpen, optimisticRepo, options]);

  const handleSelect = useCallback(
    async (newRepo: string | null) => {
      if (newRepo === currentRepo) {
        setIsOpen(false);
        setFocusedIndex(-1);
        return;
      }

      const previousRepo = currentRepo;
      setOptimisticRepo(newRepo);
      setIsOpen(false);
      setFocusedIndex(-1);
      setError(null);

      try {
        await onSave(newRepo);
      } catch (err) {
        setOptimisticRepo(previousRepo);
        const message =
          err instanceof Error ? err.message : "Failed to update repo";
        setError(message);
      }
    },
    [currentRepo, onSave],
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (!isOpen) {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          handleTriggerClick();
        }
        return;
      }

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          setFocusedIndex((prev) => Math.min(prev + 1, options.length - 1));
          break;
        case "ArrowUp":
          event.preventDefault();
          setFocusedIndex((prev) => Math.max(prev - 1, 0));
          break;
        case "Enter":
        case " ": {
          event.preventDefault();
          const selectedOption = options[focusedIndex];
          if (
            focusedIndex >= 0 &&
            focusedIndex < options.length &&
            selectedOption
          ) {
            handleSelect(selectedOption.value);
          }
          break;
        }
        case "Home":
          event.preventDefault();
          setFocusedIndex(0);
          break;
        case "End":
          event.preventDefault();
          setFocusedIndex(options.length - 1);
          break;
      }
    },
    [isOpen, focusedIndex, handleTriggerClick, handleSelect, options],
  );

  const displayLabel = optimisticRepo ?? "No repo";
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.repoDropdown, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        onKeyDown={handleKeyDown}
        disabled={isDisabled}
        data-saving={isSaving || undefined}
        aria-expanded={isOpen}
        aria-haspopup="listbox"
        aria-label={`Repo: ${displayLabel}. Click to change.`}
        data-testid="repo-dropdown-trigger"
      >
        <svg
          className={styles.triggerIcon}
          width="14"
          height="14"
          viewBox="0 0 16 16"
          fill="currentColor"
          aria-hidden="true"
        >
          <path d="M2 2.5A2.5 2.5 0 0 1 4.5 0h8.75a.75.75 0 0 1 .75.75v12.5a.75.75 0 0 1-.75.75h-2.5a.75.75 0 0 1 0-1.5h1.75v-2h-8a1 1 0 0 0-.714 1.7.75.75 0 1 1-1.072 1.05A2.495 2.495 0 0 1 2 11.5Zm10.5-1h-8a1 1 0 0 0-1 1v6.708A2.486 2.486 0 0 1 4.5 9h8ZM5 12.25a.25.25 0 0 1 .25-.25h3.5a.25.25 0 0 1 .25.25v3.25a.25.25 0 0 1-.4.2l-1.45-1.087a.249.249 0 0 0-.3 0L5.4 15.7a.25.25 0 0 1-.4-.2Z" />
        </svg>
        <span className={styles.triggerText}>{displayLabel}</span>
        <span className={styles.dropdownArrow} aria-hidden="true">
          ▾
        </span>
      </button>

      {isOpen && (
        <div
          ref={menuRef}
          className={styles.menu}
          role="listbox"
          aria-label="Select repo"
          data-testid="repo-dropdown-menu"
          onKeyDown={handleDropdownKeyDown}
        >
          {options.map((option, index) => (
            <div
              key={option.value ?? "__none__"}
              className={styles.option}
              data-selected={option.value === optimisticRepo || undefined}
              data-focused={index === focusedIndex || undefined}
              role="option"
              aria-selected={option.value === optimisticRepo}
              onClick={() => handleSelect(option.value)}
              data-testid={`repo-option-${option.value ?? "none"}`}
            >
              <svg
                className={styles.optionIcon}
                width="16"
                height="16"
                viewBox="0 0 16 16"
                fill="currentColor"
                aria-hidden="true"
              >
                {option.value ? (
                  <path d="M2 2.5A2.5 2.5 0 0 1 4.5 0h8.75a.75.75 0 0 1 .75.75v12.5a.75.75 0 0 1-.75.75h-2.5a.75.75 0 0 1 0-1.5h1.75v-2h-8a1 1 0 0 0-.714 1.7.75.75 0 1 1-1.072 1.05A2.495 2.495 0 0 1 2 11.5Zm10.5-1h-8a1 1 0 0 0-1 1v6.708A2.486 2.486 0 0 1 4.5 9h8ZM5 12.25a.25.25 0 0 1 .25-.25h3.5a.25.25 0 0 1 .25.25v3.25a.25.25 0 0 1-.4.2l-1.45-1.087a.249.249 0 0 0-.3 0L5.4 15.7a.25.25 0 0 1-.4-.2Z" />
                ) : (
                  <path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z" />
                )}
              </svg>
              <span className={styles.optionText}>{option.label}</span>
              {option.value === optimisticRepo && (
                <span className={styles.checkmark} aria-hidden="true">
                  ✓
                </span>
              )}
            </div>
          ))}
        </div>
      )}

      {isSaving && (
        <span
          className={styles.savingIndicator}
          aria-label="Saving..."
          data-testid="repo-saving"
        />
      )}

      {error && (
        <span className={styles.error} role="alert" data-testid="repo-error">
          {error}
        </span>
      )}
    </div>
  );
}
