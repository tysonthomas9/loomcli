/**
 * ErrorBoundary component.
 * Catches JavaScript errors during rendering and displays a fallback UI.
 */

import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

import { ErrorDisplay } from '@/components/ErrorDisplay';

/**
 * State for the ErrorBoundary component.
 */
interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

/**
 * Props for the ErrorBoundary component.
 */
export interface ErrorBoundaryProps {
  /** Children to render */
  children: ReactNode;
  /** Custom fallback UI (defaults to ErrorDisplay) */
  fallback?: ReactNode | ((error: Error, resetError: () => void) => ReactNode);
  /** Called when error is caught */
  onError?: (error: Error, errorInfo: ErrorInfo) => void;
  /** Key to reset error state (changing this clears the error) */
  resetOnChange?: unknown[];
}

/**
 * ErrorBoundary catches JavaScript errors anywhere in the child component tree,
 * logs those errors, and displays a fallback UI instead of crashing.
 *
 * @example
 * ```tsx
 * // Basic usage - wraps entire app
 * <ErrorBoundary>
 *   <App />
 * </ErrorBoundary>
 *
 * // With custom fallback
 * <ErrorBoundary fallback={<div>Something went wrong</div>}>
 *   <RiskyComponent />
 * </ErrorBoundary>
 *
 * // With error callback for logging
 * <ErrorBoundary onError={(error, info) => logToService(error, info)}>
 *   <App />
 * </ErrorBoundary>
 *
 * // With reset key (auto-reset when key changes)
 * <ErrorBoundary resetOnChange={[selectedId]}>
 *   <DetailView id={selectedId} />
 * </ErrorBoundary>
 * ```
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = {
    hasError: false,
    error: null,
  };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    console.error('ErrorBoundary caught an error:', error, errorInfo.componentStack);
    this.props.onError?.(error, errorInfo);
  }

  componentDidUpdate(prevProps: ErrorBoundaryProps): void {
    // Reset error state if resetOnChange key changed
    if (this.state.hasError && this.props.resetOnChange) {
      try {
        const prevKey = JSON.stringify(prevProps.resetOnChange);
        const nextKey = JSON.stringify(this.props.resetOnChange);
        if (prevKey !== nextKey) {
          this.resetError();
        }
      } catch {
        // If serialization fails (circular refs, etc.), fall back to reference equality
        if (prevProps.resetOnChange !== this.props.resetOnChange) {
          this.resetError();
        }
      }
    }
  }

  resetError = (): void => {
    this.setState({ hasError: false, error: null });
  };

  render(): ReactNode {
    if (this.state.hasError) {
      const { fallback } = this.props;
      const error = this.state.error ?? new Error('Unknown error');

      // Custom fallback (function or element)
      if (typeof fallback === 'function') {
        return fallback(error, this.resetError);
      }
      if (fallback !== undefined) {
        return fallback;
      }

      // Default fallback using ErrorDisplay
      return (
        <ErrorDisplay
          variant="unknown-error"
          title="Something went wrong"
          description="An error occurred while rendering this component."
          error={error}
          showDetails
          onRetry={this.resetError}
          retryLabel="Try again"
        />
      );
    }

    return this.props.children;
  }
}
