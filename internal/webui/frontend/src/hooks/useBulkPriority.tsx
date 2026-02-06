/**
 * useBulkPriority - React hook for changing priority of multiple issues at once.
 * Provides loading state, error handling, and dropdown menu for priority selection.
 */

import { useState, useCallback, useRef, useEffect } from 'react';

import { updateIssue } from '@/api';
import type { Priority } from '@/types';

import styles from './useBulkPriority.module.css';

/**
 * Priority option for the dropdown menu.
 */
export interface PriorityOption {
  /** Display label (e.g., "P0 (Critical)") */
  label: string;
  /** Priority value (0-4) */
  value: Priority;
}

/**
 * Priority options for the dropdown menu.
 */
export const PRIORITY_OPTIONS: PriorityOption[] = [
  { label: 'P0 (Critical)', value: 0 },
  { label: 'P1 (High)', value: 1 },
  { label: 'P2 (Medium)', value: 2 },
  { label: 'P3 (Normal)', value: 3 },
  { label: 'P4 (Backlog)', value: 4 },
];

/**
 * Options for the useBulkPriority hook.
 */
export interface UseBulkPriorityOptions {
  /** Currently selected issue IDs */
  selectedIds: Set<string>;
  /** Callback after all issues are successfully updated */
  onSuccess?: (updatedIds: string[], newPriority: Priority) => void;
  /** Callback after partial success (some issues failed to update) */
  onPartialSuccess?: (updatedIds: string[], failedIds: string[], newPriority: Priority) => void;
  /** Callback after total failure (no issues updated) */
  onError?: (error: Error, failedIds: string[]) => void;
}

/**
 * Return type for the useBulkPriority hook.
 */
export interface UseBulkPriorityReturn {
  /** Execute bulk priority update */
  bulkUpdatePriority: (issueIds: Set<string> | string[], priority: Priority) => Promise<void>;
  /** Whether a bulk update is in progress */
  isLoading: boolean;
  /** Error message if the operation failed completely */
  error: string | null;
  /** Set of issue IDs that failed to update in the last operation */
  failedIds: Set<string>;
  /** Number of issues successfully updated in the last operation */
  successCount: number;
  /** Whether the priority menu is open */
  isMenuOpen: boolean;
  /** Open the priority menu */
  openMenu: () => void;
  /** Close the priority menu */
  closeMenu: () => void;
  /** Render the priority action button with dropdown */
  renderAction: () => React.ReactNode;
  /** Reset error and failed state */
  reset: () => void;
}

/**
 * React hook for bulk priority updates.
 *
 * @example
 * ```tsx
 * const { renderAction, isLoading, error } = useBulkPriority({
 *   selectedIds,
 *   onSuccess: (updatedIds, priority) => {
 *     console.log(`Updated ${updatedIds.length} issues to P${priority}`);
 *     clearSelection();
 *   },
 * });
 * ```
 */
