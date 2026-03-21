/**
 * OwnerDropdown component.
 * Interactive dropdown for viewing/editing the owner field with text input
 * and recent owners suggestions. Owner is always a human (git author for attribution).
 */

import { useState, useCallback, useRef, useEffect } from "react";

import { useRecentOwners } from "@/hooks/useRecentOwners";

import styles from "./OwnerDropdown.module.css";

/**
 * Props for the OwnerDropdown component.
 */
export interface OwnerDropdownProps {
  /** Current owner value */
  owner: string | undefined;
  /** Callback when owner changes - receives new owner, should throw on error */
  onSave: (newOwner: string) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * OwnerDropdown renders an interactive dropdown for changing issue owner.
 * Features:
 * - Text input for free-form owner names
 * - Recent owners list from localStorage
 * - Remove owner capability
 * - Keyboard support (Escape to close, Enter to submit)
 * - Optimistic updates with error rollback
 */
export function OwnerDropdown({
  owner,
  onSave,
  isSaving,
  disabled,
  className,
}: OwnerDropdownProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticOwner, setOptimisticOwner] = useState<string | undefined>(
    owner,
  );
  const [inputValue, setInputValue] = useState("");

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const { recentOwners, addRecentOwner } = useRecentOwners();

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticOwner(owner);
  }, [owner]);

  // Focus input when dropdown opens
  useEffect(() => {
    if (isOpen) {
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
    async (newOwner: string) => {
      const previousOwner = owner;
      setOptimisticOwner(newOwner || undefined);
      setIsOpen(false);
      setInputValue("");
      setError(null);

      if (newOwner) {
        addRecentOwner(newOwner);
      }

      try {
        await onSave(newOwner);
      } catch (err) {
        setOptimisticOwner(previousOwner);
        const message =
          err instanceof Error ? err.message : "Failed to update owner";
        setError(message);
      }
    },
    [owner, onSave, addRecentOwner],
  );

  const handleInputSubmit = useCallback(() => {
    const trimmed = inputValue.trim();
    if (!trimmed) return;
    handleSave(trimmed);
  }, [inputValue, handleSave]);

  const handleRecentClick = useCallback(
    (name: string) => {
      handleSave(name);
    },
    [handleSave],
  );

  const handleRemoveOwner = useCallback(() => {
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

  const displayName = optimisticOwner || "No owner";
  const hasOwner = Boolean(optimisticOwner);
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.ownerDropdown, className]
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
        data-unset={!hasOwner || undefined}
        aria-expanded={isOpen}
        aria-haspopup="true"
        aria-label={`Owner: ${displayName}. Click to change.`}
        data-testid="owner-dropdown-trigger"
      >
        <svg
          className={styles.shieldIcon}
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M8 1.5L2.5 4v4c0 3.5 2.5 5.5 5.5 6.5 3-1 5.5-3 5.5-6.5V4L8 1.5z"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span className={styles.triggerText}>{displayName}</span>
        <span className={styles.dropdownArrow} aria-hidden="true">
          ▾
        </span>
      </button>

      {isOpen && (
        <div className={styles.menu} data-testid="owner-dropdown-menu">
          <div className={styles.inputRow}>
            <input
              ref={inputRef}
              type="text"
              className={styles.input}
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleInputKeyDown}
              placeholder="Type owner name..."
              data-testid="owner-input"
            />
            <button
              type="button"
              className={styles.submitButton}
              onClick={handleInputSubmit}
              disabled={!inputValue.trim()}
              data-testid="owner-submit"
            >
              Set
            </button>
          </div>

          {recentOwners.length > 0 && (
            <div className={styles.recentSection}>
              <span className={styles.recentLabel}>Recent</span>
              {recentOwners.map((name) => (
                <button
                  key={name}
                  type="button"
                  className={styles.recentItem}
                  onClick={() => handleRecentClick(name)}
                  data-testid={`recent-owner-${name}`}
                >
                  {name}
                </button>
              ))}
            </div>
          )}

          {hasOwner && (
            <button
              type="button"
              className={styles.removeButton}
              onClick={handleRemoveOwner}
              data-testid="owner-remove"
            >
              Remove owner
            </button>
          )}
        </div>
      )}

      {isSaving && (
        <span
          className={styles.savingIndicator}
          aria-label="Saving..."
          data-testid="owner-saving"
        />
      )}

      {error && (
        <span className={styles.error} role="alert" data-testid="owner-error">
          {error}
        </span>
      )}
    </div>
  );
}
