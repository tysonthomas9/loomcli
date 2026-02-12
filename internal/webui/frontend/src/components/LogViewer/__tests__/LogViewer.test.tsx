/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for LogViewer component (xterm.js-based).
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import type { LogLine } from '@/hooks/useLogStream';

import { LogViewer } from '../LogViewer';

// Track terminal instances for test assertions
let lastTerminalInstance: MockTerminalInstance | null = null;
let lastFitAddonInstance: MockFitAddonInstance | null = null;

interface MockTerminalInstance {
  open: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
  write: ReturnType<typeof vi.fn>;
  clear: ReturnType<typeof vi.fn>;
  loadAddon: ReturnType<typeof vi.fn>;
  scrollToBottom: ReturnType<typeof vi.fn>;
  onScroll: ReturnType<typeof vi.fn>;
  buffer: { active: { viewportY: number; baseY: number } };
  options: Record<string, unknown>;
}

interface MockFitAddonInstance {
  fit: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
}

vi.mock('@xterm/xterm', () => {
  class MockTerminal {
    open = vi.fn();
    dispose = vi.fn();
    write = vi.fn();
    clear = vi.fn();
    loadAddon = vi.fn();
    scrollToBottom = vi.fn();
    onScroll = vi.fn(() => ({ dispose: vi.fn() }));
    buffer = { active: { viewportY: 0, baseY: 0 } };
    options: Record<string, unknown> = {};

    constructor(opts?: Record<string, unknown>) {
      this.options = opts ?? {};
      lastTerminalInstance = this as unknown as MockTerminalInstance;
    }
  }
  return { Terminal: MockTerminal };
});

vi.mock('@xterm/addon-fit', () => {
  class MockFitAddon {
    fit = vi.fn();
    dispose = vi.fn();

    constructor() {
      lastFitAddonInstance = this as unknown as MockFitAddonInstance;
    }
  }
  return { FitAddon: MockFitAddon };
});

vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

// Mock ResizeObserver (not available in jsdom)
class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}

const OriginalResizeObserver = globalThis.ResizeObserver;

type GlobalWithMocks = typeof globalThis & {
  ResizeObserver: typeof MockResizeObserver | typeof ResizeObserver;
};

beforeEach(() => {
  (globalThis as GlobalWithMocks).ResizeObserver = MockResizeObserver;
  lastTerminalInstance = null;
  lastFitAddonInstance = null;
});

