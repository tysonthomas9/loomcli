/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useViewState, parseViewFromUrl, isValidViewMode } from '../useViewState';
import { DEFAULT_VIEW } from '@/components/ViewSwitcher';
import type { ViewMode as _ViewMode } from '@/components/ViewSwitcher';

/**
 * Mock window.location for URL sync tests.
 */
function mockWindowLocation(search = ''): void {
  Object.defineProperty(window, 'location', {
    value: {
      pathname: '/app',
      search,
      href: `http://localhost:3000/app${search}`,
    },
    writable: true,
    configurable: true,
  });
}

/**
 * Mock window.history for URL sync tests.
 */
function mockWindowHistory(): { replaceState: ReturnType<typeof vi.fn> } {
  const replaceState = vi.fn();
  Object.defineProperty(window, 'history', {
    value: {
      replaceState,
      pushState: vi.fn(),
    },
    writable: true,
    configurable: true,
  });
  return { replaceState };
}

describe('useViewState', () => {
  beforeEach(() => {
    mockWindowLocation();
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('initial state', () => {
    it('returns DEFAULT_VIEW when no URL param exists', () => {
      mockWindowLocation('');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe(DEFAULT_VIEW);
      expect(view).toBe('kanban');
    });

    it('returns DEFAULT_VIEW when syncUrl is false', () => {
      mockWindowLocation('?view=table');
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      const [view] = result.current;
      expect(view).toBe(DEFAULT_VIEW);
    });
  });

  describe('URL parsing', () => {
    it('parses valid view from URL (?view=table returns "table")', () => {
      mockWindowLocation('?view=table');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('table');
    });

    it('parses kanban view from URL', () => {
      mockWindowLocation('?view=kanban');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('kanban');
    });

    it('parses graph view from URL', () => {
      mockWindowLocation('?view=graph');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('graph');
    });

    it('parses monitor view from URL', () => {
      mockWindowLocation('?view=monitor');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('monitor');
    });

    it('defaults to kanban for invalid view (?view=invalid returns "kanban")', () => {
      mockWindowLocation('?view=invalid');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('kanban');
    });

    it('defaults to kanban for empty view param (?view= returns "kanban")', () => {
      mockWindowLocation('?view=');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('kanban');
    });

    it('handles case-sensitive view values (uppercase returns default)', () => {
      mockWindowLocation('?view=TABLE');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('kanban');
    });

    it('ignores other URL params and parses view correctly', () => {
      mockWindowLocation('?priority=2&view=graph&type=bug');
      const { result } = renderHook(() => useViewState());

      const [view] = result.current;
      expect(view).toBe('graph');
    });
  });

  describe('setView', () => {
    let historyMock: { replaceState: ReturnType<typeof vi.fn> };

    beforeEach(() => {
      mockWindowLocation('');
      historyMock = mockWindowHistory();
    });

    it('updates state when setView is called', () => {
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current[1]('table');
      });

      expect(result.current[0]).toBe('table');
    });

    it('calls replaceState when view changes', () => {
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current[1]('graph');
      });

      expect(historyMock.replaceState).toHaveBeenCalledWith(null, '', '/app?view=graph');
    });

    it('removes view param from URL when setting to DEFAULT_VIEW', () => {
      mockWindowLocation('?view=table');
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current[1]('kanban');
      });

      // Last call should be to pathname only (no query string)
      const lastCall = historyMock.replaceState.mock.calls.at(-1);
      expect(lastCall?.[2]).toBe('/app');
    });

    it('does not call replaceState when syncUrl is false', () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      act(() => {
        result.current[1]('table');
      });

      // State should update
      expect(result.current[0]).toBe('table');

      // replaceState should not be called for view changes
      const calls = historyMock.replaceState.mock.calls;
      const viewCall = calls.find((call) => call[2]?.includes('view=table'));
      expect(viewCall).toBeUndefined();
    });

    it('allows changing view multiple times', () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      act(() => {
        result.current[1]('table');
      });
      expect(result.current[0]).toBe('table');

      act(() => {
        result.current[1]('graph');
      });
      expect(result.current[0]).toBe('graph');

      act(() => {
        result.current[1]('kanban');
      });
      expect(result.current[0]).toBe('kanban');
    });
  });

  describe('preserving other URL params', () => {
    let historyMock: { replaceState: ReturnType<typeof vi.fn> };

    beforeEach(() => {
      historyMock = mockWindowHistory();
    });

    it('preserves other URL params when updating view', () => {
      mockWindowLocation('?priority=2&type=bug');
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current[1]('table');
      });

      const lastCall = historyMock.replaceState.mock.calls.at(-1)?.[2] as string;
      expect(lastCall).toContain('priority=2');
      expect(lastCall).toContain('type=bug');
      expect(lastCall).toContain('view=table');
    });

    it('preserves other URL params when setting to DEFAULT_VIEW', () => {
      mockWindowLocation('?priority=2&view=table&type=bug');
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current[1]('kanban');
      });

      const lastCall = historyMock.replaceState.mock.calls.at(-1)?.[2] as string;
      expect(lastCall).toContain('priority=2');
      expect(lastCall).toContain('type=bug');
      expect(lastCall).not.toContain('view=');
    });

    it('preserves pathname when updating URL', () => {
      mockWindowLocation('');
      Object.defineProperty(window.location, 'pathname', {
        value: '/board',
        configurable: true,
      });

      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current[1]('graph');
      });

      expect(historyMock.replaceState).toHaveBeenCalledWith(null, '', '/board?view=graph');
    });
  });

  describe('popstate handling', () => {
    beforeEach(() => {
      mockWindowLocation('');
      mockWindowHistory();
    });

    it('updates state on browser back/forward navigation', () => {
      mockWindowLocation('?view=table');
      const { result } = renderHook(() => useViewState());

      expect(result.current[0]).toBe('table');

      // Simulate browser navigation (change URL and fire popstate)
      act(() => {
        mockWindowLocation('?view=graph');
        window.dispatchEvent(new PopStateEvent('popstate'));
      });

      expect(result.current[0]).toBe('graph');
    });

    it('returns to DEFAULT_VIEW when navigating to URL without view param', () => {
      mockWindowLocation('?view=table');
      const { result } = renderHook(() => useViewState());

      expect(result.current[0]).toBe('table');

      // Simulate browser navigation to URL without view param
      act(() => {
        mockWindowLocation('');
        window.dispatchEvent(new PopStateEvent('popstate'));
      });

      expect(result.current[0]).toBe('kanban');
    });

    it('cleans up popstate listener on unmount', () => {
      const removeEventListenerSpy = vi.spyOn(window, 'removeEventListener');

      const { unmount } = renderHook(() => useViewState());

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith('popstate', expect.any(Function));
    });

    it('does not add popstate listener when syncUrl is false', () => {
      const addEventListenerSpy = vi.spyOn(window, 'addEventListener');

      renderHook(() => useViewState({ syncUrl: false }));

      const popstateCall = addEventListenerSpy.mock.calls.find((call) => call[0] === 'popstate');
      expect(popstateCall).toBeUndefined();
    });
  });

  describe('setter reference stability', () => {
    it('setView function is stable across re-renders', () => {
      const { result, rerender } = renderHook(() => useViewState({ syncUrl: false }));

      const setView1 = result.current[1];

      rerender();

      const setView2 = result.current[1];

      expect(setView1).toBe(setView2);
    });

    it('setView remains stable when view changes', () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      const setView1 = result.current[1];

      act(() => {
        result.current[1]('table');
      });

      const setView2 = result.current[1];

      expect(setView1).toBe(setView2);
    });
  });
});

