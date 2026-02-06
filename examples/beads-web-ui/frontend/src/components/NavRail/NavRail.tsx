/**
 * NavRail component.
 * Icon-only navigation rail for switching between views.
 */

import type { ViewMode } from '@/components/ViewSwitcher';

import styles from './NavRail.module.css';

export interface NavRailProps {
  activeView: ViewMode;
  onChange: (view: ViewMode) => void;
  className?: string;
}

const NAV_ITEMS: Array<{ id: ViewMode; label: string; icon: JSX.Element }> = [
  {
    id: 'kanban',
    label: 'Kanban',
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <rect
          x="4"
          y="4"
          width="6"
          height="6"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <rect
          x="14"
          y="4"
          width="6"
          height="6"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <rect
          x="4"
          y="14"
          width="6"
          height="6"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <rect
          x="14"
          y="14"
          width="6"
          height="6"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
      </svg>
    ),
  },
  {
    id: 'monitor',
    label: 'Monitor',
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <rect
          x="3"
          y="5"
          width="18"
          height="12"
          rx="2"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <path d="M8 19h8" stroke="currentColor" strokeWidth="2" />
      </svg>
    ),
  },
];

export function NavRail({ activeView, onChange, className }: NavRailProps): JSX.Element {
  const rootClassName = [styles.navRail, className].filter(Boolean).join(' ');

  return (
    <nav className={rootClassName} aria-label="Primary">
      {NAV_ITEMS.map((item) => {
        const isActive = activeView === item.id;
        return (
          <button
            key={item.id}
            type="button"
            className={styles.navButton}
            data-active={isActive || undefined}
            onClick={() => onChange(item.id)}
            aria-label={item.label}
          >
            <span className={styles.icon}>{item.icon}</span>
            <span className={styles.tooltip} role="tooltip">
              {item.label}
            </span>
          </button>
        );
      })}
    </nav>
  );
}