export function useBulkPriority(options: UseBulkPriorityOptions): UseBulkPriorityReturn {
  const { selectedIds, onSuccess, onPartialSuccess, onError } = options;

  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [failedIds, setFailedIds] = useState<Set<string>>(new Set());
  const [successCount, setSuccessCount] = useState(0);
  const [isMenuOpen, setIsMenuOpen] = useState(false);

  // Track mounted state to prevent state updates after unmount
  const mountedRef = useRef(true);
  const menuRef = useRef<HTMLDivElement>(null);

  // Store callbacks in refs to avoid stale closures
  const onSuccessRef = useRef(onSuccess);
  const onPartialSuccessRef = useRef(onPartialSuccess);
  const onErrorRef = useRef(onError);

  useEffect(() => {
    onSuccessRef.current = onSuccess;
  }, [onSuccess]);
  useEffect(() => {
    onPartialSuccessRef.current = onPartialSuccess;
  }, [onPartialSuccess]);
  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Close menu when clicking outside
  useEffect(() => {
    if (!isMenuOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setIsMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isMenuOpen]);

  // Close menu on Escape key
  useEffect(() => {
    if (!isMenuOpen) return;

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsMenuOpen(false);
      }
    };

    document.addEventListener('keydown', handleEscape);
    return () => document.removeEventListener('keydown', handleEscape);
  }, [isMenuOpen]);

  /**
   * Execute bulk priority update.
   */
  const bulkUpdatePriority = useCallback(
    async (issueIds: Set<string> | string[], priority: Priority) => {
      // Convert to array if Set
      const ids = Array.isArray(issueIds) ? issueIds : Array.from(issueIds);

      if (ids.length === 0) return;

      setIsLoading(true);
      setError(null);
      setFailedIds(new Set());
      setSuccessCount(0);
      setIsMenuOpen(false);

      try {
        // Update all issues in parallel
        const results = await Promise.allSettled(ids.map((id) => updateIssue(id, { priority })));

        // Guard against unmount
        if (!mountedRef.current) return;

        // Categorize results
        const updatedIds: string[] = [];
        const failedIdsList: string[] = [];

        results.forEach((result, index) => {
          const id = ids[index];
          if (id !== undefined) {
            if (result.status === 'fulfilled') {
              updatedIds.push(id);
            } else {
              failedIdsList.push(id);
            }
          }
        });

        setSuccessCount(updatedIds.length);
        setFailedIds(new Set(failedIdsList));

        // Determine outcome and call appropriate callback
        if (failedIdsList.length === 0) {
          // All succeeded
          onSuccessRef.current?.(updatedIds, priority);
        } else if (updatedIds.length === 0) {
          // All failed - find first rejected result to get error message
          const firstRejected = results.find(
            (r): r is PromiseRejectedResult => r.status === 'rejected'
          );
          const errorMessage = firstRejected
            ? (firstRejected.reason as Error).message
            : 'All issues failed to update';
          setError(errorMessage);
          onErrorRef.current?.(new Error(errorMessage), failedIdsList);
        } else {
          // Partial success
          const errorMessage = `Updated ${updatedIds.length} of ${ids.length} issues`;
          setError(errorMessage);
          onPartialSuccessRef.current?.(updatedIds, failedIdsList, priority);
        }
      } catch (err) {
        // Unexpected error (shouldn't happen with allSettled, but be safe)
        if (!mountedRef.current) return;
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        setError(errorMessage);
        onErrorRef.current?.(err instanceof Error ? err : new Error(errorMessage), ids);
      } finally {
        if (mountedRef.current) {
          setIsLoading(false);
        }
      }
    },
    []
  );

  const openMenu = useCallback(() => setIsMenuOpen(true), []);
  const closeMenu = useCallback(() => setIsMenuOpen(false), []);

  /**
   * Reset error and failed state.
   */
  const reset = useCallback(() => {
    setError(null);
    setFailedIds(new Set());
    setSuccessCount(0);
  }, []);

  /**
   * Handle priority selection from dropdown.
   */
  const handlePrioritySelect = useCallback(
    (priority: Priority) => {
      bulkUpdatePriority(selectedIds, priority);
    },
    [bulkUpdatePriority, selectedIds]
  );

  /**
   * Render the priority action button with dropdown menu.
   */
  const renderAction = useCallback(
    (): React.ReactNode => (
      <div className={styles.container} ref={menuRef}>
        <button
          type="button"
          className={styles.button}
          onClick={() => setIsMenuOpen((prev) => !prev)}
          disabled={isLoading || selectedIds.size === 0}
          aria-haspopup="menu"
          aria-expanded={isMenuOpen}
          aria-label="Change priority"
          data-testid="bulk-priority-button"
        >
          {isLoading ? 'Updating...' : 'Priority'}
        </button>

        {isMenuOpen && (
          <div
            className={styles.menu}
            role="menu"
            aria-label="Select priority"
            data-testid="bulk-priority-menu"
          >
            {PRIORITY_OPTIONS.map((option) => (
              <button
                key={option.value}
                type="button"
                className={styles.menuItem}
                role="menuitem"
                onClick={() => handlePrioritySelect(option.value)}
                data-testid={`bulk-priority-option-${option.value}`}
              >
                {option.label}
              </button>
            ))}
          </div>
        )}
      </div>
    ),
    [isLoading, selectedIds.size, isMenuOpen, handlePrioritySelect]
  );

  return {
    bulkUpdatePriority,
    isLoading,
    error,
    failedIds,
    successCount,
    isMenuOpen,
    openMenu,
    closeMenu,
    renderAction,
    reset,
  };
}
