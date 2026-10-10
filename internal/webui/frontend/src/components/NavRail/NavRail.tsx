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

/** One avatar plus the gap after it, as laid out in NavRail.module.css. */
const SWITCHER_ITEM_PITCH_PX = 41;
/** The switcher slot width that fits two 44px chevron buttons and an item.
 *  The same number is the container query in NavRail.module.css. */
const CHEVRON_BUTTONS_MIN_SLOT_PX = 126;

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
  /** Toggle the agents drawer; adds the phone-only Agents button (MOB2). */
  onAgentsToggle?: () => void;
  /** Whether the agents drawer is open. */
  agentsOpen?: boolean;
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
  onAgentsToggle,
  agentsOpen = false,
}: NavRailProps): JSX.Element {
  const rootClassName = [styles.navRail, className].filter(Boolean).join(" ");
  const activeWorkspaceRef = useRef<HTMLButtonElement>(null);
  const workspaceListRef = useRef<HTMLDivElement>(null);
  const switcherRef = useRef<HTMLElement>(null);
  const frameRef = useRef<HTMLDivElement>(null);
  const leftChevronRef = useRef<HTMLButtonElement>(null);
  const rightChevronRef = useRef<HTMLButtonElement>(null);
  // Where focus goes once a focused chevron goes away: the other chevron at
  // an end, or a shown workspace when the slot narrows to passive hints.
  const refocusChevron = useRef<"left" | "right" | "item" | null>(null);

  const hasAdd = Boolean(onAddWorkspace);
  // Which ends of the sideways (mobile) switcher have workspaces scrolled out
  // of view, so the rail can hint at them, and whether its slot has room for
  // the hints to be 44px buttons.
  const [more, setMore] = useState({
    left: false,
    right: false,
    buttons: false,
  });
  // Keyed on the workspaces' ids, not the array: App passes a new array each
  // render, and re-running would undo the user's own scrolling.
  const workspaceIds = workspaces?.map((w) => w.id).join("\n");

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
  }, [activeWorkspaceId, workspaceIds]);

  const updateMore = useCallback(() => {
    const s = switcherRef.current;
    if (!s) return;
    // Within the 4px padding nothing is hidden (a snap can stop there).
    const left = s.scrollLeft > 4;
    const right = s.scrollLeft + s.clientWidth < s.scrollWidth - 4;
    // Unrounded, like the CSS container query this mirrors.
    const buttons =
      (frameRef.current?.getBoundingClientRect().width ?? 0) >=
      CHEVRON_BUTTONS_MIN_SLOT_PX;
    // A focused chevron that goes away (a click or a scroll reached its
    // end) hands the keyboard to the other one.
    const focused = document.activeElement;
    if (focused && focused === leftChevronRef.current && !left)
      refocusChevron.current = "right";
    if (focused && focused === rightChevronRef.current && !right)
      refocusChevron.current = "left";
    if (
      !buttons &&
      focused &&
      (focused === leftChevronRef.current ||
        focused === rightChevronRef.current)
    )
      refocusChevron.current = "item";
    setMore((m) =>
      m.left === left && m.right === right && m.buttons === buttons
        ? m
        : { left, right, buttons },
    );
  }, []);
  useEffect(() => {
    updateMore();
    const s = switcherRef.current;
    if (!s || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(updateMore);
    ro.observe(s);
    if (frameRef.current) ro.observe(frameRef.current);
    return () => ro.disconnect();
  }, [updateMore, workspaces, hasAdd]);
  useEffect(() => {
    const side = refocusChevron.current;
    refocusChevron.current = null;
    if (side === "left" || side === "right")
      (side === "left" ? leftChevronRef : rightChevronRef).current?.focus();
    const s = switcherRef.current;
    if (side !== "item" || !s) return;
    const w = s.getBoundingClientRect();
    Array.from(s.querySelectorAll("button"))
      .find((b) => {
        const r = b.getBoundingClientRect();
        return r.left >= w.left - 0.5 && r.right <= w.right + 0.5;
      })
      ?.focus();
  }, [more]);

  const scrollByItem = (side: "left" | "right") => {
    switcherRef.current?.scrollBy({
      left: side === "left" ? -SWITCHER_ITEM_PITCH_PX : SWITCHER_ITEM_PITCH_PX,
    });
    updateMore();
  };

  const renderChevron = (side: "left" | "right") => {
    if (!more[side]) return null;
    const glyph = side === "left" ? "‹" : "›";
    if (!more.buttons)
      return (
        <span
          className={styles.moreHint}
          data-more-hint={side}
          aria-hidden="true"
        >
          {glyph}
        </span>
      );
    return (
      <button
        ref={side === "left" ? leftChevronRef : rightChevronRef}
        type="button"
        className={styles.moreHint}
        data-more-hint={side}
        aria-label={`Scroll workspaces ${side}`}
        onClick={() => scrollByItem(side)}
      >
        {glyph}
      </button>
    );
  };

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
      {onAgentsToggle && (
        <button
          type="button"
          className={`${styles.navButton} ${styles.agentsButton}`}
          data-active={agentsOpen || undefined}
          onClick={onAgentsToggle}
          aria-label="Agents"
          aria-expanded={agentsOpen}
          aria-controls={agentsOpen ? "agents-drawer" : undefined}
        >
          <span className={styles.icon}>
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path
                d="M4 6h16M4 12h16M4 18h16"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          </span>
        </button>
      )}
      {TOP_ITEMS.map(renderButton)}
      <div className={styles.spacer} />
      {hasWorkspaceAvatars && (
        <>
          <div className={styles.wsDivider} aria-hidden="true" />
          <div ref={frameRef} className={styles.switcherFrame}>
            {renderChevron("left")}
            <section
              ref={switcherRef}
              className={styles.workspaceSwitcher}
              aria-label="Workspace selector"
              onScroll={updateMore}
            >
              {/* No empty list: its gap would push Add off a one-item window. */}
              {workspaces && workspaces.length > 0 && (
                <div className={styles.workspaceList} ref={workspaceListRef}>
                  {workspaces.map((ws) => {
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
                            color: shouldUseWhiteText(color)
                              ? "#fff"
                              : "#171717",
                          }}
                          aria-hidden="true"
                        >
                          {getCompactAvatarInitials(ws.name)}
                        </span>
                      </CompactRailHost>
                    );
                  })}
                </div>
              )}
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
            {renderChevron("right")}
          </div>
          <div className={styles.wsDivider} aria-hidden="true" />
        </>
      )}
      {BOTTOM_ITEMS.map(renderButton)}
    </nav>
  );
}
