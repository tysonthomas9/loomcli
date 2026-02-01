/**
 * BulkActionToolbar component for bulk issue operations.
 * Displays when one or more issues are selected, providing
 * action buttons and a selection count.
 */

import { useCallback } from 'react';
import styles from './BulkActionToolbar.module.css';

/**
 * Action configuration for the bulk action toolbar.
 */
export interface BulkAction {
  /** Unique identifier for the action */
  id: string;
  /** Display label for the button */
  label: string;
  /** Icon component or null for text-only */
  icon?: React.ReactNode;
  /** Handler called with selected IDs when clicked */
  onClick: (selectedIds: Set<string>) => void | Promise<void>;
  /** Whether the action is currently loading */
  loading?: boolean;
  /** Whether the action is disabled */
  disabled?: boolean;
  /** Button variant: primary (filled), secondary (outline), or danger (red) */
  variant?: 'primary' | 'secondary' | 'danger';
}

/**
 * Props for the BulkActionToolbar component.
 */
export interface BulkActionToolbarProps {
  /** Set of currently selected issue IDs */
  selectedIds: Set<string>;
  /** Clear all selections */
  onClearSelection: () => void;
  /** Array of bulk actions to display */
  actions?: BulkAction[];
  /** Additional CSS class name */
  className?: string;
}

/**
 * BulkActionToolbar displays a floating toolbar when issues are selected.
 * Shows selection count and provides action buttons for bulk operations.
 *
 * @example
 * ```tsx
 * <BulkActionToolbar
 *   selectedIds={selectedIds}
 *   onClearSelection={clearSelection}
 *   actions={[
 *     { id: 'close', label: 'Close', onClick: handleClose, variant: 'danger' },
 *   ]}
 * />
 * ```
 */
export function BulkActionToolbar({
  selectedIds,
  onClearSelection,
  actions = [],
  className,
}: BulkActionToolbarProps): JSX.Element | null {
  // Handle action button click - defined before early return to satisfy rules-of-hooks
  const handleActionClick = useCallback(
    (action: BulkAction) => {
      if (action.disabled || action.loading) return;
      action.onClick(selectedIds);
    },
    [selectedIds]
  );

  // Don't render if nothing is selected
  if (selectedIds.size === 0) {
    return null;
  }

  const count = selectedIds.size;
  const rootClassName = className ? `${styles.toolbar} ${className}` : styles.toolbar;

  return (
    <div
      className={rootClassName}
      role="toolbar"
      aria-label={`Bulk actions for ${count} selected issue${count !== 1 ? 's' : ''}`}
      data-testid="bulk-action-toolbar"
    >
      {/* Selection count */}
      <span className={styles.selectionCount} data-testid="selection-count">
        {count} selected
      </span>

      {/* Action buttons */}
      <div className={styles.actions}>
        {actions.map((action) => (
          <button
            key={action.id}
            type="button"
            className={`${styles.actionButton} ${styles[action.variant ?? 'secondary']}`}
            onClick={() => handleActionClick(action)}
            disabled={action.disabled || action.loading}
            aria-label={action.label}
            data-testid={`bulk-action-${action.id}`}
          >
            {action.icon && <span className={styles.icon}>{action.icon}</span>}
            <span>{action.loading ? 'Loading...' : action.label}</span>
          </button>
        ))}

        {/* Deselect all button */}
        <button
          type="button"
          className={`${styles.actionButton} ${styles.secondary}`}
          onClick={onClearSelection}
          aria-label="Clear selection"
          data-testid="bulk-action-clear"
        >
          Deselect all
        </button>
      </div>
    </div>
  );
}
