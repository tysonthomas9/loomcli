/**
 * ErrorToast component.
 * Displays a transient error notification that auto-dismisses.
 * Used to show rollback error messages when Kanban drag-and-drop status changes fail.
 */

import { useEffect, useCallback } from 'react';

import styles from './ErrorToast.module.css';

/**
 * Props for the ErrorToast component.
 */
export interface ErrorToastProps {
  /** Error message to display */
  message: string;
  /** Callback when toast is dismissed (auto or manual) */
  onDismiss: () => void;
  /** Auto-dismiss duration in milliseconds (default: 5000) */
  duration?: number;
  /** Additional CSS class name */
  className?: string;
  /** Test ID for testing */
  testId?: string;
}

/**
 * ErrorToast displays a transient error notification in the bottom-right corner.
 * It automatically dismisses after a configurable duration and can be manually dismissed.
 *
 * @example
 * ```tsx
 * function MyComponent() {
 *   const [error, setError] = useState<string | null>(null)
 *
 *   return (
 *     <>
 *       <button onClick={() => setError('Something went wrong')}>
 *         Trigger Error
 *       </button>
 *       {error && (
 *         <ErrorToast
 *           message={error}
 *           onDismiss={() => setError(null)}
 *           duration={5000}
 *         />
 *       )}
 *     </>
 *   )
 * }
 * ```
 */
export function ErrorToast({
  message,
  onDismiss,
  duration = 5000,
  className,
  testId = 'error-toast',
}: ErrorToastProps): JSX.Element {
  // Auto-dismiss after duration
  useEffect(() => {
    if (duration <= 0) return;

    const timeoutId = setTimeout(() => {
      onDismiss();
    }, duration);

    return () => clearTimeout(timeoutId);
  }, [duration, onDismiss]);

  // Handle dismiss button click
  const handleDismiss = useCallback(() => {
    onDismiss();
  }, [onDismiss]);

  // Handle keyboard dismiss (Escape key)
  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'Escape') {
        onDismiss();
      }
    },
    [onDismiss]
  );

  const rootClassName = className ? `${styles.toast} ${className}` : styles.toast;

  return (
    <div
      className={rootClassName}
      role="alert"
      aria-live="assertive"
      data-testid={testId}
      onKeyDown={handleKeyDown}
    >
      <div className={styles.content}>
        <svg
          className={styles.icon}
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" />
          <line x1="12" y1="8" x2="12" y2="12" />
          <line x1="12" y1="16" x2="12.01" y2="16" />
        </svg>
        <span className={styles.message}>{message}</span>
      </div>
      <button
        type="button"
        className={styles.dismissButton}
        onClick={handleDismiss}
        aria-label="Dismiss error"
      >
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <line x1="18" y1="6" x2="6" y2="18" />
          <line x1="6" y1="6" x2="18" y2="18" />
        </svg>
      </button>
    </div>
  );
}
