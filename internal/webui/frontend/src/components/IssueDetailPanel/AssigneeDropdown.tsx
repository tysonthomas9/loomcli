/**
 * AssigneeDropdown component.
 * Interactive dropdown for viewing/editing the assignee field with text input
 * and recent assignees suggestions.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import { useRecentAssignees } from "@/hooks";

import styles from "./AssigneeDropdown.module.css";

/**
 * Props for the AssigneeDropdown component.
 */
export interface AssigneeDropdownProps {
  /** Current assignee value (may include [H] prefix) */
  assignee: string | undefined;
  /** Callback when assignee changes - receives new assignee, should throw on error */
  onSave: (newAssignee: string) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Strip the [H] prefix from an assignee name for display.
 */
function stripHumanPrefix(name: string): string {
  return name.replace(/^\[H\]\s*/, "");
}

/**
 * AssigneeDropdown renders an interactive dropdown for changing issue assignee.
 * Features:
 * - Text input for free-form assignee names
 * - Recent assignees list from localStorage
 * - Unassign capability
 * - Keyboard support (Escape to close, Enter to submit)
 * - Optimistic updates with error rollback
 */
export function AssigneeDropdown({
  assignee,
  onSave,
  isSaving,
  disabled,
  className,
}: AssigneeDropdownProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticAssignee, setOptimisticAssignee] = useState<
    string | undefined
  >(assignee);
  const [inputValue, setInputValue] = useState("");

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const { recentAssignees, addRecentAssignee } = useRecentAssignees();

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticAssignee(assignee);
  }, [assignee]);

  // Focus input when dropdown opens
  useEffect(() => {
    if (isOpen) {
      // Delay to allow DOM to render
      requestAnimationFrame(() => {
        inputRef.current?.focus();
      });
    }
  }, [isOpen]);

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        setInputValue("");
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Handle escape key to close
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
        setInputValue("");
        triggerRef.current?.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen]);

  const handleTriggerClick = useCallback(() => {
    if (disabled || isSaving) return;
    setError(null);
    setIsOpen((prev) => !prev);
    setInputValue("");
  }, [disabled, isSaving]);

  const handleSave = useCallback(
    async (newAssignee: string) => {
      const previousAssignee = assignee;
      setOptimisticAssignee(newAssignee || undefined);
      setIsOpen(false);
      setInputValue("");
      setError(null);

      // Add to recent assignees (without [H] prefix)
      if (newAssignee) {
        const nameWithoutPrefix = stripHumanPrefix(newAssignee);
        addRecentAssignee(nameWithoutPrefix);
      }

      try {
        await onSave(newAssignee);
      } catch (err) {
        // Rollback on error
        setOptimisticAssignee(previousAssignee);
        const message =
          err instanceof Error ? err.message : "Failed to update assignee";
        setError(message);
      }
    },
    [assignee, onSave, addRecentAssignee],
  );

  const handleInputSubmit = useCallback(() => {
    const trimmed = inputValue.trim();
    if (!trimmed) return;
    // Add [H] prefix for manually entered names
    handleSave(`[H] ${trimmed}`);
  }, [inputValue, handleSave]);

  const handleRecentClick = useCallback(
    (name: string) => {
      // Recent names are stored without prefix, add [H] for human assignment
      handleSave(`[H] ${name}`);
    },
    [handleSave],
  );

  const handleUnassign = useCallback(() => {
    handleSave("");
  }, [handleSave]);

  const handleInputKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === "Enter") {
        event.preventDefault();
        handleInputSubmit();
      }
    },
    [handleInputSubmit],
  );

  const displayName = optimisticAssignee
    ? stripHumanPrefix(optimisticAssignee)
    : "Unassigned";
  const hasAssignee = Boolean(optimisticAssignee);
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.assigneeDropdown, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        disabled={isDisabled}
        data-saving={isSaving || undefined}
        data-unassigned={!hasAssignee || undefined}
        aria-expanded={isOpen}
        aria-haspopup="true"
        aria-label={`Assignee: ${displayName}. Click to change.`}
        data-testid="assignee-dropdown-trigger"
      >
        <svg
          className={styles.personIcon}
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <circle cx="8" cy="5" r="3" stroke="currentColor" strokeWidth="1.5" />
          <path
            d="M2 14c0-2.5 2.5-4 6-4s6 1.5 6 4"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
          />
        </svg>
        <span className={styles.triggerText}>{displayName}</span>
        <span className={styles.dropdownArrow} aria-hidden="true">
          ▾
        </span>
      </button>

      {isOpen && (
        <div className={styles.menu} data-testid="assignee-dropdown-menu">
          <div className={styles.inputRow}>
            <input
              ref={inputRef}
              type="text"
              className={styles.input}
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleInputKeyDown}
              placeholder="Type a name..."
              data-testid="assignee-input"
            />
            <button
              type="button"
              className={styles.submitButton}
              onClick={handleInputSubmit}
              disabled={!inputValue.trim()}
              data-testid="assignee-submit"
            >
              Assign
            </button>
          </div>

          {recentAssignees.length > 0 && (
            <div className={styles.recentSection}>
              <span className={styles.recentLabel}>Recent</span>
              {recentAssignees.map((name) => (
                <button
                  key={name}
                  type="button"
                  className={styles.recentItem}
                  onClick={() => handleRecentClick(name)}
                  data-testid={`recent-assignee-${name}`}
                >
                  {name}
                </button>
              ))}
            </div>
          )}

          {hasAssignee && (
            <button
              type="button"
              className={styles.unassignButton}
              onClick={handleUnassign}
              data-testid="assignee-unassign"
            >
              Unassign
            </button>
          )}
        </div>
      )}

      {isSaving && (
        <span
          className={styles.savingIndicator}
          aria-label="Saving..."
          data-testid="assignee-saving"
        />
      )}

      {error && (
        <span
          className={styles.error}
          role="alert"
          data-testid="assignee-error"
        >
          {error}
        </span>
      )}
    </div>
  );
}
