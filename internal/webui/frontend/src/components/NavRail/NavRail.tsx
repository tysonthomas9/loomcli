/**
 * NavRail component.
 * Icon-only navigation rail for switching between views.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import type { ViewMode } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import { CompactRailHost } from "@/components/CompactRail";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";

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
  operatorQueueCount?: number;
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
    id: "home",
    label: "Home",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path
          d="M3 10 12 3l9 7v9a2 2 0 0 1-2 2h-5v-7h-4v7H5a2 2 0 0 1-2-2v-9Z"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
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
  {
    id: "files",
    label: "Files",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path
          d="M6 2h8l4 4v15a1 1 0 01-1 1H6a1 1 0 01-1-1V3a1 1 0 011-1z"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinejoin="round"
        />
        <path
          d="M14 2v4h4"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  {
    id: "skills",
    label: "Skills",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path
          d="M12 4L2 8.5 12 13l10-4.5L12 4z"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinejoin="round"
        />
        <path
          d="M6 11v4.5c0 1.66 2.69 3 6 3s6-1.34 6-3V11"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path
          d="M22 8.5v5"
          fill="none"
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
  operatorQueueCount,
  badges,
  workspaces,
  activeWorkspaceId,
  onWorkspaceSwitch,
  onAddWorkspace,
}: NavRailProps): JSX.Element {
  const rootClassName = [styles.navRail, className].filter(Boolean).join(" ");
  const activeWorkspaceRef = useRef<HTMLButtonElement>(null);
  const workspaceListRef = useRef<HTMLDivElement>(null);
  const switcherRef = useRef<HTMLElement>(null);

  // Keep the active workspace in view (like block: "nearest") by scrolling the
  // list itself. scrollIntoView would also move the browser's Tab starting
  // point to the button, so the first Tab would skip the skip link.
  useEffect(() => {
    const list = workspaceListRef.current;
    const button = activeWorkspaceRef.current;
    if (!list || !button) return;
    const l = list.getBoundingClientRect();
    const b = button.getBoundingClientRect();
    if (b.top < l.top) list.scrollTop -= l.top - b.top;
    else if (b.bottom > l.bottom) list.scrollTop += b.bottom - l.bottom;
    // On the mobile bottom rail the switcher scrolls sideways instead; 6px
    // leaves room for the active ring.
    const switcher = switcherRef.current;
    if (!switcher) return;
    const w = switcher.getBoundingClientRect();
    if (b.left - 6 < w.left) switcher.scrollLeft -= w.left - b.left + 6;
    else if (b.right + 6 > w.right)
      switcher.scrollLeft += b.right - w.right + 6;
  }, [activeWorkspaceId, workspaces]);

  // Which ends of the sideways (mobile) switcher have workspaces scrolled out
  // of view, so the rail can hint at them.
  const [more, setMore] = useState({ left: false, right: false });
  const updateMore = useCallback(() => {
    const s = switcherRef.current;
    if (!s) return;
    const left = s.scrollLeft > 1;
    const right = s.scrollLeft + s.clientWidth < s.scrollWidth - 1;
    setMore((m) =>
      m.left === left && m.right === right ? m : { left, right },
    );
  }, []);
  useEffect(() => {
    updateMore();
    const s = switcherRef.current;
    if (!s || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(updateMore);
    ro.observe(s);
    return () => ro.disconnect();
  }, [updateMore, workspaces]);

  const renderButton = (item: NavItem) => {
    const isActive = (item.activeForViews ?? [item.id]).includes(activeView);
    const showBadge =
      item.id === "terminal" && sessionCount != null && sessionCount > 0;
    const showQueueBadge =
      item.id === "home" &&
      operatorQueueCount != null &&
      operatorQueueCount > 0;
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
        {showQueueBadge && (
          <span
            className={styles.queueBadge}
            data-testid="nav-home-badge"
            aria-label={`${operatorQueueCount} items need you`}
          >
            {operatorQueueCount}
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
          <div className={styles.switcherFrame}>
            {more.left && (
              <span
                className={styles.moreHint}
                data-more-hint="left"
                aria-hidden="true"
              >
                ‹
              </span>
            )}
            <section
              ref={switcherRef}
              className={styles.workspaceSwitcher}
              aria-label="Workspace selector"
              onScroll={updateMore}
            >
              <div className={styles.workspaceList} ref={workspaceListRef}>
                {workspaces?.map((ws) => {
                  const color = getAvatarColor(ws.name);
                  const isActive = ws.id === activeWorkspaceId;
                  return (
                    <CompactRailHost
                      key={ws.id}
                      as="button"
                      type="button"
                      label={ws.name}
                      aria-label={`Switch to ${ws.name}`}
                      hostRef={isActive ? activeWorkspaceRef : undefined}
                      className={styles.wsAvatar}
                      data-active={isActive || undefined}
                      onClick={() => onWorkspaceSwitch?.(ws.id)}
                    >
                      <span
                        className={styles.wsAvatarCircle}
                        style={{
                          backgroundColor: color,
                          color: shouldUseWhiteText(color) ? "#fff" : "#171717",
                        }}
                        aria-hidden="true"
                      >
                        {getCompactAvatarInitials(ws.name)}
                      </span>
                    </CompactRailHost>
                  );
                })}
              </div>
              {onAddWorkspace && (
                <CompactRailHost
                  as="button"
                  type="button"
                  label="Add workspace"
                  className={styles.wsAdd}
                  onClick={onAddWorkspace}
                >
                  +
                </CompactRailHost>
              )}
            </section>
            {more.right && (
              <span
                className={styles.moreHint}
                data-more-hint="right"
                aria-hidden="true"
              >
                ›
              </span>
            )}
          </div>
          <div className={styles.wsDivider} aria-hidden="true" />
        </>
      )}
      {BOTTOM_ITEMS.map(renderButton)}
    </nav>
  );
}
