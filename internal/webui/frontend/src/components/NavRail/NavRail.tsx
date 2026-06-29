/**
 * NavRail component.
 * Icon-only navigation rail for switching between views.
 */

import { useEffect, useRef } from "react";

import type { ViewMode } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./NavRail.module.css";

/** Aether wireframe pin 5: ~5 workspace dots visible, then scroll. */
export const WORKSPACE_SWITCHER_LIST_MAX_HEIGHT_PX = 210;

export interface NavRailWorkspace {
  id: string;
  name: string;
}

export interface NavRailProps {
  activeView: ViewMode;
  onChange: (view: ViewMode) => void;
  className?: string;
  sessionCount?: number;
  badges?: Partial<Record<ViewMode, boolean>>;
  /** Workspaces shown as switcher avatars at the rail bottom. */
  workspaces?: NavRailWorkspace[];
  /** Currently active workspace id (highlighted avatar). */
  activeWorkspaceId?: string;
  /** Switch to a workspace by id. */
  onWorkspaceSwitch?: (id: string) => void;
  /** Open the create-workspace flow. */
  onAddWorkspace?: () => void;
}

type NavItem = {
  id: ViewMode;
  label: string;
  icon: JSX.Element;
  activeForViews?: ViewMode[];
};

const TOP_ITEMS: NavItem[] = [
  {
    id: "kanban",
    label: "Workspaces",
    activeForViews: ["kanban", "table"],
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
    id: "prs",
    label: "Pull Requests",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <circle
          cx="6"
          cy="6"
          r="2.5"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <circle
          cx="6"
          cy="18"
          r="2.5"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <circle
          cx="18"
          cy="18"
          r="2.5"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <path
          d="M6 8.5v7M18 15.5V12a3 3 0 00-3-3h-3"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
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
          height="14"
          rx="2"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <line
          x1="8"
          y1="21"
          x2="16"
          y2="21"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
        />
        <line
          x1="12"
          y1="17"
          x2="12"
          y2="21"
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
  sessionCount,
  badges,
  workspaces,
  activeWorkspaceId,
  onWorkspaceSwitch,
  onAddWorkspace,
}: NavRailProps): JSX.Element {
  const rootClassName = [styles.navRail, className].filter(Boolean).join(" ");
  const activeWorkspaceRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    activeWorkspaceRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [activeWorkspaceId, workspaces]);

  const renderButton = (item: NavItem) => {
    const isActive = (item.activeForViews ?? [item.id]).includes(activeView);
    const showBadge =
      item.id === "terminal" && sessionCount != null && sessionCount > 0;
    const showUnread = !isActive && badges?.[item.id] === true;
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
        {showBadge && (
          <span
            className={styles.badge}
            aria-label={`${sessionCount} active sessions`}
          >
            {sessionCount}
          </span>
        )}
        {showUnread && (
          <span
            role="img"
            className={styles.unreadIndicator}
            aria-label="has unread output"
          />
        )}
        <span className={styles.tooltip} role="tooltip">
          {item.label}
        </span>
      </button>
    );
  };

  const hasWorkspaceAvatars =
    (workspaces && workspaces.length > 0) || Boolean(onAddWorkspace);

  return (
    <nav className={rootClassName} aria-label="Primary">
      {TOP_ITEMS.map(renderButton)}
      <div className={styles.spacer} />
      {hasWorkspaceAvatars && (
        <>
          <div className={styles.wsDivider} aria-hidden="true" />
          <section
            className={styles.workspaceSwitcher}
            aria-label="Workspace selector"
          >
            <div className={styles.workspaceList}>
              {workspaces?.map((ws) => {
                const color = getAvatarColor(ws.name);
                const isActive = ws.id === activeWorkspaceId;
                return (
                  <button
                    key={ws.id}
                    ref={isActive ? activeWorkspaceRef : undefined}
                    type="button"
                    className={styles.wsAvatar}
                    data-active={isActive || undefined}
                    onClick={() => onWorkspaceSwitch?.(ws.id)}
                    aria-label={`Switch to ${ws.name}`}
                  >
                    <span
                      className={styles.wsAvatarCircle}
                      style={{
                        backgroundColor: color,
                        color: shouldUseWhiteText(color) ? "#fff" : "#171717",
                      }}
                      aria-hidden="true"
                    >
                      {ws.name.charAt(0).toUpperCase()}
                    </span>
                    <span className={styles.tooltip} role="tooltip">
                      {ws.name}
                    </span>
                  </button>
                );
              })}
            </div>
            {onAddWorkspace && (
              <button
                type="button"
                className={styles.wsAdd}
                onClick={onAddWorkspace}
                aria-label="Add workspace"
              >
                +
                <span className={styles.tooltip} role="tooltip">
                  Add workspace
                </span>
              </button>
            )}
          </section>
          <div className={styles.wsDivider} aria-hidden="true" />
        </>
      )}
      {BOTTOM_ITEMS.map(renderButton)}
    </nav>
  );
}
