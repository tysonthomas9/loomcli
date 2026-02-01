/**
 * Toast component.
 * Individual toast notification with icon, message, and dismiss button.
 */

import { useCallback } from 'react'
import type { ToastType } from '@/hooks/useToast'
import styles from './Toast.module.css'

/**
 * Props for the Toast component.
 */
export interface ToastProps {
  /** Unique toast ID */
  id: string
  /** Message to display */
  message: string
  /** Toast type for styling */
  type: ToastType
  /** Callback when dismissed */
  onDismiss: (id: string) => void
  /** Additional CSS class */
  className?: string
}

/**
 * Icon component for each toast type.
 */
function TypeIcon({ type, className }: { type: ToastType; className?: string }): JSX.Element {
  const iconClass = className ? `${className} ${styles.icon}` : styles.icon

  switch (type) {
    case 'success':
      return (
        <svg
          className={iconClass}
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
          <path d="M9 12l2 2 4-4" />
        </svg>
      )
    case 'error':
      return (
        <svg
          className={iconClass}
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
      )
    case 'warning':
      return (
        <svg
          className={iconClass}
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
          <path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
      )
    case 'info':
    default:
      return (
        <svg
          className={iconClass}
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
          <line x1="12" y1="16" x2="12" y2="12" />
          <line x1="12" y1="8" x2="12.01" y2="8" />
        </svg>
      )
  }
}

/**
 * Close icon for the dismiss button.
 */
function CloseIcon(): JSX.Element {
  return (
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
  )
}

/**
 * Toast displays an individual notification with type-specific styling.
 * Includes an icon, message text, and dismiss button.
 */
export function Toast({
  id,
  message,
  type,
  onDismiss,
  className,
}: ToastProps): JSX.Element {
  const handleDismiss = useCallback(() => {
    onDismiss(id)
  }, [id, onDismiss])

  const rootClassName = [
    styles.toast,
    styles[type],
    className,
  ].filter(Boolean).join(' ')

  return (
    <div
      className={rootClassName}
      role="alert"
      aria-live={type === 'error' ? 'assertive' : 'polite'}
      data-testid={`toast-${type}`}
    >
      <TypeIcon type={type} />
      <span className={styles.message} title={message}>{message}</span>
      <button
        type="button"
        className={styles.dismissButton}
        onClick={handleDismiss}
        aria-label="Dismiss notification"
      >
        <CloseIcon />
      </button>
    </div>
  )
}
