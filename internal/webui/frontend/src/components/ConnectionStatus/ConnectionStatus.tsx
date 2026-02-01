/**
 * ConnectionStatus component displays the current connection state.
 * Provides visual feedback about whether the application is connected to the beads daemon.
 */

import type { ConnectionState } from '@/api/sse';
import styles from './ConnectionStatus.module.css';

/**
 * Props for the ConnectionStatus component.
 */
export interface ConnectionStatusProps {
  /** Current connection state from useSSE */
  state: ConnectionState;
  /** Additional CSS class name */
  className?: string;
  /** Display variant */
  variant?: 'badge' | 'inline';
  /** Show status text (default: true) */
  showText?: boolean;
  /** Current reconnect attempt count (from useSSE) */
  reconnectAttempts?: number;
  /** Callback when retry button is clicked */
  onRetry?: () => void;
  /** Whether to show retry button when reconnecting (default: true) */
  showRetryButton?: boolean;
}

/**
 * Map connection state to user-friendly display text.
 */
function getStatusText(state: ConnectionState, reconnectAttempts?: number): string {
  switch (state) {
    case 'connected':
      return 'Connected';
    case 'connecting':
      return 'Connecting...';
    case 'reconnecting':
      if (reconnectAttempts !== undefined && reconnectAttempts > 0) {
        return `Reconnecting (attempt ${reconnectAttempts})...`;
      }
      return 'Reconnecting...';
    case 'disconnected':
      return 'Disconnected';
    default:
      return 'Unknown';
  }
}

/**
 * ConnectionStatus displays the current connection state.
 * Shows a status indicator dot and optional status text.
 */
export function ConnectionStatus({
  state,
  className,
  variant = 'inline',
  showText = true,
  reconnectAttempts,
  onRetry,
  showRetryButton = true,
}: ConnectionStatusProps): JSX.Element {
  const statusText = getStatusText(state, reconnectAttempts);

  const rootClassName = [styles.connectionStatus, styles[variant], className]
    .filter(Boolean)
    .join(' ');

  // Show retry button when reconnecting with attempts >= 1 and callback provided
  const shouldShowRetry =
    state === 'reconnecting' &&
    showRetryButton &&
    onRetry !== undefined &&
    reconnectAttempts !== undefined &&
    reconnectAttempts >= 1;

  return (
    <div
      className={rootClassName}
      role="status"
      aria-live="polite"
      aria-label={`Connection status: ${statusText}`}
      data-state={state}
      data-variant={variant}
    >
      <span className={styles.indicator} aria-hidden="true" />
      {showText && <span className={styles.text}>{statusText}</span>}
      {shouldShowRetry && (
        <button
          type="button"
          className={styles.retryButton}
          onClick={onRetry}
          aria-label="Retry connection now"
        >
          Retry Now
        </button>
      )}
    </div>
  );
}
