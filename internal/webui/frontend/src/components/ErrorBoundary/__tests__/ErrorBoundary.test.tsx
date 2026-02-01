/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ErrorBoundary component.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import { ErrorBoundary } from '../ErrorBoundary';

/**
 * Component that throws an error on render.
 */
function ThrowingComponent({ shouldThrow = true }: { shouldThrow?: boolean }): JSX.Element {
  if (shouldThrow) {
    throw new Error('Test error message');
  }
  return <div data-testid="normal-content">Normal content</div>;
}

// Store original console.error
const originalError = console.error;

describe('ErrorBoundary', () => {
  beforeEach(() => {
    // Silence console.error during tests since React logs caught errors
    console.error = vi.fn();
  });

  afterEach(() => {
    console.error = originalError;
  });

  describe('normal rendering', () => {
    it('renders children when no error occurs', () => {
      render(
        <ErrorBoundary>
          <div data-testid="child">Child content</div>
        </ErrorBoundary>
      );
      expect(screen.getByTestId('child')).toBeInTheDocument();
      expect(screen.getByText('Child content')).toBeInTheDocument();
    });

    it('does not show fallback when no error', () => {
      render(
        <ErrorBoundary>
          <div>Normal content</div>
        </ErrorBoundary>
      );
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
    });

    it('renders multiple children correctly', () => {
      render(
        <ErrorBoundary>
          <div data-testid="child-1">First</div>
          <div data-testid="child-2">Second</div>
        </ErrorBoundary>
      );
      expect(screen.getByTestId('child-1')).toBeInTheDocument();
      expect(screen.getByTestId('child-2')).toBeInTheDocument();
    });
  });

  describe('error catching', () => {
    it('catches errors and shows default fallback', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByTestId('error-display')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: /Something went wrong/i })).toBeInTheDocument();
    });

    it('shows error message in details', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByText('Test error message')).toBeInTheDocument();
    });

    it('logs error to console', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(console.error).toHaveBeenCalled();
    });

    it('shows description in default fallback', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(
        screen.getByText('An error occurred while rendering this component.')
      ).toBeInTheDocument();
    });
  });

  describe('custom fallback', () => {
    it('renders custom fallback element', () => {
      render(
        <ErrorBoundary fallback={<div data-testid="custom-fallback">Custom error UI</div>}>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByTestId('custom-fallback')).toBeInTheDocument();
      expect(screen.getByText('Custom error UI')).toBeInTheDocument();
    });

    it('calls fallback function with error and reset', () => {
      const fallbackFn = vi.fn((error: Error, _resetError: () => void) => (
        <div>
          <span data-testid="error-msg">{error.message}</span>
          <button data-testid="reset-btn" onClick={_resetError}>
            Reset
          </button>
        </div>
      ));

      render(
        <ErrorBoundary fallback={fallbackFn}>
          <ThrowingComponent />
        </ErrorBoundary>
      );

      expect(fallbackFn).toHaveBeenCalled();
      expect(screen.getByTestId('error-msg')).toHaveTextContent('Test error message');
    });

    it('renders custom fallback instead of default ErrorDisplay', () => {
      render(
        <ErrorBoundary fallback={<span>Oops!</span>}>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByText('Oops!')).toBeInTheDocument();
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
    });
  });

  describe('onError callback', () => {
    it('calls onError with error and errorInfo', () => {
      const handleError = vi.fn();
      render(
        <ErrorBoundary onError={handleError}>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(handleError).toHaveBeenCalledTimes(1);
      expect(handleError).toHaveBeenCalledWith(
        expect.any(Error),
        expect.objectContaining({ componentStack: expect.any(String) })
      );
    });

    it('does not call onError when no error occurs', () => {
      const handleError = vi.fn();
      render(
        <ErrorBoundary onError={handleError}>
          <div>Safe content</div>
        </ErrorBoundary>
      );
      expect(handleError).not.toHaveBeenCalled();
    });
  });

  describe('reset functionality', () => {
    it('shows retry button in default fallback', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    });

    it('resets error state when retry is clicked and error is fixed', () => {
      const { rerender } = render(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('error-display')).toBeInTheDocument();

      // Simulate fixing the error by rerendering with shouldThrow=false
      rerender(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow={false} />
        </ErrorBoundary>
      );

      fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

      // After reset with fixed component, should show normal content
      expect(screen.getByTestId('normal-content')).toBeInTheDocument();
    });

    it('reset via custom fallback function works', () => {
      const { rerender } = render(
        <ErrorBoundary
          fallback={(error, resetError) => (
            <button data-testid="custom-reset" onClick={resetError}>
              Reset from {error.message}
            </button>
          )}
        >
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('custom-reset')).toBeInTheDocument();

      rerender(
        <ErrorBoundary
          fallback={(error, resetError) => (
            <button data-testid="custom-reset" onClick={resetError}>
              Reset from {error.message}
            </button>
          )}
        >
          <ThrowingComponent shouldThrow={false} />
        </ErrorBoundary>
      );

      fireEvent.click(screen.getByTestId('custom-reset'));
      expect(screen.getByTestId('normal-content')).toBeInTheDocument();
    });

    it('resets when resetOnChange key changes', () => {
      const { rerender } = render(
        <ErrorBoundary resetOnChange={['key1']}>
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('error-display')).toBeInTheDocument();

      // Change the key and fix the error
      rerender(
        <ErrorBoundary resetOnChange={['key2']}>
          <ThrowingComponent shouldThrow={false} />
        </ErrorBoundary>
      );

      // Should auto-reset and show normal content
      expect(screen.getByTestId('normal-content')).toBeInTheDocument();
    });

    it('does not reset when resetOnChange key stays the same', () => {
      const { rerender } = render(
        <ErrorBoundary resetOnChange={['key1']}>
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('error-display')).toBeInTheDocument();

      // Keep the same key
      rerender(
        <ErrorBoundary resetOnChange={['key1']}>
          <ThrowingComponent shouldThrow={false} />
        </ErrorBoundary>
      );

      // Should still show error since key didn't change
      expect(screen.getByTestId('error-display')).toBeInTheDocument();
    });

    it('handles complex resetOnChange values', () => {
      const { rerender } = render(
        <ErrorBoundary resetOnChange={[{ id: 1 }, 'path']}>
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('error-display')).toBeInTheDocument();

      // Change complex key
      rerender(
        <ErrorBoundary resetOnChange={[{ id: 2 }, 'path']}>
          <ThrowingComponent shouldThrow={false} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('normal-content')).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('default fallback has alert role', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('default fallback has aria-live assertive', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent />
        </ErrorBoundary>
      );
      expect(screen.getByTestId('error-display')).toHaveAttribute('aria-live', 'assertive');
    });
  });

  describe('edge cases', () => {
    it('handles undefined children', () => {
      render(<ErrorBoundary>{undefined}</ErrorBoundary>);
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
    });

    it('handles null children', () => {
      render(<ErrorBoundary>{null}</ErrorBoundary>);
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
    });

    it('handles errors with no message', () => {
      const ThrowEmptyError = (): JSX.Element => {
        throw new Error('');
      };

      render(
        <ErrorBoundary>
          <ThrowEmptyError />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('error-display')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: /Something went wrong/i })).toBeInTheDocument();
    });

    it('nested error boundaries - inner catches error', () => {
      render(
        <ErrorBoundary fallback={<span data-testid="outer">Outer fallback</span>}>
          <div>
            <ErrorBoundary fallback={<span data-testid="inner">Inner fallback</span>}>
              <ThrowingComponent />
            </ErrorBoundary>
          </div>
        </ErrorBoundary>
      );

      // Inner boundary should catch the error
      expect(screen.getByTestId('inner')).toBeInTheDocument();
      expect(screen.queryByTestId('outer')).not.toBeInTheDocument();
    });

    it('error after reset re-catches correctly', () => {
      const { rerender } = render(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByTestId('error-display')).toBeInTheDocument();

      // Fix error and reset
      rerender(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow={false} />
        </ErrorBoundary>
      );
      fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
      expect(screen.getByTestId('normal-content')).toBeInTheDocument();

      // Throw error again
      rerender(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow={true} />
        </ErrorBoundary>
      );

      // Should catch again
      expect(screen.getByTestId('error-display')).toBeInTheDocument();
    });
  });
});
