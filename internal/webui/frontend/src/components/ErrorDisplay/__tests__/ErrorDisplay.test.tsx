/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ErrorDisplay component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import { ErrorDisplay } from '../ErrorDisplay';

describe('ErrorDisplay', () => {
  describe('rendering', () => {
    it('renders with default fetch-error variant', () => {
      render(<ErrorDisplay />);
      expect(screen.getByTestId('error-display')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Failed to load data' })).toBeInTheDocument();
      expect(
        screen.getByText('There was a problem fetching the data. Please try again.')
      ).toBeInTheDocument();
    });

    it('renders with connection-error variant', () => {
      render(<ErrorDisplay variant="connection-error" />);
      expect(screen.getByRole('heading', { name: 'Connection lost' })).toBeInTheDocument();
      expect(screen.getByText(/Unable to connect/)).toBeInTheDocument();
    });

    it('renders with unknown-error variant', () => {
      render(<ErrorDisplay variant="unknown-error" />);
      expect(screen.getByRole('heading', { name: 'Something went wrong' })).toBeInTheDocument();
    });

    it('renders with custom variant', () => {
      render(<ErrorDisplay variant="custom" />);
      expect(screen.getByRole('heading', { name: 'Error' })).toBeInTheDocument();
    });

    it('renders with alert role for accessibility', () => {
      render(<ErrorDisplay />);
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('renders default error icon', () => {
      const { container } = render(<ErrorDisplay />);
      const svg = container.querySelector('svg');
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute('aria-hidden', 'true');
    });
  });

  describe('custom content', () => {
    it('renders custom title', () => {
      render(<ErrorDisplay title="Custom Error Title" />);
      expect(screen.getByRole('heading', { name: 'Custom Error Title' })).toBeInTheDocument();
    });

    it('renders custom description', () => {
      render(<ErrorDisplay description="Custom error description" />);
      expect(screen.getByText('Custom error description')).toBeInTheDocument();
    });

    it('custom title overrides variant default', () => {
      render(<ErrorDisplay variant="fetch-error" title="Override Title" />);
      expect(screen.getByRole('heading', { name: 'Override Title' })).toBeInTheDocument();
      expect(screen.queryByText('Failed to load data')).not.toBeInTheDocument();
    });

    it('custom description overrides variant default', () => {
      render(<ErrorDisplay variant="fetch-error" description="Override desc" />);
      expect(screen.getByText('Override desc')).toBeInTheDocument();
      expect(
        screen.queryByText('There was a problem fetching the data. Please try again.')
      ).not.toBeInTheDocument();
    });

    it('renders custom icon', () => {
      const CustomIcon = () => <span data-testid="custom-icon">Icon</span>;
      render(<ErrorDisplay icon={<CustomIcon />} />);
      expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
    });
  });

  describe('retry functionality', () => {
    it('renders retry button when onRetry is provided', () => {
      render(<ErrorDisplay onRetry={() => {}} />);
      expect(screen.getByTestId('retry-button')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    });

    it('does not render retry button when onRetry is not provided', () => {
      render(<ErrorDisplay />);
      expect(screen.queryByTestId('retry-button')).not.toBeInTheDocument();
    });

    it('calls onRetry when retry button is clicked', () => {
      const handleRetry = vi.fn();
      render(<ErrorDisplay onRetry={handleRetry} />);
      fireEvent.click(screen.getByTestId('retry-button'));
      expect(handleRetry).toHaveBeenCalledTimes(1);
    });

    it('shows custom retry label', () => {
      render(<ErrorDisplay onRetry={() => {}} retryLabel="Reload" />);
      expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument();
    });

    it('shows retrying state when isRetrying is true', () => {
      render(<ErrorDisplay onRetry={() => {}} isRetrying />);
      expect(screen.getByRole('button', { name: 'Retrying...' })).toBeInTheDocument();
    });

    it('disables button when isRetrying is true', () => {
      render(<ErrorDisplay onRetry={() => {}} isRetrying />);
      expect(screen.getByTestId('retry-button')).toBeDisabled();
    });

    it('does not call onRetry when button is disabled', () => {
      const handleRetry = vi.fn();
      render(<ErrorDisplay onRetry={handleRetry} isRetrying />);
      fireEvent.click(screen.getByTestId('retry-button'));
      expect(handleRetry).not.toHaveBeenCalled();
    });
  });

  describe('error details', () => {
    it('does not show details by default', () => {
      const error = new Error('Test error message');
      render(<ErrorDisplay error={error} />);
      expect(screen.queryByText('Technical details')).not.toBeInTheDocument();
    });

    it('shows details when showDetails is true', () => {
      const error = new Error('Test error message');
      render(<ErrorDisplay error={error} showDetails />);
      expect(screen.getByText('Technical details')).toBeInTheDocument();
    });

    it('displays error message in details', () => {
      const error = new Error('Specific error message');
      render(<ErrorDisplay error={error} showDetails />);
      expect(screen.getByText('Specific error message')).toBeInTheDocument();
    });

    it('does not show details when error is null', () => {
      render(<ErrorDisplay error={null} showDetails />);
      expect(screen.queryByText('Technical details')).not.toBeInTheDocument();
    });

    it('does not show details when error is undefined', () => {
      render(<ErrorDisplay error={undefined} showDetails />);
      expect(screen.queryByText('Technical details')).not.toBeInTheDocument();
    });

    it('does not show details when error has no message', () => {
      const error = new Error('');
      render(<ErrorDisplay error={error} showDetails />);
      expect(screen.queryByText('Technical details')).not.toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies className prop to root element', () => {
      render(<ErrorDisplay className="custom-class" />);
      expect(screen.getByTestId('error-display')).toHaveClass('custom-class');
    });

    it('applies data-variant attribute', () => {
      render(<ErrorDisplay variant="connection-error" />);
      expect(screen.getByTestId('error-display')).toHaveAttribute('data-variant', 'connection-error');
    });

    it('applies data-variant for custom variant', () => {
      render(<ErrorDisplay variant="custom" />);
      expect(screen.getByTestId('error-display')).toHaveAttribute('data-variant', 'custom');
    });
  });

  describe('accessibility', () => {
    it('has alert role for screen readers', () => {
      render(<ErrorDisplay />);
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('has aria-live assertive', () => {
      render(<ErrorDisplay />);
      expect(screen.getByTestId('error-display')).toHaveAttribute('aria-live', 'assertive');
    });

    it('icon is hidden from screen readers', () => {
      const { container } = render(<ErrorDisplay />);
      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('aria-hidden', 'true');
    });

    it('title is a heading element', () => {
      render(<ErrorDisplay title="Test heading" />);
      const heading = screen.getByRole('heading', { name: 'Test heading' });
      expect(heading.tagName).toBe('H3');
    });
  });

  describe('edge cases', () => {
    it('renders with empty description when variant has empty default', () => {
      const { container } = render(<ErrorDisplay variant="custom" />);
      // custom variant has empty description by default
      const paragraphs = container.querySelectorAll('p');
      expect(paragraphs.length).toBe(0);
    });

    it('renders with all optional props', () => {
      const CustomIcon = () => <span data-testid="custom-icon">Icon</span>;
      const error = new Error('Test error');
      const handleRetry = vi.fn();

      render(
        <ErrorDisplay
          variant="custom"
          title="All props title"
          description="All props description"
          icon={<CustomIcon />}
          error={error}
          showDetails
          onRetry={handleRetry}
          isRetrying={false}
          retryLabel="Retry now"
          className="custom-all-props"
        />
      );

      expect(screen.getByRole('heading', { name: 'All props title' })).toBeInTheDocument();
      expect(screen.getByText('All props description')).toBeInTheDocument();
      expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
      expect(screen.getByText('Test error')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Retry now' })).toBeInTheDocument();
      expect(screen.getByTestId('error-display')).toHaveClass('custom-all-props');
    });

    it('renders correctly when description is undefined', () => {
      render(<ErrorDisplay title="Title only" description={undefined} />);
      expect(screen.getByRole('heading', { name: 'Title only' })).toBeInTheDocument();
      // Should still show default description
      expect(
        screen.getByText('There was a problem fetching the data. Please try again.')
      ).toBeInTheDocument();
    });

    it('handles long error messages with word wrap', () => {
      const longMessage =
        'This is a very long error message that should be wrapped properly in the error details section without breaking the layout';
      const error = new Error(longMessage);
      render(<ErrorDisplay error={error} showDetails />);
      expect(screen.getByText(longMessage)).toBeInTheDocument();
    });
  });
});
