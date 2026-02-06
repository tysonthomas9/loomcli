/**
 * EmptyState component.
 * Displays a placeholder when no data is available or no results match filters.
 */

import type { ReactNode } from 'react';

import styles from './EmptyState.module.css';

/**
 * Variant presets for common empty state scenarios.
 */
export type EmptyStateVariant = 'no-results' | 'no-issues' | 'custom';

/**
 * Props for the EmptyState component.
 */
export interface EmptyStateProps {
  /** Preset variant for common scenarios */
  variant?: EmptyStateVariant;
  /** Custom title (overrides variant default) */
  title?: string;
  /** Custom description (overrides variant default) */
  description?: string;
  /** Optional action element (e.g., "Clear filters" button) */
  action?: ReactNode;
  /** Optional icon element */
  icon?: ReactNode;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Default content for each variant.
 */
const VARIANT_DEFAULTS: Record<EmptyStateVariant, { title: string; description: string }> = {
  'no-results': {
    title: 'No results found',
    description: 'Try adjusting your search or filter criteria.',
  },
  'no-issues': {
    title: 'No issues yet',
    description: 'Create your first issue to get started.',
  },
  custom: {
    title: 'Nothing to show',
    description: '',
  },
};

/**
 * Default search icon SVG.
 */
function DefaultIcon(): JSX.Element {
  return (
    <svg
      className={styles.icon}
      width="48"
      height="48"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="11" cy="11" r="8" />
      <path d="M21 21l-4.35-4.35" />
      <path d="M8 11h6" />
    </svg>
  );
}

/**
 * EmptyState displays a placeholder with icon, title, description, and optional action.
 * Used when no data is available or no results match the current filters.
 *
 * @example
 * ```tsx
 * // No search results
 * <EmptyState
 *   variant="no-results"
 *   action={<button onClick={clearFilters}>Clear filters</button>}
 * />
 *
 * // No issues exist
 * <EmptyState variant="no-issues" />
 *
 * // Custom content
 * <EmptyState
 *   variant="custom"
 *   title="Connection lost"
 *   description="Please check your internet connection."
 *   icon={<ErrorIcon />}
 * />
 * ```
 */
export function EmptyState({
  variant = 'no-results',
  title,
  description,
  action,
  icon,
  className,
}: EmptyStateProps): JSX.Element {
  const defaults = VARIANT_DEFAULTS[variant];

  const displayTitle = title ?? defaults.title;
  const displayDescription = description ?? defaults.description;

  const rootClassName = className ? `${styles.emptyState} ${className}` : styles.emptyState;

  return (
    <div className={rootClassName} data-testid="empty-state" data-variant={variant} role="status">
      <div className={styles.iconWrapper}>{icon ?? <DefaultIcon />}</div>
      <h3 className={styles.title}>{displayTitle}</h3>
      {displayDescription && <p className={styles.description}>{displayDescription}</p>}
      {action && <div className={styles.action}>{action}</div>}
    </div>
  );
}
