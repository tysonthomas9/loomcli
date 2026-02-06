/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for LogViewer component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import type { LogLine } from '@/hooks/useLogStream';

import { LogViewer } from '../LogViewer';

/**
 * Create a test log line with required fields.
 */
function createLogLine(overrides: Partial<LogLine> = {}): LogLine {
  return {
    line: 'Test log message',
    lineNumber: 1,
    timestamp: '2026-02-03T10:00:00Z',
    ...overrides,
  };
}

/**
 * Create an array of sequential log lines.
 */
function createLogLines(count: number, startNumber = 1): LogLine[] {
  return Array.from({ length: count }, (_, i) => ({
    line: `Log line ${startNumber + i}`,
    lineNumber: startNumber + i,
    timestamp: `2026-02-03T10:00:${String(i).padStart(2, '0')}Z`,
  }));
}

describe('LogViewer', () => {
  describe('Empty state', () => {
    it('renders empty state message when no lines', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(
        screen.getByText(/No logs available yet. Logs appear when the agent starts working./i)
      ).toBeInTheDocument();
    });

    it('renders empty state with proper container', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      const viewer = screen.getByTestId('log-viewer');
      expect(viewer).toBeInTheDocument();
    });
  });

  describe('Line rendering', () => {
    it('renders log lines', () => {
      const lines = [
        createLogLine({ line: 'First message', lineNumber: 1 }),
        createLogLine({ line: 'Second message', lineNumber: 2 }),
      ];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(screen.getByText('First message')).toBeInTheDocument();
      expect(screen.getByText('Second message')).toBeInTheDocument();
    });

    it('renders line numbers by default', () => {
      const lines = [
        createLogLine({ lineNumber: 1 }),
        createLogLine({ lineNumber: 2 }),
        createLogLine({ lineNumber: 3 }),
      ];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(screen.getByText('1')).toBeInTheDocument();
      expect(screen.getByText('2')).toBeInTheDocument();
      expect(screen.getByText('3')).toBeInTheDocument();
    });

    it('hides line numbers when showLineNumbers is false', () => {
      const lines = [createLogLine({ lineNumber: 42, line: 'My log message' })];

      render(<LogViewer lines={lines} connectionState="connected" showLineNumbers={false} />);

      expect(screen.queryByText('42')).not.toBeInTheDocument();
      expect(screen.getByText('My log message')).toBeInTheDocument();
    });

    it('renders many lines correctly', () => {
      const lines = createLogLines(100);

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(screen.getByText('Log line 1')).toBeInTheDocument();
      expect(screen.getByText('Log line 100')).toBeInTheDocument();
    });

    it('preserves non-sequential line numbers', () => {
      const lines = [
        createLogLine({ lineNumber: 50, line: 'Line at 50' }),
        createLogLine({ lineNumber: 51, line: 'Line at 51' }),
        createLogLine({ lineNumber: 52, line: 'Line at 52' }),
      ];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(screen.getByText('50')).toBeInTheDocument();
      expect(screen.getByText('51')).toBeInTheDocument();
      expect(screen.getByText('52')).toBeInTheDocument();
    });
  });

  describe('Connection status indicator', () => {
    it('shows Connected status when connected', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(screen.getByText('Connected')).toBeInTheDocument();
    });

    it('shows Connecting status when connecting', () => {
      render(<LogViewer lines={[]} connectionState="connecting" />);

      expect(screen.getByText('Connecting...')).toBeInTheDocument();
    });

    it('shows Reconnecting status when reconnecting', () => {
      render(<LogViewer lines={[]} connectionState="reconnecting" />);

      expect(screen.getByText('Reconnecting...')).toBeInTheDocument();
    });

    it('shows Disconnected status when disconnected', () => {
      render(<LogViewer lines={[]} connectionState="disconnected" />);

      expect(screen.getByText('Disconnected')).toBeInTheDocument();
    });

    it('renders status dot with correct data-state attribute', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="connected" />);

      const statusDot = container.querySelector('[data-state="connected"]');
      expect(statusDot).toBeInTheDocument();
    });

    it('marks dot as pulsing during connecting', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="connecting" />);

      const statusDot = container.querySelector('[data-pulsing="true"]');
      expect(statusDot).toBeInTheDocument();
    });

    it('marks dot as pulsing during reconnecting', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="reconnecting" />);

      const statusDot = container.querySelector('[data-pulsing="true"]');
      expect(statusDot).toBeInTheDocument();
    });

    it('does not pulse when connected', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="connected" />);

      const statusDot = container.querySelector('[data-pulsing="false"]');
      expect(statusDot).toBeInTheDocument();
    });

    it('does not pulse when disconnected', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="disconnected" />);

      const statusDot = container.querySelector('[data-pulsing="false"]');
      expect(statusDot).toBeInTheDocument();
    });
  });

  describe('Error display', () => {
    it('shows error banner when error is provided', () => {
      render(<LogViewer lines={[]} connectionState="disconnected" error="Connection failed" />);

      expect(screen.getByRole('alert')).toBeInTheDocument();
      expect(screen.getByText('Connection failed')).toBeInTheDocument();
    });

    it('does not show error banner when error is null', () => {
      render(<LogViewer lines={[]} connectionState="connected" error={null} />);

      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('does not show error banner when error is undefined', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
  });

  describe('Auto-scroll toggle', () => {
    it('does not show scroll button when auto-scroll is enabled', () => {
      render(<LogViewer lines={[]} connectionState="connected" autoScroll={true} />);

      expect(screen.queryByRole('button', { name: /scroll to bottom/i })).not.toBeInTheDocument();
    });

    it('shows scroll button when auto-scroll is disabled', () => {
      render(
        <LogViewer lines={createLogLines(10)} connectionState="connected" autoScroll={false} />
      );

      expect(screen.getByRole('button', { name: /scroll to bottom/i })).toBeInTheDocument();
    });

    it('calls onAutoScrollChange when scroll button is clicked', () => {
      const onAutoScrollChange = vi.fn();

      render(
        <LogViewer
          lines={createLogLines(10)}
          connectionState="connected"
          autoScroll={false}
          onAutoScrollChange={onAutoScrollChange}
        />
      );

      const scrollButton = screen.getByRole('button', { name: /scroll to bottom/i });
      fireEvent.click(scrollButton);

      expect(onAutoScrollChange).toHaveBeenCalledWith(true);
    });

    it('does not throw when clicking scroll button without onAutoScrollChange', () => {
      render(
        <LogViewer lines={createLogLines(10)} connectionState="connected" autoScroll={false} />
      );

      const scrollButton = screen.getByRole('button', { name: /scroll to bottom/i });
      expect(() => fireEvent.click(scrollButton)).not.toThrow();
    });
  });

  describe('Custom className', () => {
    it('applies custom className to root element', () => {
      render(<LogViewer lines={[]} connectionState="connected" className="custom-log-viewer" />);

      const viewer = screen.getByTestId('log-viewer');
      expect(viewer).toHaveClass('custom-log-viewer');
    });

    it('preserves base styles when custom className is applied', () => {
      const { container } = render(
        <LogViewer lines={[]} connectionState="connected" className="custom-class" />
      );

      const viewer = container.firstChild as HTMLElement;
      // Should have both the module CSS class and custom class
      expect(viewer.classList.length).toBeGreaterThan(1);
    });
  });

  describe('Height customization', () => {
    it('applies custom height style', () => {
      render(<LogViewer lines={[]} connectionState="connected" height="400px" />);

      const viewer = screen.getByTestId('log-viewer');
      expect(viewer).toHaveStyle({ height: '400px' });
    });

    it('defaults to 100% height', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      const viewer = screen.getByTestId('log-viewer');
      expect(viewer).toHaveStyle({ height: '100%' });
    });

    it('accepts percentage height values', () => {
      render(<LogViewer lines={[]} connectionState="connected" height="50%" />);

      const viewer = screen.getByTestId('log-viewer');
      expect(viewer).toHaveStyle({ height: '50%' });
    });
  });

  describe('Accessibility', () => {
    it('has proper log role on scroll container', () => {
      render(<LogViewer lines={createLogLines(5)} connectionState="connected" />);

      expect(screen.getByRole('log')).toBeInTheDocument();
    });

    it('has aria-live attribute for live updates', () => {
      render(<LogViewer lines={createLogLines(5)} connectionState="connected" />);

      const logContainer = screen.getByRole('log');
      expect(logContainer).toHaveAttribute('aria-live', 'polite');
    });

    it('has aria-label on log container', () => {
      render(<LogViewer lines={createLogLines(5)} connectionState="connected" />);

      const logContainer = screen.getByRole('log');
      expect(logContainer).toHaveAttribute('aria-label', 'Log output');
    });

    it('scroll container is keyboard focusable', () => {
      render(<LogViewer lines={createLogLines(5)} connectionState="connected" />);

      const logContainer = screen.getByRole('log');
      expect(logContainer).toHaveAttribute('tabIndex', '0');
    });

    it('status dot has aria-hidden for screen readers', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="connected" />);

      const statusDot = container.querySelector('[data-state]');
      expect(statusDot).toHaveAttribute('aria-hidden', 'true');
    });

    it('has data-testid for e2e tests', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(screen.getByTestId('log-viewer')).toBeInTheDocument();
    });

    it('scroll button has proper type attribute', () => {
      render(
        <LogViewer lines={createLogLines(10)} connectionState="connected" autoScroll={false} />
      );

      const button = screen.getByRole('button', { name: /scroll to bottom/i });
      expect(button).toHaveAttribute('type', 'button');
    });
  });

  describe('Scroll behavior', () => {
    it('scroll container exists and is scrollable', () => {
      render(<LogViewer lines={createLogLines(100)} connectionState="connected" />);

      const logContainer = screen.getByRole('log');
      expect(logContainer).toBeInTheDocument();
    });

    it('disables auto-scroll when user scrolls up', () => {
      const onAutoScrollChange = vi.fn();

      render(
        <LogViewer
          lines={createLogLines(100)}
          connectionState="connected"
          autoScroll={true}
          onAutoScrollChange={onAutoScrollChange}
        />
      );

      const logContainer = screen.getByRole('log');

      // Simulate scrolling up by changing scrollTop
      Object.defineProperty(logContainer, 'scrollTop', {
        writable: true,
        value: 100,
      });
      Object.defineProperty(logContainer, 'scrollHeight', {
        writable: true,
        value: 1000,
      });
      Object.defineProperty(logContainer, 'clientHeight', {
        writable: true,
        value: 200,
      });

      // First scroll to set lastScrollTop
      fireEvent.scroll(logContainer);

      // Now scroll up (decrease scrollTop)
      Object.defineProperty(logContainer, 'scrollTop', {
        writable: true,
        value: 50,
      });

      fireEvent.scroll(logContainer);

      expect(onAutoScrollChange).toHaveBeenCalledWith(false);
    });
  });

  describe('Edge cases', () => {
    it('handles empty line content', () => {
      const lines = [createLogLine({ line: '', lineNumber: 1 })];

      render(<LogViewer lines={lines} connectionState="connected" />);

      // Should still render the line number
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    it('handles lines with special characters', () => {
      const lines = [
        createLogLine({ line: '<script>alert("xss")</script>', lineNumber: 1 }),
        createLogLine({ line: 'Line with "quotes" and \'apostrophes\'', lineNumber: 2 }),
        createLogLine({ line: 'Line with & ampersand', lineNumber: 3 }),
      ];

      render(<LogViewer lines={lines} connectionState="connected" />);

      // Content should be escaped/rendered as text
      expect(screen.getByText('<script>alert("xss")</script>')).toBeInTheDocument();
      expect(screen.getByText('Line with "quotes" and \'apostrophes\'')).toBeInTheDocument();
      expect(screen.getByText('Line with & ampersand')).toBeInTheDocument();
    });

    it('handles very long lines', () => {
      const longLine = 'A'.repeat(10000);
      const lines = [createLogLine({ line: longLine, lineNumber: 1 })];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(screen.getByText(longLine)).toBeInTheDocument();
    });

    it('handles unicode characters', () => {
      const lines = [createLogLine({ line: 'Hello World', lineNumber: 1 })];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(screen.getByText('Hello World')).toBeInTheDocument();
    });

    it('handles rapid prop updates', () => {
      const { rerender } = render(
        <LogViewer lines={createLogLines(5)} connectionState="connecting" />
      );

      // Rapid rerenders
      rerender(<LogViewer lines={createLogLines(10)} connectionState="connected" />);
      rerender(<LogViewer lines={createLogLines(15)} connectionState="connected" />);
      rerender(<LogViewer lines={createLogLines(20)} connectionState="connected" />);

      expect(screen.getByText('Log line 20')).toBeInTheDocument();
    });

    it('handles transition from lines to empty', () => {
      const { rerender } = render(
        <LogViewer lines={createLogLines(5)} connectionState="connected" />
      );

      expect(screen.getByText('Log line 1')).toBeInTheDocument();

      rerender(<LogViewer lines={[]} connectionState="connected" />);

      expect(screen.getByText(/No logs available yet/i)).toBeInTheDocument();
    });
  });

  describe('Prop combinations', () => {
    it('renders correctly with all optional props', () => {
      const onAutoScrollChange = vi.fn();

      render(
        <LogViewer
          lines={createLogLines(10)}
          connectionState="connected"
          autoScroll={true}
          onAutoScrollChange={onAutoScrollChange}
          showLineNumbers={true}
          className="custom-class"
          error={null}
          height="500px"
        />
      );

      const viewer = screen.getByTestId('log-viewer');
      expect(viewer).toBeInTheDocument();
      expect(viewer).toHaveClass('custom-class');
      expect(viewer).toHaveStyle({ height: '500px' });
    });

    it('renders correctly with minimal props', () => {
      render(<LogViewer lines={[]} connectionState="disconnected" />);

      expect(screen.getByTestId('log-viewer')).toBeInTheDocument();
      expect(screen.getByText('Disconnected')).toBeInTheDocument();
    });
  });
});
