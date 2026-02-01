/**
 * PriorityDropdown component.
 * Interactive dropdown for changing issue priority (P0-P4) with colored indicators.
 */

import { useState, useCallback, useRef, useEffect } from 'react';
import type { Priority } from '@/types';
import styles from './PriorityDropdown.module.css';

/**
 * Priority option configuration.
 */
interface PriorityOption {
  /** The priority value (0-4) */
  value: Priority;
  /** Full label (e.g., "Critical") */
  label: string;
  /** Short label (e.g., "P0") */
  shortLabel: string;
}

/**
 * Priority options for the dropdown.
 */
const PRIORITY_OPTIONS: PriorityOption[] = [
  { value: 0, label: 'Critical', shortLabel: 'P0' },
  { value: 1, label: 'High', shortLabel: 'P1' },
  { value: 2, label: 'Medium', shortLabel: 'P2' },
  { value: 3, label: 'Normal', shortLabel: 'P3' },
  { value: 4, label: 'Backlog', shortLabel: 'P4' },
];

/**
 * Props for the PriorityDropdown component.
 */
export interface PriorityDropdownProps {
  /** Current priority value */
  priority: Priority;
  /** Callback when priority changes - receives new priority, should throw on error */
  onSave: (newPriority: Priority) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * PriorityDropdown renders an interactive dropdown for changing issue priority.
 * Features:
 * - Colored priority badges matching the design system
 * - Keyboard navigation (Arrow keys, Enter, Escape)
 * - Optimistic updates with error rollback
 * - Loading state during save
 */
export function PriorityDropdown({
  priority,
  onSave,
  isSaving,
  disabled,
  className,
}: PriorityDropdownProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticPriority, setOptimisticPriority] = useState<Priority>(priority);
  const [focusedIndex, setFocusedIndex] = useState<number>(-1);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticPriority(priority);
  }, [priority]);

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
      // Set initial focus to current priority when opening
      const currentIndex = PRIORITY_OPTIONS.findIndex((opt) => opt.value === optimisticPriority);
      setFocusedIndex(currentIndex);
    }
  }, [disabled, isSaving, isOpen, optimisticPriority]);

  const handleSelect = useCallback(
    async (newPriority: Priority) => {
      // Skip if same priority selected
      if (newPriority === priority) {
        setIsOpen(false);
        setFocusedIndex(-1);
        return;
      }

      // Optimistic update
      const previousPriority = priority;
      setOptimisticPriority(newPriority);
      setIsOpen(false);
      setFocusedIndex(-1);
      setError(null);

      try {
        await onSave(newPriority);
      } catch (err) {
        // Rollback on error
        setOptimisticPriority(previousPriority);
        const message = err instanceof Error ? err.message : 'Failed to update priority';
        setError(message);
      }
    },
    [priority, onSave]
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
          setFocusedIndex((prev) => Math.min(prev + 1, PRIORITY_OPTIONS.length - 1));
          break;
        case 'ArrowUp':
          event.preventDefault();
          setFocusedIndex((prev) => Math.max(prev - 1, 0));
          break;
        case 'Enter':
        case ' ': {
          event.preventDefault();
          const selectedOption = PRIORITY_OPTIONS[focusedIndex];
          if (focusedIndex >= 0 && focusedIndex < PRIORITY_OPTIONS.length && selectedOption) {
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
          setFocusedIndex(PRIORITY_OPTIONS.length - 1);
          break;
      }
    },
    [isOpen, focusedIndex, handleTriggerClick, handleSelect]
  );

  // Find current option, falling back to Medium (P2) if not found
  const defaultOption: PriorityOption = { value: 2, label: 'Medium', shortLabel: 'P2' };
  const currentOption =
    PRIORITY_OPTIONS.find((opt) => opt.value === optimisticPriority) ?? defaultOption;
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.priorityDropdown, className].filter(Boolean).join(' ');

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        onKeyDown={handleKeyDown}
        disabled={isDisabled}
        data-priority={optimisticPriority}
        data-saving={isSaving || undefined}
        aria-expanded={isOpen}
        aria-haspopup="listbox"
        aria-label={`Priority: ${currentOption.shortLabel} - ${currentOption.label}. Click to change.`}
        data-testid="priority-dropdown-trigger"
      >
        <span className={styles.colorDot} data-priority={optimisticPriority} />
        <span className={styles.triggerText}>
          {currentOption.shortLabel} - {currentOption.label}
        </span>
        <span className={styles.dropdownArrow} aria-hidden="true">
          ▾
        </span>
      </button>

      {isOpen && (
        <div
          ref={menuRef}
          className={styles.menu}
          role="listbox"
          aria-label="Select priority"
          data-testid="priority-dropdown-menu"
        >
          {PRIORITY_OPTIONS.map((option, index) => (
            <div
              key={option.value}
              className={styles.option}
              data-priority={option.value}
              data-selected={option.value === optimisticPriority || undefined}
              data-focused={index === focusedIndex || undefined}
              role="option"
              aria-selected={option.value === optimisticPriority}
              onClick={() => handleSelect(option.value)}
              data-testid={`priority-option-${option.value}`}
            >
              <span className={styles.colorDot} data-priority={option.value} />
              <span className={styles.optionText}>
                {option.shortLabel} - {option.label}
              </span>
              {option.value === optimisticPriority && (
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
          data-testid="priority-saving"
        />
      )}

      {error && (
        <span className={styles.error} role="alert" data-testid="priority-error">
          {error}
        </span>
      )}
    </div>
  );
}
