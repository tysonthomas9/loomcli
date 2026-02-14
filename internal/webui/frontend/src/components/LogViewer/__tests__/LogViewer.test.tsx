/**
 * @vitest-environment jsdom
 */
import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { LogChunk } from '@/hooks/useLogStream';

import { LogViewer } from '../LogViewer';

let lastTerminalInstance: {
  write: ReturnType<typeof vi.fn>;
  clear: ReturnType<typeof vi.fn>;
  reset: ReturnType<typeof vi.fn>;
  open: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
  loadAddon: ReturnType<typeof vi.fn>;
  scrollToBottom: ReturnType<typeof vi.fn>;
  onScroll: ReturnType<typeof vi.fn>;
  buffer: { active: { viewportY: number; baseY: number } };
} | null = null;

let lastFitAddonInstance: { fit: ReturnType<typeof vi.fn> } | null = null;

vi.mock('@xterm/xterm', () => {
  class MockTerminal {
    write = vi.fn();
    clear = vi.fn();
    reset = vi.fn();
    open = vi.fn();
    dispose = vi.fn();
    loadAddon = vi.fn();
    scrollToBottom = vi.fn();
    onScroll = vi.fn(() => ({ dispose: vi.fn() }));
    buffer = { active: { viewportY: 0, baseY: 0 } };

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
    expect(lastTerminalInstance?.write).toHaveBeenNthCalledWith(2, new TextEncoder().encode('\rdef'));
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
});
