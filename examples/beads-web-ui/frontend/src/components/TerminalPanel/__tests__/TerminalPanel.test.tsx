/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalPanel component.
 */

import { render, screen, fireEvent, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import { TerminalPanel } from '../TerminalPanel';

// Mock xterm and addons before importing the component.
// Use class syntax so `new Terminal(...)` works correctly.
vi.mock('@xterm/xterm', () => {
  class MockTerminal {
    open = vi.fn();
    dispose = vi.fn();
    onData = vi.fn(() => ({ dispose: vi.fn() }));
    write = vi.fn();
    loadAddon = vi.fn();
    cols = 80;
    rows = 24;
  }
  return { Terminal: MockTerminal };
});

vi.mock('@xterm/addon-fit', () => {
  class MockFitAddon {
    fit = vi.fn();
    dispose = vi.fn();
  }
  return { FitAddon: MockFitAddon };
});

vi.mock('@xterm/addon-web-links', () => {
  class MockWebLinksAddon {
    dispose = vi.fn();
  }
  return { WebLinksAddon: MockWebLinksAddon };
});

vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  binaryType = '';
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  send = vi.fn();
  close = vi.fn();

  constructor() {
    // Simulate connection asynchronously
    setTimeout(() => {
      this.readyState = MockWebSocket.OPEN;
      this.onopen?.();
    }, 0);
  }
}

// Mock ResizeObserver (not available in jsdom)
class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}

// Replace globals with mocks
const OriginalWebSocket = globalThis.WebSocket;
const OriginalResizeObserver = globalThis.ResizeObserver;

// Type-safe global mock helpers
type GlobalWithMocks = typeof globalThis & {
  WebSocket: typeof MockWebSocket | typeof WebSocket;
  ResizeObserver: typeof MockResizeObserver | typeof ResizeObserver;
};

beforeEach(() => {
  (globalThis as GlobalWithMocks).WebSocket = MockWebSocket;
  (globalThis as GlobalWithMocks).ResizeObserver = MockResizeObserver;
});
afterEach(() => {
  (globalThis as GlobalWithMocks).WebSocket = OriginalWebSocket;
  (globalThis as GlobalWithMocks).ResizeObserver = OriginalResizeObserver;
});

describe('TerminalPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.style.overflow = '';
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe('visibility', () => {
    it('renders overlay with open class when isOpen=true', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      const overlay = screen.getByTestId('terminal-panel-overlay');
      expect(overlay.className).toMatch(/open/);
    });

    it('hidden when isOpen=false (overlay lacks open class, aria-hidden=true)', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={false} onClose={onClose} />);

      const overlay = screen.getByTestId('terminal-panel-overlay');
      expect(overlay.className).not.toMatch(/open/);
      expect(overlay).toHaveAttribute('aria-hidden', 'true');
    });
  });

  describe('closing behavior', () => {
    it('calls onClose on backdrop click (click on overlay div itself)', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      const overlay = screen.getByTestId('terminal-panel-overlay');
      // Click directly on the overlay (target === currentTarget)
      fireEvent.click(overlay);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('does NOT close on Escape key (Escape is needed for terminal apps)', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      act(() => {
        fireEvent.keyDown(document, { key: 'Escape' });
      });

      expect(onClose).not.toHaveBeenCalled();
    });

    it('does NOT call onClose on panel click (stopPropagation via target check)', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      const panel = screen.getByRole('dialog');
      fireEvent.click(panel);

      expect(onClose).not.toHaveBeenCalled();
    });

    it('calls onClose when close button clicked', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      const closeButton = screen.getByTestId('terminal-close-button');
      fireEvent.click(closeButton);

      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  describe('header', () => {
    it('shows header with title "Terminal" and close button', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      expect(screen.getByText('Terminal')).toBeInTheDocument();
      expect(screen.getByTestId('terminal-close-button')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Close terminal' })).toBeInTheDocument();
    });
  });

  describe('body scroll lock', () => {
    it('locks body scroll when open (overflow hidden)', () => {
      const onClose = vi.fn();
      render(<TerminalPanel isOpen={true} onClose={onClose} />);

      expect(document.body.style.overflow).toBe('hidden');
    });

    it('restores body scroll on close', () => {
      const onClose = vi.fn();
      const { rerender } = render(<TerminalPanel isOpen={true} onClose={onClose} />);

      expect(document.body.style.overflow).toBe('hidden');

      rerender(<TerminalPanel isOpen={false} onClose={onClose} />);

      expect(document.body.style.overflow).not.toBe('hidden');
    });
  });
});