afterEach(() => {
  (globalThis as GlobalWithMocks).ResizeObserver = OriginalResizeObserver;
});

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
  describe('Terminal creation', () => {
    it('creates Terminal with correct config', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(lastTerminalInstance).not.toBeNull();
      expect(lastTerminalInstance!.options).toMatchObject({
        disableStdin: true,
        fontSize: 14,
        fontFamily: 'Menlo, Monaco, "Courier New", monospace',
        scrollback: 5000,
        convertEol: true,
        cursorBlink: false,
        cursorStyle: 'bar',
        cursorWidth: 0,
        theme: {
          background: '#1e1e1e',
          foreground: '#d4d4d4',
        },
      });
    });

    it('loads FitAddon and calls fit', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(lastTerminalInstance!.loadAddon).toHaveBeenCalledWith(lastFitAddonInstance);
      expect(lastFitAddonInstance!.fit).toHaveBeenCalled();
    });

    it('opens terminal in container', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(lastTerminalInstance!.open).toHaveBeenCalledTimes(1);
      const arg = lastTerminalInstance!.open.mock.calls[0][0];
      expect(arg).toBeInstanceOf(HTMLDivElement);
    });

    it('disposes terminal on unmount', () => {
      const { unmount } = render(<LogViewer lines={[]} connectionState="connected" />);

      const terminal = lastTerminalInstance!;
      unmount();

      expect(terminal.dispose).toHaveBeenCalledTimes(1);
    });
  });

  describe('Line writing', () => {
    it('writes lines to terminal', () => {
      const lines = [
        createLogLine({ line: 'First message', lineNumber: 1 }),
        createLogLine({ line: 'Second message', lineNumber: 2 }),
      ];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('First message\n');
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Second message\n');
    });

    it('writes lines incrementally on update', () => {
      const lines1 = createLogLines(3);
      const { rerender } = render(<LogViewer lines={lines1} connectionState="connected" />);

      // Should have written 3 lines
      expect(lastTerminalInstance!.write).toHaveBeenCalledTimes(3);

      const lines2 = [...lines1, ...createLogLines(2, 4)];
      rerender(<LogViewer lines={lines2} connectionState="connected" />);

      // Should have written only 2 new lines (5 total)
      expect(lastTerminalInstance!.write).toHaveBeenCalledTimes(5);
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Log line 4\n');
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Log line 5\n');
    });

    it('clears terminal and rewrites when lines reset (stream change)', () => {
      const lines1 = createLogLines(5);
      const { rerender } = render(<LogViewer lines={lines1} connectionState="connected" />);

      expect(lastTerminalInstance!.write).toHaveBeenCalledTimes(5);

      // Simulate stream reset (new agent/task)
      const lines2 = createLogLines(2, 100);
      rerender(<LogViewer lines={lines2} connectionState="connected" />);

      expect(lastTerminalInstance!.clear).toHaveBeenCalled();
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Log line 100\n');
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Log line 101\n');
    });

    it('handles empty lines array', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(lastTerminalInstance!.write).not.toHaveBeenCalled();
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

    it('calls terminal.scrollToBottom when scroll button is clicked', () => {
      render(
        <LogViewer lines={createLogLines(10)} connectionState="connected" autoScroll={false} />
      );

      const scrollButton = screen.getByRole('button', { name: /scroll to bottom/i });
      fireEvent.click(scrollButton);

      expect(lastTerminalInstance!.scrollToBottom).toHaveBeenCalled();
    });

    it('does not throw when clicking scroll button without onAutoScrollChange', () => {
      render(
        <LogViewer lines={createLogLines(10)} connectionState="connected" autoScroll={false} />
      );

      const scrollButton = screen.getByRole('button', { name: /scroll to bottom/i });
      expect(() => fireEvent.click(scrollButton)).not.toThrow();
    });

    it('scrolls to bottom on new lines when auto-scroll is enabled', () => {
      render(<LogViewer lines={createLogLines(5)} connectionState="connected" autoScroll={true} />);

      expect(lastTerminalInstance!.scrollToBottom).toHaveBeenCalled();
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
    it('has data-testid on log viewer container', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(screen.getByTestId('log-viewer')).toBeInTheDocument();
    });

    it('has data-testid on terminal container', () => {
      render(<LogViewer lines={[]} connectionState="connected" />);

      expect(screen.getByTestId('terminal-container')).toBeInTheDocument();
    });

    it('status dot has aria-hidden for screen readers', () => {
      const { container } = render(<LogViewer lines={[]} connectionState="connected" />);

      const statusDot = container.querySelector('[data-state]');
      expect(statusDot).toHaveAttribute('aria-hidden', 'true');
    });

    it('scroll button has proper type attribute', () => {
      render(
        <LogViewer lines={createLogLines(10)} connectionState="connected" autoScroll={false} />
      );

      const button = screen.getByRole('button', { name: /scroll to bottom/i });
      expect(button).toHaveAttribute('type', 'button');
    });
  });

  describe('Edge cases', () => {
    it('handles rapid prop updates', () => {
      const { rerender } = render(
        <LogViewer lines={createLogLines(5)} connectionState="connecting" />
      );

      rerender(<LogViewer lines={createLogLines(10)} connectionState="connected" />);
      rerender(<LogViewer lines={createLogLines(15)} connectionState="connected" />);
      rerender(<LogViewer lines={createLogLines(20)} connectionState="connected" />);

      // Should have written all 20 lines total
      expect(lastTerminalInstance!.write).toHaveBeenCalledTimes(20);
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Log line 20\n');
    });

    it('handles transition from lines to empty', () => {
      const { rerender } = render(
        <LogViewer lines={createLogLines(5)} connectionState="connected" />
      );

      expect(lastTerminalInstance!.write).toHaveBeenCalledTimes(5);

      rerender(<LogViewer lines={[]} connectionState="connected" />);

      expect(lastTerminalInstance!.clear).toHaveBeenCalled();
    });

    it('passes raw content to terminal (xterm handles escaping)', () => {
      const lines = [
        createLogLine({ line: '<script>alert("xss")</script>', lineNumber: 1 }),
        createLogLine({ line: 'Line with & ampersand', lineNumber: 2 }),
      ];

      render(<LogViewer lines={lines} connectionState="connected" />);

      expect(lastTerminalInstance!.write).toHaveBeenCalledWith(
        '<script>alert("xss")</script>\n'
      );
      expect(lastTerminalInstance!.write).toHaveBeenCalledWith('Line with & ampersand\n');
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
