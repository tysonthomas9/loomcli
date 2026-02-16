/**
 * @vitest-environment jsdom
 */
import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { LogChunk } from '@/hooks/logTypes';

import { LogViewer } from '../LogViewer';

let lastTerminalInstance: {
  write: ReturnType<typeof vi.fn>;
  clear: ReturnType<typeof vi.fn>;
  reset: ReturnType<typeof vi.fn>;
  open: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
  loadAddon: ReturnType<typeof vi.fn>;
  scrollToBottom: ReturnType<typeof vi.fn>;
  scrollToLine: ReturnType<typeof vi.fn>;
  onScroll: ReturnType<typeof vi.fn>;
  buffer: { active: { viewportY: number; baseY: number; length: number } };
} | null = null;

let lastFitAddonInstance: { fit: ReturnType<typeof vi.fn> } | null = null;

vi.mock('@xterm/xterm', () => {
  class MockTerminal {
    options: Record<string, unknown> = { disableStdin: true };
    buffer = { active: { viewportY: 0, baseY: 100, length: 0 } };
    write = vi.fn((data: Uint8Array | string, callback?: () => void) => {
      const text = typeof data === 'string' ? data : new TextDecoder().decode(data);
      const lineCount = text.split('\n').length - 1;
      this.buffer.active.length += Math.max(lineCount, 0);
      if (callback) callback();
    });
    clear = vi.fn();
    reset = vi.fn(() => {
      this.buffer.active.length = 0;
    });
    open = vi.fn();
    dispose = vi.fn();
    loadAddon = vi.fn();
    scrollToBottom = vi.fn();
    scrollToLine = vi.fn();
    onScroll = vi.fn(() => ({ dispose: vi.fn() }));
    onData = vi.fn(() => ({ dispose: vi.fn() }));

    constructor() {
      lastTerminalInstance = this as unknown as typeof lastTerminalInstance;
    }
  }
  return { Terminal: MockTerminal };
});

vi.mock('@xterm/addon-fit', () => {
  class MockFitAddon {
    fit = vi.fn();

    constructor() {
      lastFitAddonInstance = this as unknown as typeof lastFitAddonInstance;
    }
  }
  return { FitAddon: MockFitAddon };
});

vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

class MockResizeObserver {
  observe = vi.fn();
  disconnect = vi.fn();
}

const OriginalResizeObserver = globalThis.ResizeObserver;

function createChunk(text: string, offset: number): LogChunk {
  return {
    chunk: new TextEncoder().encode(text),
    byteOffset: offset,
    timestamp: '2026-02-14T00:00:00Z',
  };
}

beforeEach(() => {
  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver;
  lastTerminalInstance = null;
  lastFitAddonInstance = null;
});

afterEach(() => {
  globalThis.ResizeObserver = OriginalResizeObserver;
});

describe('LogViewer', () => {
  it('renders connection status', () => {
    render(<LogViewer chunks={[]} connectionState="connected" />);
    expect(screen.getByText('Connected')).toBeInTheDocument();
  });

  it('writes new chunks incrementally', () => {
    const first = [createChunk('abc', 3)];
    const { rerender } = render(<LogViewer chunks={first} connectionState="connected" />);

    expect(lastTerminalInstance?.write).toHaveBeenCalledTimes(1);
    const second = [...first, createChunk('\rdef', 7)];
    rerender(<LogViewer chunks={second} connectionState="connected" />);
    expect(lastTerminalInstance?.write).toHaveBeenCalledTimes(2);
    expect(lastTerminalInstance?.write).toHaveBeenNthCalledWith(2, new TextEncoder().encode('\rdef'), expect.any(Function));
  });

  it('resets terminal when resetVersion changes', () => {
    const { rerender } = render(
      <LogViewer chunks={[createChunk('abc', 3)]} connectionState="connected" resetVersion={0} />
    );
    expect(lastTerminalInstance?.write).toHaveBeenCalledTimes(1);

    rerender(<LogViewer chunks={[]} connectionState="connected" resetVersion={1} />);
    expect(lastTerminalInstance?.reset).toHaveBeenCalledTimes(1);
  });

  it('loads fit addon and fits terminal', () => {
    render(<LogViewer chunks={[]} connectionState="connected" />);
    expect(lastTerminalInstance?.loadAddon).toHaveBeenCalledWith(lastFitAddonInstance);
    expect(lastFitAddonInstance?.fit).toHaveBeenCalled();
  });

  it('keeps loading banner mounted and only toggles visibility', () => {
    const { rerender } = render(
      <LogViewer chunks={[]} connectionState="connected" isLoadingMore={false} />
    );

    const banner = screen.getByTestId('loading-banner');
    expect(banner).toHaveAttribute('data-visible', 'false');

    rerender(<LogViewer chunks={[]} connectionState="connected" isLoadingMore />);
    expect(screen.getByTestId('loading-banner')).toBe(banner);
    expect(banner).toHaveAttribute('data-visible', 'true');
  });

  it('restores viewport anchor after prepend when auto-scroll is disabled', () => {
    const { rerender } = render(
      <LogViewer
        chunks={[createChunk('line-1\nline-2\n', 12)]}
        connectionState="connected"
        autoScroll={false}
        resetVersion={0}
      />
    );

    expect(lastTerminalInstance?.buffer.active.length).toBe(2);
    if (lastTerminalInstance) {
      lastTerminalInstance.buffer.active.viewportY = 4;
    }

    rerender(
      <LogViewer
        chunks={[createChunk('old-1\nold-2\nline-1\nline-2\n', 24)]}
        connectionState="connected"
        autoScroll={false}
        resetVersion={1}
      />
    );

    expect(lastTerminalInstance?.scrollToLine).toHaveBeenCalledWith(6);
    expect(lastTerminalInstance?.scrollToBottom).not.toHaveBeenCalled();
  });
});
