/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ConnectionStatus component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import type { ConnectionState } from '@/api/sse';

import { ConnectionStatus } from '../ConnectionStatus';

describe('ConnectionStatus', () => {
  describe('rendering', () => {
    it('renders without crashing', () => {
      render(<ConnectionStatus state="connected" />);
      expect(screen.getByRole('status')).toBeInTheDocument();
    });

    it('renders as div with role="status"', () => {
      render(<ConnectionStatus state="connected" />);
      const status = screen.getByRole('status');
      expect(status.tagName).toBe('DIV');
    });

    it('renders indicator element', () => {
      const { container } = render(<ConnectionStatus state="connected" />);
      const indicator = container.querySelector('[aria-hidden="true"]');
      expect(indicator).toBeInTheDocument();
    });
  });

  describe('state display', () => {
    it('shows correct text for "connected" state', () => {
      render(<ConnectionStatus state="connected" />);
      expect(screen.getByText('Connected')).toBeInTheDocument();
    });

    it('shows correct text for "connecting" state', () => {
      render(<ConnectionStatus state="connecting" />);
      expect(screen.getByText('Connecting...')).toBeInTheDocument();
    });

    it('shows correct text for "reconnecting" state', () => {
      render(<ConnectionStatus state="reconnecting" />);
      expect(screen.getByText('Reconnecting...')).toBeInTheDocument();
    });

    it('shows correct text for "disconnected" state', () => {
      render(<ConnectionStatus state="disconnected" />);
      expect(screen.getByText('Disconnected')).toBeInTheDocument();
    });
  });

  describe('data attributes', () => {
    it.each<ConnectionState>(['connected', 'connecting', 'reconnecting', 'disconnected'])(
      'data-state attribute reflects "%s" state',
      (state) => {
        const { container } = render(<ConnectionStatus state={state} />);
        const root = container.firstChild as HTMLElement;
        expect(root).toHaveAttribute('data-state', state);
      }
    );

    it('data-variant attribute reflects "inline" variant (default)', () => {
      const { container } = render(<ConnectionStatus state="connected" />);
      const root = container.firstChild as HTMLElement;
      expect(root).toHaveAttribute('data-variant', 'inline');
    });

    it('data-variant attribute reflects "badge" variant', () => {
      const { container } = render(<ConnectionStatus state="connected" variant="badge" />);
      const root = container.firstChild as HTMLElement;
      expect(root).toHaveAttribute('data-variant', 'badge');
    });
  });

  describe('props', () => {
    it('applies className prop to root element', () => {
      const { container } = render(<ConnectionStatus state="connected" className="custom-class" />);
      const root = container.firstChild as HTMLElement;
      expect(root).toHaveClass('custom-class');
    });

    it('variant="badge" applies badge styles', () => {
      const { container } = render(<ConnectionStatus state="connected" variant="badge" />);
      const root = container.firstChild as HTMLElement;
      expect(root.className).toContain('badge');
    });

    it('variant="inline" applies inline styles (default)', () => {
      const { container } = render(<ConnectionStatus state="connected" />);
      const root = container.firstChild as HTMLElement;
      expect(root.className).toContain('inline');
    });

    it('showText=true (default) shows text label', () => {
      render(<ConnectionStatus state="connected" />);
      expect(screen.getByText('Connected')).toBeInTheDocument();
    });

    it('showText=false hides text label', () => {
      render(<ConnectionStatus state="connected" showText={false} />);
      expect(screen.queryByText('Connected')).not.toBeInTheDocument();
    });

    it('showText=false still renders indicator', () => {
      const { container } = render(<ConnectionStatus state="connected" showText={false} />);
      const indicator = container.querySelector('[aria-hidden="true"]');
      expect(indicator).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has role="status" for live updates', () => {
      render(<ConnectionStatus state="connected" />);
      expect(screen.getByRole('status')).toBeInTheDocument();
    });

    it('has aria-live="polite" for screen readers', () => {
      render(<ConnectionStatus state="connected" />);
      const status = screen.getByRole('status');
      expect(status).toHaveAttribute('aria-live', 'polite');
    });

    it('has aria-label with full status description', () => {
      render(<ConnectionStatus state="connected" />);
      expect(screen.getByLabelText('Connection status: Connected')).toBeInTheDocument();
    });

    it('indicator has aria-hidden="true"', () => {
      const { container } = render(<ConnectionStatus state="connected" />);
      const indicator = container.querySelector('[aria-hidden="true"]');
      expect(indicator).toBeInTheDocument();
    });

    it.each<[ConnectionState, string]>([
      ['connected', 'Connection status: Connected'],
      ['connecting', 'Connection status: Connecting...'],
      ['reconnecting', 'Connection status: Reconnecting...'],
      ['disconnected', 'Connection status: Disconnected'],
    ])('aria-label is correct for "%s" state', (state, expectedLabel) => {
      render(<ConnectionStatus state={state} />);
      expect(screen.getByLabelText(expectedLabel)).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles rapid state changes', () => {
      const { rerender } = render(<ConnectionStatus state="connected" />);
      expect(screen.getByText('Connected')).toBeInTheDocument();

      rerender(<ConnectionStatus state="reconnecting" />);
      expect(screen.getByText('Reconnecting...')).toBeInTheDocument();

      rerender(<ConnectionStatus state="connected" />);
      expect(screen.getByText('Connected')).toBeInTheDocument();
    });

    it('handles all variant/showText combinations', () => {
      const { rerender } = render(
        <ConnectionStatus state="connected" variant="inline" showText={true} />
      );
      expect(screen.getByText('Connected')).toBeInTheDocument();

      rerender(<ConnectionStatus state="connected" variant="inline" showText={false} />);
      expect(screen.queryByText('Connected')).not.toBeInTheDocument();

      rerender(<ConnectionStatus state="connected" variant="badge" showText={true} />);
      expect(screen.getByText('Connected')).toBeInTheDocument();

      rerender(<ConnectionStatus state="connected" variant="badge" showText={false} />);
      expect(screen.queryByText('Connected')).not.toBeInTheDocument();
    });
  });

  describe('reconnect counter and retry button', () => {
    it('shows attempt count when reconnecting with attempts > 0', () => {
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={3} />);
      expect(screen.getByText('Reconnecting (attempt 3)...')).toBeInTheDocument();
    });

    it('shows simple reconnecting text when attempts is 0', () => {
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={0} />);
      expect(screen.getByText('Reconnecting...')).toBeInTheDocument();
    });

    it('shows simple reconnecting text when attempts not provided', () => {
      render(<ConnectionStatus state="reconnecting" />);
      expect(screen.getByText('Reconnecting...')).toBeInTheDocument();
    });

    it('shows retry button when reconnecting with onRetry callback', () => {
      const onRetry = vi.fn();
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={2} onRetry={onRetry} />);
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument();
    });

    it('calls onRetry when retry button clicked', () => {
      const onRetry = vi.fn();
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={2} onRetry={onRetry} />);
      fireEvent.click(screen.getByRole('button', { name: /retry/i }));
      expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it('hides retry button when showRetryButton is false', () => {
      render(
        <ConnectionStatus
          state="reconnecting"
          reconnectAttempts={2}
          onRetry={() => {}}
          showRetryButton={false}
        />
      );
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('hides retry button when not in reconnecting state', () => {
      render(<ConnectionStatus state="connected" reconnectAttempts={2} onRetry={() => {}} />);
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('hides retry button when reconnectAttempts is 0', () => {
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={0} onRetry={() => {}} />);
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('hides retry button when onRetry is not provided', () => {
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={2} />);
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('retry button has accessible label', () => {
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={1} onRetry={() => {}} />);
      expect(screen.getByRole('button')).toHaveAttribute('aria-label', 'Retry connection now');
    });

    it('updates aria-label when reconnect attempts shown', () => {
      render(<ConnectionStatus state="reconnecting" reconnectAttempts={5} />);
      expect(
        screen.getByLabelText('Connection status: Reconnecting (attempt 5)...')
      ).toBeInTheDocument();
    });
  });
});
