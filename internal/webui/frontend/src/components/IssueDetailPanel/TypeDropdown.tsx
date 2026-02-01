/**
 * TypeDropdown component.
 * Interactive dropdown for changing issue type (bug, feature, task, epic, chore) with icons.
 */

import { useState, useCallback, useRef, useEffect } from 'react';
import type { IssueType, KnownIssueType } from '@/types';
import { TypeIcon } from '@/components/TypeIcon';
import styles from './TypeDropdown.module.css';

/**
 * Type option configuration.
 */
interface TypeOption {
  /** The type value */
  value: KnownIssueType;
  /** Display label (e.g., "Bug") */
  label: string;
}

/**
 * Type options for the dropdown.
 */
const TYPE_OPTIONS: TypeOption[] = [
  { value: 'bug', label: 'Bug' },
  { value: 'feature', label: 'Feature' },
  { value: 'task', label: 'Task' },
  { value: 'epic', label: 'Epic' },
  { value: 'chore', label: 'Chore' },
];

/**
 * Props for the TypeDropdown component.
 */
export interface TypeDropdownProps {
  /** Current issue type */
  type: IssueType | undefined;
  /** Callback when type is saved - receives new type, should throw on error */
  onSave: (newType: IssueType) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Format issue type to human-readable string.
 */
function formatIssueType(type: IssueType | undefined): string {
  if (!type) return 'Task';
  return type.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

/**
 * TypeDropdown renders an interactive dropdown for changing issue type.
 * Features:
 * - Type icons matching the design system
 * - Keyboard navigation (Arrow keys, Enter, Escape)
 * - Optimistic updates with error rollback
 * - Loading state during save
 */
export function TypeDropdown({
  type,
  onSave,
  isSaving,
  disabled,
  className,
}: TypeDropdownProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticType, setOptimisticType] = useState<IssueType | undefined>(type);
  const [focusedIndex, setFocusedIndex] = useState<number>(-1);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticType(type);
  }, [type]);

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
        setFocusedIndex(-1);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isOpen]);

  // Handle escape key to close
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsOpen(false);
        setFocusedIndex(-1);
        triggerRef.current?.focus();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen]);

  const handleTriggerClick = useCallback(() => {
    if (disabled || isSaving) return;
    setError(null);
    setIsOpen((prev) => !prev);
    if (!isOpen) {
      // Set initial focus to current type when opening
      const currentIndex = TYPE_OPTIONS.findIndex((opt) => opt.value === optimisticType);
      setFocusedIndex(currentIndex >= 0 ? currentIndex : 0);
    }
  }, [disabled, isSaving, isOpen, optimisticType]);

  const handleSelect = useCallback(
    async (newType: IssueType) => {
      // Skip if same type selected
      if (newType === type) {
        setIsOpen(false);
        setFocusedIndex(-1);
        return;
      }

      // Optimistic update
      const previousType = type;
      setOptimisticType(newType);
      setIsOpen(false);
      setFocusedIndex(-1);
      setError(null);

      try {
        await onSave(newType);
      } catch (err) {
        // Rollback on error
        setOptimisticType(previousType);
        const message = err instanceof Error ? err.message : 'Failed to update type';
        setError(message);
      }
    },
    [type, onSave]
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (!isOpen) {
        // Open on Enter or Space when closed
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          handleTriggerClick();
        }
        return;
      }

      switch (event.key) {
        case 'ArrowDown':
          event.preventDefault();
          setFocusedIndex((prev) => Math.min(prev + 1, TYPE_OPTIONS.length - 1));
          break;
        case 'ArrowUp':
          event.preventDefault();
          setFocusedIndex((prev) => Math.max(prev - 1, 0));
          break;
        case 'Enter':
        case ' ': {
          event.preventDefault();
          const selectedOption = TYPE_OPTIONS[focusedIndex];
          if (focusedIndex >= 0 && focusedIndex < TYPE_OPTIONS.length && selectedOption) {
            handleSelect(selectedOption.value);
          }
          break;
        }
        case 'Home':
          event.preventDefault();
          setFocusedIndex(0);
          break;
        case 'End':
          event.preventDefault();
          setFocusedIndex(TYPE_OPTIONS.length - 1);
          break;
      }
    },
    [isOpen, focusedIndex, handleTriggerClick, handleSelect]
  );

  const displayType = optimisticType ?? 'task';
  const displayLabel = formatIssueType(optimisticType);
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.typeDropdown, className].filter(Boolean).join(' ');

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        onKeyDown={handleKeyDown}
        disabled={isDisabled}
        data-type={displayType}
        data-saving={isSaving || undefined}
        aria-expanded={isOpen}
        aria-haspopup="listbox"
        aria-label={`Type: ${displayLabel}. Click to change.`}
        data-testid="type-dropdown-trigger"
      >
        <TypeIcon type={displayType} size={14} className={styles.triggerIcon ?? ''} />
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
          aria-label="Select type"
          data-testid="type-dropdown-menu"
        >
          {TYPE_OPTIONS.map((option, index) => (
            <div
              key={option.value}
              className={styles.option}
              data-type={option.value}
              data-selected={option.value === optimisticType || undefined}
              data-focused={index === focusedIndex || undefined}
              role="option"
              aria-selected={option.value === optimisticType}
              onClick={() => handleSelect(option.value)}
              data-testid={`type-option-${option.value}`}
            >
              <TypeIcon type={option.value} size={16} className={styles.optionIcon ?? ''} />
              <span className={styles.optionText}>{option.label}</span>
              {option.value === optimisticType && (
                <span className={styles.checkmark} aria-hidden="true">
                  ✓
                </span>
              )}
            </div>
          ))}
        </div>
      )}

      {isSaving && (
        <span className={styles.savingIndicator} aria-label="Saving..." data-testid="type-saving" />
      )}

      {error && (
        <span className={styles.error} role="alert" data-testid="type-error">
          {error}
        </span>
      )}
    </div>
  );
}
