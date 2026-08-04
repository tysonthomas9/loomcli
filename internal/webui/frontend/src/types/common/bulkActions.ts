/**
 * Bulk action types for the BulkActionToolbar.
 *
 * Lives in src/types/ so hooks (e.g. useBulkClose) can reference the
 * shape without crossing the frontend layer DAG back into components.
 */

import type { ReactNode } from "react";

/**
 * Action configuration for the bulk action toolbar.
 */
export interface BulkAction {
  /** Unique identifier for the action */
  id: string;
  /** Display label for the button */
  label: string;
  /** Icon component or null for text-only */
  icon?: ReactNode;
  /** Handler called with selected IDs when clicked */
  onClick: (selectedIds: Set<string>) => void | Promise<void>;
  /** Whether the action is currently loading */
  loading?: boolean;
  /** Whether the action is disabled */
  disabled?: boolean;
  /** Button variant: primary (filled), secondary (outline), or danger (red) */
  variant?: "primary" | "secondary" | "danger";
}
