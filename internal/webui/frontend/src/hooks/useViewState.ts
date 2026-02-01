/**
 * useViewState - React hook for managing view mode state with URL synchronization.
 * Provides centralized view state management for switching between Kanban, Table, and Graph views.
 */

import { useState, useCallback, useEffect } from 'react';
import { type ViewMode, DEFAULT_VIEW } from '@/components/ViewSwitcher';

/**
 * Valid view modes for validation.
 */
const VALID_VIEWS: ViewMode[] = ['kanban', 'table', 'graph', 'monitor'];

/**
 * URL parameter name for view.
 */
const VIEW_PARAM = 'view';

/**
 * Options for useViewState hook.
 */
export interface UseViewStateOptions {
  /** Whether to sync with URL (default: true) */
  syncUrl?: boolean;
}

/**
 * Return type for useViewState hook.
 */
export type UseViewStateReturn = [ViewMode, (view: ViewMode) => void];

/**
 * Check if running in browser environment.
 */
function isBrowser(): boolean {
  return typeof window !== 'undefined' && typeof window.location !== 'undefined';
}

/**
 * Check if a value is a valid view mode.
 */
function isValidViewMode(value: string | null): value is ViewMode {
  return value !== null && VALID_VIEWS.includes(value as ViewMode);
}

/**
 * Parse view mode from URL search parameters.
 * Returns DEFAULT_VIEW for invalid or missing values.
 */
function parseViewFromUrl(): ViewMode {
  if (!isBrowser()) return DEFAULT_VIEW;

  const params = new URLSearchParams(window.location.search);
  const view = params.get(VIEW_PARAM);

  if (isValidViewMode(view)) {
    return view;
  }

  return DEFAULT_VIEW;
}

/**
 * Update URL with view mode without triggering navigation.
 * Uses replaceState to avoid polluting browser history.
 * Removes the view param from URL when it matches DEFAULT_VIEW for cleaner URLs.
 */
function updateViewUrl(view: ViewMode): void {
  if (!isBrowser()) return;

  const params = new URLSearchParams(window.location.search);

  if (view === DEFAULT_VIEW) {
    // Clean URL for default view
    params.delete(VIEW_PARAM);
  } else {
    params.set(VIEW_PARAM, view);
  }

  const queryString = params.toString();
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;

  window.history.replaceState(null, '', newUrl);
}

/**
 * React hook for managing view mode state with URL synchronization.
 *
 * @example
 * ```tsx
 * function App() {
 *   const [activeView, setActiveView] = useViewState();
 *
 *   return (
 *     <ViewSwitcher
 *       activeView={activeView}
 *       onChange={setActiveView}
 *     />
 *   );
 * }
 * ```
 */
export function useViewState(options: UseViewStateOptions = {}): UseViewStateReturn {
  const { syncUrl = true } = options;

  // Initialize state from URL if syncing and in browser
  const [view, setViewState] = useState<ViewMode>(() => {
    if (syncUrl) {
      return parseViewFromUrl();
    }
    return DEFAULT_VIEW;
  });

  // Sync URL when state changes
  useEffect(() => {
    if (syncUrl && isBrowser()) {
      updateViewUrl(view);
    }
  }, [view, syncUrl]);

  // Handle browser back/forward navigation
  useEffect(() => {
    if (!syncUrl || !isBrowser()) return;

    const handlePopState = () => {
      setViewState(parseViewFromUrl());
    };

    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, [syncUrl]);

  // Memoized setter
  const setView = useCallback((newView: ViewMode) => {
    setViewState(newView);
  }, []);

  return [view, setView];
}

// Export helpers for testing
export { parseViewFromUrl, isValidViewMode };
