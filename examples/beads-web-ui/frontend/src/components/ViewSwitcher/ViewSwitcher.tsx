/**
 * ViewSwitcher component.
 * Tabbed navigation for switching between Kanban, Table, and Graph views.
 */

import { useCallback, useRef } from 'react';

import styles from './ViewSwitcher.module.css';

/**
 * Available view modes.
 */
export type ViewMode = 'kanban' | 'table' | 'graph' | 'monitor';

/**
 * Default view when none is specified.
 */
export const DEFAULT_VIEW: ViewMode = 'kanban';

/**
 * View configuration.
 */
interface ViewConfig {
  id: ViewMode;
  label: string;
}

/**
 * Available views in display order.
 */
const VIEWS: ViewConfig[] = [
  { id: 'kanban', label: 'Kanban' },
  { id: 'table', label: 'Table' },
  { id: 'graph', label: 'Graph' },
  { id: 'monitor', label: 'Monitor' },
];

/**
 * Props for ViewSwitcher component.
 */
export interface ViewSwitcherProps {
  /** Currently active view */
  activeView: ViewMode;
  /** Callback when view changes */
  onChange: (view: ViewMode) => void;
  /** Additional CSS class name */
  className?: string;
  /** Disable view switching (e.g., during loading) */
  disabled?: boolean;
  /** Layout orientation (default: 'horizontal') */
  orientation?: 'horizontal' | 'vertical';
}

/**
 * ViewSwitcher renders a tab group for switching between views.
 * Follows WAI-ARIA tabs pattern with keyboard navigation.
 */
export function ViewSwitcher({
  activeView,
  onChange,
  className,
  disabled = false,
  orientation = 'horizontal',
}: ViewSwitcherProps): JSX.Element {
  const tabRefs = useRef<Map<ViewMode, HTMLButtonElement | null>>(new Map());

  const setTabRef = useCallback(
    (id: ViewMode) => (el: HTMLButtonElement | null) => {
      tabRefs.current.set(id, el);
    },
    []
  );

  // Handle keyboard navigation
  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (disabled) return;

      const currentIndex = VIEWS.findIndex((v) => v.id === activeView);
      let newIndex = currentIndex;

      switch (event.key) {
        case 'ArrowLeft':
        case 'ArrowUp':
          newIndex = currentIndex > 0 ? currentIndex - 1 : VIEWS.length - 1;
          break;
        case 'ArrowRight':
        case 'ArrowDown':
          newIndex = currentIndex < VIEWS.length - 1 ? currentIndex + 1 : 0;
          break;
        case 'Home':
          newIndex = 0;
          break;
        case 'End':
          newIndex = VIEWS.length - 1;
          break;
        default:
          return;
      }

      if (newIndex !== currentIndex) {
        event.preventDefault();
        const newViewConfig = VIEWS[newIndex];
        if (newViewConfig) {
          onChange(newViewConfig.id);
          // Focus the new tab
          tabRefs.current.get(newViewConfig.id)?.focus();
        }
      }
    },
    [activeView, onChange, disabled]
  );

  const rootClassName = [
    styles.viewSwitcher,
    orientation === 'vertical' && styles.vertical,
    className,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div
      className={rootClassName}
      role="tablist"
      aria-label="View selector"
      aria-orientation={orientation}
      onKeyDown={handleKeyDown}
      data-testid="view-switcher"
    >
      {VIEWS.map((view) => {
        const isActive = activeView === view.id;
        const tabClassName = isActive ? `${styles.tab} ${styles.active}` : styles.tab;

        return (
          <button
            key={view.id}
            ref={setTabRef(view.id)}
            role="tab"
            aria-selected={isActive}
            aria-controls="main-content"
            tabIndex={isActive ? 0 : -1}
            className={tabClassName}
            onClick={() => !disabled && onChange(view.id)}
            disabled={disabled}
            data-testid={`view-tab-${view.id}`}
          >
            {view.label}
          </button>
        );
      })}
    </div>
  );
}
