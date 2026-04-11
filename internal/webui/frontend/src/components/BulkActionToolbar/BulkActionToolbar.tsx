/**
 * BulkActionToolbar component for bulk issue operations.
 * Displays when one or more issues are selected, providing
 * action buttons and a selection count.
 */

import { useCallback, useEffect, useRef } from "react";

import type { BulkAction } from "@/types/common";
import { useAnnounce } from "@/hooks/ui";

import styles from "./BulkActionToolbar.module.css";

// Re-export so existing consumers that imported BulkAction from the
// BulkActionToolbar module (pre-Phase 7) keep compiling. New code should
// import from @/types directly.
export type { BulkAction } from "@/types/common";

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
  const { announce } = useAnnounce();
  const prevCountRef = useRef(selectedIds.size);

  // Announce selection count changes
  useEffect(() => {
    const count = selectedIds.size;
    if (count !== prevCountRef.current) {
      if (count > 0) {
        announce(`${count} issue${count !== 1 ? "s" : ""} selected`);
      }
      prevCountRef.current = count;
    }
  }, [selectedIds.size, announce]);

  // Handle action button click - defined before early return to satisfy rules-of-hooks
  const handleActionClick = useCallback(
    (action: BulkAction) => {
      if (action.disabled || action.loading) return;
      action.onClick(selectedIds);
    },
    [selectedIds],
  );

  // Don't render if nothing is selected
  if (selectedIds.size === 0) {
    return null;
  }

  const count = selectedIds.size;
  const rootClassName = className
    ? `${styles.toolbar} ${className}`
    : styles.toolbar;

  return (
    <div
      className={rootClassName}
      role="toolbar"
      aria-label={`Bulk actions for ${count} selected issue${count !== 1 ? "s" : ""}`}
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
            className={`${styles.actionButton} ${styles[action.variant ?? "secondary"]}`}
            onClick={() => handleActionClick(action)}
            disabled={action.disabled || action.loading}
            aria-label={action.label}
            data-testid={`bulk-action-${action.id}`}
          >
            {action.icon && <span className={styles.icon}>{action.icon}</span>}
            <span>{action.loading ? "Loading..." : action.label}</span>
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
