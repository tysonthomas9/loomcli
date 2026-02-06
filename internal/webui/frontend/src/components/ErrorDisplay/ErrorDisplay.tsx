/**
 * ErrorDisplay component.
 * Displays error messages with optional retry functionality.
 */

import type { ReactNode } from 'react';

import styles from './ErrorDisplay.module.css';

/**
 * Variant presets for common error scenarios.
 */
export type ErrorDisplayVariant = 'fetch-error' | 'connection-error' | 'unknown-error' | 'custom';

/**
 * Props for the ErrorDisplay component.
 */
export interface ErrorDisplayProps {
  /** Preset variant for common error scenarios */
  variant?: ErrorDisplayVariant;
  /** Custom title (overrides variant default) */
  title?: string;
  /** Custom description (overrides variant default) */
  description?: string;
  /** Original error object for displaying details */
  error?: Error | null;
  /** Callback when user clicks retry button */
  onRetry?: () => void;
  /** Whether retry is currently in progress */
  isRetrying?: boolean;
  /** Label for retry button (default: "Try again") */
  retryLabel?: string;
  /** Show technical error details (default: false) */
  showDetails?: boolean;
  /** Optional icon element (overrides default error icon) */
  icon?: ReactNode;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Default content for each variant.
 */
const VARIANT_DEFAULTS: Record<ErrorDisplayVariant, { title: string; description: string }> = {
  'fetch-error': {
    title: 'Failed to load data',
    description: 'There was a problem fetching the data. Please try again.',
  },
  'connection-error': {
    title: 'Connection lost',
    description: 'Unable to connect to the server. Please check your connection.',
  },
  'unknown-error': {
    title: 'Something went wrong',
    description: 'An unexpected error occurred. Please try again later.',
  },
  custom: {
    title: 'Error',
    description: '',
  },
};

/**
 * Default error icon SVG (circle with exclamation).
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
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
  );
}

/**
 * ErrorDisplay shows an error message with optional retry functionality.
 * Used when API calls fail or connections are lost.
 *
 * @example
 * ```tsx
 * // Basic usage
 * <ErrorDisplay variant="fetch-error" onRetry={refetch} />
 *
 * // With error details
 * <ErrorDisplay
 *   variant="fetch-error"
 *   error={error}
 *   showDetails
 *   onRetry={refetch}
 *   isRetrying={isLoading}
 * />
 *
 * // Custom content
 * <ErrorDisplay
 *   variant="custom"
 *   title="Permission denied"
 *   description="You don't have access to this resource."
 * />
 * ```
 */
export function ErrorDisplay({
  variant = 'fetch-error',
  title,
  description,
  error,
  onRetry,
  isRetrying = false,
  retryLabel = 'Try again',
  showDetails = false,
  icon,
  className,
}: ErrorDisplayProps): JSX.Element {
  const defaults = VARIANT_DEFAULTS[variant];
  const displayTitle = title ?? defaults.title;
  const displayDescription = description ?? defaults.description;

  const rootClassName = className ? `${styles.errorDisplay} ${className}` : styles.errorDisplay;

  return (
    <div
      className={rootClassName}
      role="alert"
      aria-live="assertive"
      data-testid="error-display"
      data-variant={variant}
    >
      <div className={styles.iconWrapper}>{icon ?? <DefaultIcon />}</div>
      <h3 className={styles.title}>{displayTitle}</h3>
      {displayDescription && <p className={styles.description}>{displayDescription}</p>}

      {showDetails && error?.message && (
        <details className={styles.details}>
          <summary className={styles.detailsSummary}>Technical details</summary>
          <pre className={styles.detailsContent}>{error.message}</pre>
        </details>
      )}

      {onRetry && (
        <div className={styles.action}>
          <button
            type="button"
            onClick={onRetry}
            disabled={isRetrying}
            className={styles.retryButton}
            data-testid="retry-button"
          >
            {isRetrying ? 'Retrying...' : retryLabel}
          </button>
        </div>
      )}
    </div>
  );
}
