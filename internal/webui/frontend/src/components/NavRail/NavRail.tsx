/**
 * NavRail component.
 * Icon-only navigation rail for switching between views.
 */

import type { ViewMode } from "@/components/ViewSwitcher";

import styles from "./NavRail.module.css";

export interface NavRailProps {
  activeView: ViewMode;
  onChange: (view: ViewMode) => void;
  className?: string;
}

type NavItem = { id: ViewMode; label: string; icon: JSX.Element };

const TOP_ITEMS: NavItem[] = [
  {
    id: "kanban",
    label: "Kanban",
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
    id: "observability",
    label: "Observability",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <rect
          x="4"
          y="14"
          width="4"
          height="6"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <rect
          x="10"
          y="8"
          width="4"
          height="12"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <rect
          x="16"
          y="4"
          width="4"
          height="16"
          rx="1"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
      </svg>
    ),
  },
  {
    id: "files",
    label: "Files",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path
          d="M3 7V5a2 2 0 012-2h4l2 2h6a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  {
    id: "terminal",
    label: "Terminal",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <rect
          x="2"
          y="3"
          width="20"
          height="18"
          rx="2"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <polyline
          points="6 9 10 12 6 15"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <line
          x1="12"
          y1="15"
          x2="18"
          y2="15"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
        />
      </svg>
    ),
  },
];

const BOTTOM_ITEMS: NavItem[] = [
  {
    id: "settings",
    label: "Settings",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <circle
          cx="12"
          cy="12"
          r="3"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <path
          d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83 0 2 2 0 010-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
];

export function NavRail({
  activeView,
  onChange,
  className,
}: NavRailProps): JSX.Element {
  const rootClassName = [styles.navRail, className].filter(Boolean).join(" ");

  const renderButton = (item: NavItem) => {
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
  };

  return (
    <nav className={rootClassName} aria-label="Primary">
      {TOP_ITEMS.map(renderButton)}
      <div className={styles.spacer} />
      {BOTTOM_ITEMS.map(renderButton)}
    </nav>
  );
}