describe('parseViewFromUrl', () => {
  beforeEach(() => {
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns DEFAULT_VIEW when no view param', () => {
    mockWindowLocation('');
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it('parses kanban view', () => {
    mockWindowLocation('?view=kanban');
    const result = parseViewFromUrl();
    expect(result).toBe('kanban');
  });

  it('parses table view', () => {
    mockWindowLocation('?view=table');
    const result = parseViewFromUrl();
    expect(result).toBe('table');
  });

  it('parses graph view', () => {
    mockWindowLocation('?view=graph');
    const result = parseViewFromUrl();
    expect(result).toBe('graph');
  });

  it('parses monitor view', () => {
    mockWindowLocation('?view=monitor');
    const result = parseViewFromUrl();
    expect(result).toBe('monitor');
  });

  it('returns DEFAULT_VIEW for invalid view', () => {
    mockWindowLocation('?view=invalid');
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it('returns DEFAULT_VIEW for empty view', () => {
    mockWindowLocation('?view=');
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it('returns DEFAULT_VIEW for numeric view', () => {
    mockWindowLocation('?view=123');
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it('returns DEFAULT_VIEW for uppercase view', () => {
    mockWindowLocation('?view=TABLE');
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });
});

describe('isValidViewMode', () => {
  it('returns true for kanban', () => {
    expect(isValidViewMode('kanban')).toBe(true);
  });

  it('returns true for table', () => {
    expect(isValidViewMode('table')).toBe(true);
  });

  it('returns true for graph', () => {
    expect(isValidViewMode('graph')).toBe(true);
  });

  it('returns true for monitor', () => {
    expect(isValidViewMode('monitor')).toBe(true);
  });

  it('returns false for invalid string', () => {
    expect(isValidViewMode('invalid')).toBe(false);
  });

  it('returns false for empty string', () => {
    expect(isValidViewMode('')).toBe(false);
  });

  it('returns false for null', () => {
    expect(isValidViewMode(null)).toBe(false);
  });

  it('returns false for uppercase valid view', () => {
    expect(isValidViewMode('KANBAN')).toBe(false);
  });

  it('returns false for similar but invalid strings', () => {
    expect(isValidViewMode('kanban ')).toBe(false);
    expect(isValidViewMode(' table')).toBe(false);
    expect(isValidViewMode('graphs')).toBe(false);
  });
});

describe('SSR/non-browser environment', () => {
  let originalWindow: typeof globalThis.window;

  beforeEach(() => {
    originalWindow = globalThis.window;
  });

  afterEach(() => {
    globalThis.window = originalWindow;
    vi.restoreAllMocks();
  });

  it('parseViewFromUrl returns DEFAULT_VIEW when window is undefined', () => {
    // Simulate non-browser environment
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it('parseViewFromUrl returns DEFAULT_VIEW when location is undefined', () => {
    // Simulate partial browser environment
    // @ts-expect-error - intentionally creating partial window for SSR test
    globalThis.window = {};

    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });
});

describe('syncUrl option', () => {
  let historyMock: { replaceState: ReturnType<typeof vi.fn> };

  beforeEach(() => {
    mockWindowLocation('?view=table');
    historyMock = mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('reads from URL when syncUrl is true (default)', () => {
    const { result } = renderHook(() => useViewState());
    expect(result.current[0]).toBe('table');
  });

  it('ignores URL when syncUrl is false', () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));
    expect(result.current[0]).toBe(DEFAULT_VIEW);
  });

  it('writes to URL when syncUrl is true (default)', () => {
    mockWindowLocation('');
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current[1]('graph');
    });

    expect(historyMock.replaceState).toHaveBeenCalledWith(
      null,
      '',
      expect.stringContaining('view=graph')
    );
  });

  it('does not write to URL when syncUrl is false', () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

    act(() => {
      result.current[1]('graph');
    });

    // replaceState should not be called with view param
    const viewCalls = historyMock.replaceState.mock.calls.filter(
      (call) => typeof call[2] === 'string' && call[2].includes('view=')
    );
    expect(viewCalls).toHaveLength(0);
  });

  it('does not listen to popstate when syncUrl is false', () => {
    const addEventListenerSpy = vi.spyOn(window, 'addEventListener');

    renderHook(() => useViewState({ syncUrl: false }));

    const popstateListeners = addEventListenerSpy.mock.calls.filter(
      (call) => call[0] === 'popstate'
    );
    expect(popstateListeners).toHaveLength(0);
  });
});

describe('edge cases', () => {
  beforeEach(() => {
    mockWindowLocation('');
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('handles setting the same view multiple times', () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

    act(() => {
      result.current[1]('table');
    });
    expect(result.current[0]).toBe('table');

    act(() => {
      result.current[1]('table');
    });
    expect(result.current[0]).toBe('table');
  });

  it('handles rapid view changes', () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

    act(() => {
      result.current[1]('table');
      result.current[1]('graph');
      result.current[1]('kanban');
    });

    expect(result.current[0]).toBe('kanban');
  });

  it('works with URL containing hash', () => {
    // Note: URLSearchParams handles hash correctly when passed just the search portion
    // The browser's window.location.search does not include the hash
    mockWindowLocation('?view=table');
    const { result } = renderHook(() => useViewState());

    expect(result.current[0]).toBe('table');
  });

  it('handles URL with multiple view params (uses first)', () => {
    // URLSearchParams.get returns the first value when there are duplicates
    mockWindowLocation('?view=table&view=graph');
    const { result } = renderHook(() => useViewState());

    expect(result.current[0]).toBe('table');
  });
});
