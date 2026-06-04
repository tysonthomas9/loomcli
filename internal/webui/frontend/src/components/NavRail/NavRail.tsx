/**
 * NavRail component.
 * Icon-only navigation rail for switching between views.
 */

import type { ViewMode } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./NavRail.module.css";

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
    id: "agents",
    label: "Agents",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <circle
          cx="12"
          cy="8"
          r="4"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        />
        <path
          d="M4 21v-1a6 6 0 016-6h4a6 6 0 016 6v1"
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
        title={item.label}
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
        <span className={styles.tooltip}>{item.label}</span>
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
        <div className={styles.workspaceAvatars}>
          {workspaces?.map((ws) => {
            const color = getAvatarColor(ws.name);
            return (
              <button
                key={ws.id}
                type="button"
                className={styles.wsAvatar}
                data-active={ws.id === activeWorkspaceId || undefined}
                onClick={() => onWorkspaceSwitch?.(ws.id)}
                style={{
                  backgroundColor: color,
                  color: shouldUseWhiteText(color) ? "#fff" : "#171717",
                }}
                aria-label={`Switch to ${ws.name}`}
                title={ws.name}
              >
                {ws.name.charAt(0).toUpperCase()}
                <span className={styles.tooltip}>{ws.name}</span>
              </button>
            );
          })}
          {onAddWorkspace && (
            <button
              type="button"
              className={styles.wsAdd}
              onClick={onAddWorkspace}
              aria-label="Add workspace"
              title="Add workspace"
            >
              +<span className={styles.tooltip}>Add workspace</span>
            </button>
          )}
        </div>
      )}
      {BOTTOM_ITEMS.map(renderButton)}
    </nav>
  );
}
