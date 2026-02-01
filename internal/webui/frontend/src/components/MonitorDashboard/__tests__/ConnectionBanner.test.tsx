/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ConnectionBanner component.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { ConnectionBanner } from '../ConnectionBanner';

describe('ConnectionBanner', () => {
  /**
   * Default props for most tests.
   */
  const defaultProps = {
    lastUpdated: new Date(),
    retryCountdown: 0,
    isReconnecting: false,
    onRetry: vi.fn(),
  };

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-28T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe('rendering warning message and last updated time', () => {
    it('renders the disconnected warning message', () => {
      render(<ConnectionBanner {...defaultProps} />);

      expect(screen.getByText('Disconnected from loom server')).toBeInTheDocument();
    });

    it('renders the warning icon', () => {
      render(<ConnectionBanner {...defaultProps} />);

      expect(screen.getByText('⚠️')).toBeInTheDocument();
    });

    it('has role="alert" for accessibility', () => {
      render(<ConnectionBanner {...defaultProps} />);

      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('shows "just now" for recent updates (< 60 seconds)', () => {
      const lastUpdated = new Date('2026-01-28T11:59:30Z'); // 30 seconds ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated just now')).toBeInTheDocument();
    });

    it('shows minutes for updates within the hour', () => {
      const lastUpdated = new Date('2026-01-28T11:55:00Z'); // 5 minutes ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated 5 minutes ago')).toBeInTheDocument();
    });

    it('shows singular minute for 1 minute ago', () => {
      const lastUpdated = new Date('2026-01-28T11:59:00Z'); // 1 minute ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated 1 minute ago')).toBeInTheDocument();
    });

    it('shows hours for updates within a day', () => {
      const lastUpdated = new Date('2026-01-28T09:00:00Z'); // 3 hours ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated 3 hours ago')).toBeInTheDocument();
    });

    it('shows singular hour for 1 hour ago', () => {
      const lastUpdated = new Date('2026-01-28T11:00:00Z'); // 1 hour ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated 1 hour ago')).toBeInTheDocument();
    });

    it('shows days for updates older than a day', () => {
      const lastUpdated = new Date('2026-01-26T12:00:00Z'); // 2 days ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated 2 days ago')).toBeInTheDocument();
    });

    it('shows singular day for 1 day ago', () => {
      const lastUpdated = new Date('2026-01-27T12:00:00Z'); // 1 day ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      expect(screen.getByText('Last updated 1 day ago')).toBeInTheDocument();
    });

    it('shows "unknown" when lastUpdated is null', () => {
      render(<ConnectionBanner {...defaultProps} lastUpdated={null} />);

      expect(screen.getByText('Last updated unknown')).toBeInTheDocument();
    });
  });

  describe('retry button callback', () => {
    it('renders the retry button', () => {
      render(<ConnectionBanner {...defaultProps} />);

      expect(screen.getByRole('button', { name: /retry connection now/i })).toBeInTheDocument();
    });

    it('shows "Retry Now" text on the button', () => {
      render(<ConnectionBanner {...defaultProps} />);

      expect(screen.getByRole('button')).toHaveTextContent('Retry Now');
    });

    it('calls onRetry when retry button is clicked', () => {
      const onRetry = vi.fn();
      render(<ConnectionBanner {...defaultProps} onRetry={onRetry} />);

      fireEvent.click(screen.getByRole('button', { name: /retry connection now/i }));

      expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it('calls onRetry multiple times on multiple clicks', () => {
      const onRetry = vi.fn();
      render(<ConnectionBanner {...defaultProps} onRetry={onRetry} />);

      const button = screen.getByRole('button', { name: /retry connection now/i });
      fireEvent.click(button);
      fireEvent.click(button);
      fireEvent.click(button);

      expect(onRetry).toHaveBeenCalledTimes(3);
    });
  });

  describe('countdown display when retryCountdown > 0', () => {
    it('shows countdown when retryCountdown is greater than 0', () => {
      render(<ConnectionBanner {...defaultProps} retryCountdown={10} />);

      expect(screen.getByText('Retrying in 10s')).toBeInTheDocument();
    });

    it('shows different countdown values', () => {
      const { rerender } = render(<ConnectionBanner {...defaultProps} retryCountdown={30} />);
      expect(screen.getByText('Retrying in 30s')).toBeInTheDocument();

      rerender(<ConnectionBanner {...defaultProps} retryCountdown={5} />);
      expect(screen.getByText('Retrying in 5s')).toBeInTheDocument();

      rerender(<ConnectionBanner {...defaultProps} retryCountdown={1} />);
      expect(screen.getByText('Retrying in 1s')).toBeInTheDocument();
    });

    it('does not show countdown when retryCountdown is 0', () => {
      render(<ConnectionBanner {...defaultProps} retryCountdown={0} />);

      expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
    });

    it('hides countdown when retryCountdown changes to 0', () => {
      const { rerender } = render(<ConnectionBanner {...defaultProps} retryCountdown={5} />);
      expect(screen.getByText('Retrying in 5s')).toBeInTheDocument();

      rerender(<ConnectionBanner {...defaultProps} retryCountdown={0} />);
      expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
    });
  });

  describe('stale data indicator (>5 min old shows stronger warning)', () => {
    it('does not set data-stale attribute for recent data (< 5 minutes)', () => {
      const lastUpdated = new Date('2026-01-28T11:56:00Z'); // 4 minutes ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      const banner = screen.getByRole('alert');
      expect(banner).not.toHaveAttribute('data-stale');
    });

    it('sets data-stale="true" for data older than 5 minutes', () => {
      const lastUpdated = new Date('2026-01-28T11:54:00Z'); // 6 minutes ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      const banner = screen.getByRole('alert');
      expect(banner).toHaveAttribute('data-stale', 'true');
    });

    it('sets data-stale="true" for data exactly at 5 minute boundary', () => {
      // 5 minutes and 1 second ago - should be stale
      const lastUpdated = new Date('2026-01-28T11:54:59Z');
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      const banner = screen.getByRole('alert');
      expect(banner).toHaveAttribute('data-stale', 'true');
    });

    it('does not set data-stale when lastUpdated is null', () => {
      render(<ConnectionBanner {...defaultProps} lastUpdated={null} />);

      const banner = screen.getByRole('alert');
      expect(banner).not.toHaveAttribute('data-stale');
    });

    it('sets data-stale="true" for very old data (hours)', () => {
      const lastUpdated = new Date('2026-01-28T09:00:00Z'); // 3 hours ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      const banner = screen.getByRole('alert');
      expect(banner).toHaveAttribute('data-stale', 'true');
    });

    it('sets data-stale="true" for extremely old data (days)', () => {
      const lastUpdated = new Date('2026-01-26T12:00:00Z'); // 2 days ago
      render(<ConnectionBanner {...defaultProps} lastUpdated={lastUpdated} />);

      const banner = screen.getByRole('alert');
      expect(banner).toHaveAttribute('data-stale', 'true');
    });
  });

  describe('disabled state when isReconnecting is true', () => {
    it('disables the retry button when isReconnecting is true', () => {
      render(<ConnectionBanner {...defaultProps} isReconnecting={true} />);

      const button = screen.getByRole('button', { name: /retry connection now/i });
      expect(button).toBeDisabled();
    });

    it('enables the retry button when isReconnecting is false', () => {
      render(<ConnectionBanner {...defaultProps} isReconnecting={false} />);

      const button = screen.getByRole('button', { name: /retry connection now/i });
      expect(button).not.toBeDisabled();
    });

    it('shows "Connecting..." text when isReconnecting is true', () => {
      render(<ConnectionBanner {...defaultProps} isReconnecting={true} />);

      expect(screen.getByRole('button')).toHaveTextContent('Connecting...');
    });

    it('shows "Retry Now" text when isReconnecting is false', () => {
      render(<ConnectionBanner {...defaultProps} isReconnecting={false} />);

      expect(screen.getByRole('button')).toHaveTextContent('Retry Now');
    });

    it('does not call onRetry when button is disabled and clicked', () => {
      const onRetry = vi.fn();
      render(<ConnectionBanner {...defaultProps} isReconnecting={true} onRetry={onRetry} />);

      const button = screen.getByRole('button', { name: /retry connection now/i });
      fireEvent.click(button);

      expect(onRetry).not.toHaveBeenCalled();
    });

    it('transitions from enabled to disabled state', () => {
      const { rerender } = render(<ConnectionBanner {...defaultProps} isReconnecting={false} />);

      const button = screen.getByRole('button', { name: /retry connection now/i });
      expect(button).not.toBeDisabled();
      expect(button).toHaveTextContent('Retry Now');

      rerender(<ConnectionBanner {...defaultProps} isReconnecting={true} />);
      expect(button).toBeDisabled();
      expect(button).toHaveTextContent('Connecting...');
    });
  });

  describe('custom className', () => {
    it('applies custom className', () => {
      render(<ConnectionBanner {...defaultProps} className="custom-class" />);

      const banner = screen.getByRole('alert');
      expect(banner).toHaveClass('custom-class');
    });

    it('preserves base styles when custom className is applied', () => {
      const { container } = render(<ConnectionBanner {...defaultProps} className="custom-class" />);

      const banner = container.firstChild as HTMLElement;
      // Should have both the module CSS class and custom class
      expect(banner.classList.length).toBeGreaterThan(1);
    });
  });

  describe('aria attributes', () => {
    it('has aria-live="polite" for accessibility', () => {
      render(<ConnectionBanner {...defaultProps} />);

      const banner = screen.getByRole('alert');
      expect(banner).toHaveAttribute('aria-live', 'polite');
    });

    it('has aria-hidden="true" on the icon', () => {
      const { container } = render(<ConnectionBanner {...defaultProps} />);

      const icon = container.querySelector('[aria-hidden="true"]');
      expect(icon).toBeInTheDocument();
      expect(icon).toHaveTextContent('⚠️');
    });

    it('has aria-label on the retry button', () => {
      render(<ConnectionBanner {...defaultProps} />);

      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('aria-label', 'Retry connection now');
    });

    it('has aria-live="off" on countdown to prevent excessive announcements', () => {
      const { container } = render(<ConnectionBanner {...defaultProps} retryCountdown={10} />);

      const countdown = container.querySelector('[aria-live="off"]');
      expect(countdown).toBeInTheDocument();
      expect(countdown).toHaveTextContent('Retrying in 10s');
    });
  });

  describe('edge cases', () => {
    it('handles all props being set simultaneously', () => {
      const onRetry = vi.fn();
      const lastUpdated = new Date('2026-01-28T11:50:00Z'); // 10 minutes ago (stale)

      render(
        <ConnectionBanner
          lastUpdated={lastUpdated}
          retryCountdown={15}
          isReconnecting={false}
          onRetry={onRetry}
          className="test-class"
        />
      );

      const banner = screen.getByRole('alert');
      expect(banner).toHaveAttribute('data-stale', 'true');
      expect(banner).toHaveClass('test-class');
      expect(screen.getByText('Disconnected from loom server')).toBeInTheDocument();
      expect(screen.getByText('Last updated 10 minutes ago')).toBeInTheDocument();
      expect(screen.getByText('Retrying in 15s')).toBeInTheDocument();

      const button = screen.getByRole('button');
      expect(button).not.toBeDisabled();
      fireEvent.click(button);
      expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it('handles countdown and isReconnecting together', () => {
      render(
        <ConnectionBanner
          {...defaultProps}
          retryCountdown={5}
          isReconnecting={true}
        />
      );

      expect(screen.getByText('Retrying in 5s')).toBeInTheDocument();
      expect(screen.getByRole('button')).toBeDisabled();
      expect(screen.getByRole('button')).toHaveTextContent('Connecting...');
    });
  });
});
