/**
 * TerminalTabBar component.
 * Horizontal tab strip for switching between terminal sessions.
 * Follows WAI-ARIA tabs pattern with keyboard navigation.
 * Supports drag-and-drop reordering via @dnd-kit and tab pinning.
 */

import React, {
  forwardRef,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  DndContext,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  DragOverlay,
} from "@dnd-kit/core";
import type { DragStartEvent, DragEndEvent } from "@dnd-kit/core";
import {
  SortableContext,
  horizontalListSortingStrategy,
  arrayMove,
} from "@dnd-kit/sortable";

import type { ConnectionState } from "@/components/TerminalView/instances";
import { NewTerminalTabMenu } from "@/components/TerminalView/layout";
import { SortableTab } from "./SortableTab";
import { TabContextMenu } from "./TabContextMenu";
import { TabMarkers } from "./TabMarkers";
import styles from "./TerminalTabBar.module.css";

export interface TerminalTab {
  id: string;
  label: string;
  connectionState: ConnectionState;
  brandColor?: string;
  hasUnread?: boolean;
  /**
   * Quiet and apparently parked on a prompt. Distinct from hasUnread: unread
   * means output arrived, waiting means output stopped and it is your turn.
   */
  isWaitingForInput?: boolean;
  isPinned?: boolean;
  /**
   * RFC3339 time this tab's shell was last replaced (server restart). Renders
   * a persistent marker until dismissed; independent of connectionState.
   */
  replacedAt?: string;
}

export interface TerminalTabBarProps {
  tabs: TerminalTab[];
  activeTabId: string;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onNewTab: () => void;
  /** Backends for the "+" dropdown; when set, opens a menu instead of onNewTab. */
  availableBackends?: string[];
  backendsLoading?: boolean;
  onBackendSelect?: (backend: string) => void;
  onTabRename?: (tabId: string, newLabel: string) => void;
  onDuplicateTab?: (tabId: string) => void;
  maxTabsReached?: boolean;
  onCloseAll?: () => void;
  /** When set, shows the agent-style split-right control (moves active tab to a new column). */
  canSplitRight?: boolean;
  onSplitRight?: () => void;
  onExport?: () => void;
  onTabPin?: (tabId: string, pinned: boolean) => void;
  onCloseOthers?: (tabId: string) => void;
  onReorderTabs?: (orderedTabIds: string[]) => void;
  /** Clears a tab's persisted restart marker (marker click or context menu). */
  onDismissRestartNotice?: (tabId: string) => void;
  /** When false, only the tab strip is shown (used on secondary split columns). */
  showToolbarActions?: boolean;
  /**
   * Workspace-wide tab count. In split view each bar only receives a group's
   * tabs — pass the global count so close stays available when other tabs exist.
   */
  totalTabCount?: number;
  /** Native drag between split editor groups (disables in-bar reorder). */
  groupDrag?: {
    onDragStart: (tabId: string) => void;
    onDragEnd: () => void;
  };
  /** Drop zone for tabs dragged from another editor group. */
  dropTarget?: {
    onDragOver: (event: React.DragEvent) => void;
    onDrop: () => void;
  };
}

interface ContextMenuState {
  tabId: string;
  x: number;
  y: number;
}

export const TerminalTabBar = forwardRef<HTMLDivElement, TerminalTabBarProps>(
  function TerminalTabBar(props: TerminalTabBarProps, ref): JSX.Element {
    const {
      tabs,
      activeTabId,
      onTabChange,
      onTabClose,
      onNewTab,
      availableBackends,
      backendsLoading,
      onBackendSelect,
      onTabRename,
      onDuplicateTab,
      maxTabsReached,
      onCloseAll,
      canSplitRight,
      onSplitRight,
      onExport,
      onTabPin,
      onCloseOthers,
      onReorderTabs,
      onDismissRestartNotice,
      showToolbarActions = true,
      groupDrag,
      dropTarget,
      totalTabCount,
    } = props;
    const canCloseTabs = (totalTabCount ?? tabs.length) > 1;
    const tabListRef = useRef<HTMLDivElement | null>(null);
    const inputRef = useRef<HTMLInputElement>(null);
    const [editingTabId, setEditingTabId] = useState<string | null>(null);
    const [draftLabel, setDraftLabel] = useState("");
    const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(
      null,
    );
    const [overflowState, setOverflowState] = useState({
      canScrollLeft: false,
      canScrollRight: false,
    });
    const prevTabCountRef = useRef(tabs.length);
    const [activeDragId, setActiveDragId] = useState<string | null>(null);

    const sensors = useSensors(
      useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
      useSensor(KeyboardSensor),
    );

    const collisionDetection = useMemo(() => {
      return (args: Parameters<typeof closestCenter>[0]) => {
        const { active, droppableContainers } = args;
        const activeTab = tabs.find((t) => t.id === active.id);
        if (!activeTab) return closestCenter(args);
        const sameZone = droppableContainers.filter((c) => {
          const tab = tabs.find((t) => t.id === c.id);
          return (
            tab && (tab.isPinned ?? false) === (activeTab.isPinned ?? false)
          );
        });
        return closestCenter({ ...args, droppableContainers: sameZone });
      };
    }, [tabs]);

    const handleDragStart = useCallback(
      (e: DragStartEvent) => setActiveDragId(String(e.active.id)),
      [],
    );

    const handleDragEnd = useCallback(
      (event: DragEndEvent) => {
        setActiveDragId(null);
        const { active, over } = event;
        if (!over || active.id === over.id) return;
        const aTab = tabs.find((t) => t.id === active.id);
        const oTab = tabs.find((t) => t.id === over.id);
        if (!aTab || !oTab) return;
        if ((aTab.isPinned ?? false) !== (oTab.isPinned ?? false)) return;
        const zone = tabs.filter(
          (t) => (t.isPinned ?? false) === (aTab.isPinned ?? false),
        );
        const reordered = arrayMove(
          zone,
          zone.findIndex((t) => t.id === active.id),
          zone.findIndex((t) => t.id === over.id),
        );
        const other = tabs.filter(
          (t) => (t.isPinned ?? false) !== (aTab.isPinned ?? false),
        );
        const full =
          (aTab.isPinned ?? false)
            ? [...reordered, ...other]
            : [...other, ...reordered];
        onReorderTabs?.(full.map((t) => t.id));
      },
      [tabs, onReorderTabs],
    );

    useEffect(() => {
      if (editingTabId && !tabs.some((t) => t.id === editingTabId))
        setEditingTabId(null);
    }, [tabs, editingTabId]);

    useEffect(() => {
      const el = tabListRef.current;
      if (!el) return;
      const update = () => {
        const { scrollLeft, scrollWidth, clientWidth } = el;
        setOverflowState({
          canScrollLeft: scrollLeft > 1,
          canScrollRight: scrollLeft + clientWidth < scrollWidth - 1,
        });
      };
      update();
      el.addEventListener("scroll", update, { passive: true });
      const obs = new ResizeObserver(update);
      obs.observe(el);
      return () => {
        el.removeEventListener("scroll", update);
        obs.disconnect();
      };
    }, []);

    useEffect(() => {
      const el = tabListRef.current;
      if (!el) return;
      const id = requestAnimationFrame(() => {
        const { scrollLeft, scrollWidth, clientWidth } = el;
        setOverflowState({
          canScrollLeft: scrollLeft > 1,
          canScrollRight: scrollLeft + clientWidth < scrollWidth - 1,
        });
      });
      return () => cancelAnimationFrame(id);
    }, [tabs.length, activeTabId]);

    useEffect(() => {
      const el = tabListRef.current;
      if (!el || !activeTabId) return;
      const tabEl = el.querySelector<HTMLElement>(
        `#terminal-tab-${CSS.escape(activeTabId)}`,
      );
      if (typeof tabEl?.scrollIntoView === "function") {
        tabEl.scrollIntoView({ block: "nearest", inline: "nearest" });
      }
    }, [activeTabId]);

    useEffect(() => {
      if (tabs.length > prevTabCountRef.current) {
        requestAnimationFrame(() => {
          const el = tabListRef.current;
          if (el) el.scrollTo({ left: el.scrollWidth, behavior: "smooth" });
        });
      }
      prevTabCountRef.current = tabs.length;
    }, [tabs.length]);

    useEffect(() => {
      if (editingTabId && inputRef.current) {
        inputRef.current.focus();
        inputRef.current.select();
      }
    }, [editingTabId]);

    const enterEditMode = useCallback(
      (tabId: string, label: string) => {
        if (!onTabRename) return;
        setEditingTabId(tabId);
        setDraftLabel(label);
      },
      [onTabRename],
    );

    const confirmEdit = useCallback(() => {
      if (!editingTabId) return;
      const trimmed = draftLabel.trim();
      const orig = tabs.find((t) => t.id === editingTabId);
      if (trimmed && trimmed !== orig?.label)
        onTabRename?.(editingTabId, trimmed);
      setEditingTabId(null);
    }, [editingTabId, draftLabel, tabs, onTabRename]);

    const cancelEdit = useCallback(() => setEditingTabId(null), []);

    const handleEditKeyDown = useCallback(
      (e: React.KeyboardEvent) => {
        e.stopPropagation();
        if (e.key === "Enter") {
          e.preventDefault();
          confirmEdit();
        } else if (e.key === "Escape") {
          e.preventDefault();
          cancelEdit();
        }
      },
      [confirmEdit, cancelEdit],
    );

    const handleKeyDown = useCallback(
      (event: React.KeyboardEvent) => {
        if (tabs.length === 0) return;
        const ci = tabs.findIndex((t) => t.id === activeTabId);
        let ni = ci;
        switch (event.key) {
          case "ArrowLeft":
            ni = ci > 0 ? ci - 1 : tabs.length - 1;
            break;
          case "ArrowRight":
            ni = ci < tabs.length - 1 ? ci + 1 : 0;
            break;
          case "Home":
            ni = 0;
            break;
          case "End":
            ni = tabs.length - 1;
            break;
          case "Delete":
          case "Backspace":
            if (canCloseTabs) {
              event.preventDefault();
              onTabClose(activeTabId);
            }
            return;
          default:
            return;
        }
        event.preventDefault();
        if (ni !== ci) {
          const t = tabs[ni];
          if (t) onTabChange(t.id);
        }
      },
      [tabs, activeTabId, onTabChange, onTabClose, canCloseTabs],
    );

    const openCtxMenu = useCallback((tabId: string, e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      setContextMenu({
        tabId,
        x: Math.min(e.clientX, window.innerWidth - 160),
        y: Math.min(e.clientY, window.innerHeight - 260),
      });
    }, []);

    const dismissCtxMenu = useCallback(() => setContextMenu(null), []);

    const hasPinned = tabs.some((t) => t.isPinned);
    const hasUnpinned = tabs.some((t) => !t.isPinned);
    const showDivider = hasPinned && hasUnpinned;
    const pinBound = tabs.findIndex((t) => !t.isPinned);
    const tabIds = useMemo(() => tabs.map((t) => t.id), [tabs]);
    const dragTab = activeDragId
      ? tabs.find((t) => t.id === activeDragId)
      : null;
    const ctxTab = contextMenu
      ? tabs.find((t) => t.id === contextMenu.tabId)
      : null;

    const handleDropTarget = useCallback(
      (event: React.DragEvent) => {
        event.preventDefault();
        dropTarget?.onDrop();
      },
      [dropTarget],
    );

    const renderTabItems = () =>
      tabs.map((tab, index) => {
        const isActive = tab.id === activeTabId;
        const cls = isActive
          ? `${styles.tab ?? ""} ${styles.active ?? ""}`
          : (styles.tab ?? "");
        return (
          <React.Fragment key={tab.id}>
            {showDivider && index === pinBound && (
              <div className={styles.pinDivider} />
            )}
            <SortableTab
              id={tab.id}
              className={cls}
              isPinned={tab.isPinned ?? false}
              isActive={isActive}
              onClick={() => onTabChange(tab.id)}
              onContextMenu={(e) => openCtxMenu(tab.id, e)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onTabChange(tab.id);
                } else if (e.key === "F10" && e.shiftKey) {
                  e.preventDefault();
                  e.stopPropagation();
                  setContextMenu({ tabId: tab.id, x: 0, y: 0 });
                }
              }}
              data-testid={`terminal-tab-${tab.id}`}
              {...(groupDrag
                ? {
                    groupDrag: {
                      onDragStart: () => groupDrag.onDragStart(tab.id),
                      onDragEnd: groupDrag.onDragEnd,
                    },
                  }
                : {})}
            >
              {(tab.isPinned ?? false) && (
                <span className={styles.pinIcon} aria-label="Pinned">
                  <svg
                    width="10"
                    height="10"
                    viewBox="0 0 16 16"
                    fill="currentColor"
                  >
                    <path d="M9.828.722a.5.5 0 0 1 .354.146l4.95 4.95a.5.5 0 0 1-.707.708l-.78-.78-3.18 3.18c.36.59.58 1.28.58 2.02 0 .63-.15 1.22-.42 1.74l-2.48-2.48-3.39 3.39a.5.5 0 0 1-.707-.707l3.39-3.39-2.48-2.48c.52-.27 1.11-.42 1.74-.42.74 0 1.43.22 2.02.58l3.18-3.18-.78-.78a.5.5 0 0 1 .354-.854z" />
                  </svg>
                </span>
              )}
              <span
                className={styles.statusDot}
                data-status={tab.connectionState}
                data-testid={`terminal-tab-status-${tab.id}`}
                aria-label={`Connection: ${tab.connectionState}`}
                style={
                  tab.brandColor
                    ? ({
                        "--brand-color": tab.brandColor,
                      } as React.CSSProperties)
                    : undefined
                }
              />
              <TabMarkers
                tabId={tab.id}
                replacedAt={tab.replacedAt}
                onDismissRestartNotice={
                  onDismissRestartNotice
                    ? () => onDismissRestartNotice(tab.id)
                    : undefined
                }
              />
              {editingTabId === tab.id ? (
                <input
                  ref={inputRef}
                  type="text"
                  className={styles.tabLabelInput}
                  value={draftLabel}
                  onChange={(e) => setDraftLabel(e.target.value)}
                  onBlur={confirmEdit}
                  onKeyDown={handleEditKeyDown}
                  onClick={(e) => e.stopPropagation()}
                  aria-label="Rename tab"
                  data-testid={`terminal-tab-rename-input-${tab.id}`}
                />
              ) : (
                <span
                  className={styles.tabLabel}
                  onDoubleClick={(e) => {
                    e.stopPropagation();
                    enterEditMode(tab.id, tab.label);
                  }}
                  data-testid={`terminal-tab-label-${tab.id}`}
                >
                  {tab.label}
                </span>
              )}
              {tab.hasUnread && !isActive && (
                <span
                  role="img"
                  className={styles.unreadDot}
                  aria-label="has new output"
                  data-testid={`terminal-tab-unread-${tab.id}`}
                />
              )}
              {/*
                Deliberately NOT gated on !isActive, unlike the unread dot:
                the incident this badge exists for happened on the tab the
                user was looking at.
              */}
              {tab.isWaitingForInput && (
                <span
                  role="img"
                  className={styles.waitingBadge}
                  aria-label="waiting for input"
                  title="Quiet — this session looks like it is waiting for input"
                  data-testid={`terminal-tab-waiting-${tab.id}`}
                />
              )}
              {canCloseTabs && (
                <button
                  type="button"
                  tabIndex={-1}
                  className={styles.closeButton}
                  draggable={false}
                  onDragStart={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    onTabClose(tab.id);
                  }}
                  aria-label={`Close ${tab.label}`}
                  data-testid={`terminal-tab-close-${tab.id}`}
                >
                  ×
                </button>
              )}
            </SortableTab>
          </React.Fragment>
        );
      });

    const newTabControl =
      showToolbarActions &&
      (onBackendSelect ? (
        <NewTerminalTabMenu
          availableBackends={availableBackends ?? []}
          isLoading={backendsLoading ?? false}
          disabled={maxTabsReached ?? false}
          onSelect={onBackendSelect}
          onDisabledAttempt={onNewTab}
        />
      ) : (
        <button
          className={styles.actionButton}
          onClick={onNewTab}
          disabled={maxTabsReached}
          aria-label="New terminal tab"
          title={
            maxTabsReached
              ? "Maximum terminal tabs reached"
              : "New terminal tab"
          }
          data-testid="terminal-new-tab-button"
        >
          +
        </button>
      ));

    const tabList = (
      <div
        ref={tabListRef}
        className={styles.tabList}
        role="tablist"
        aria-label="Terminal tabs"
        aria-keyshortcuts="Meta+1 Meta+2 Meta+3 Meta+4 Meta+5 Meta+6 Meta+7 Meta+8 Meta+9 Meta+T Meta+W"
        onKeyDown={handleKeyDown}
        onDragOver={dropTarget?.onDragOver}
        onDrop={dropTarget ? handleDropTarget : undefined}
      >
        {renderTabItems()}
      </div>
    );

    const tabListWithDnd = groupDrag ? (
      tabList
    ) : (
      <DndContext
        sensors={sensors}
        collisionDetection={collisionDetection}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <SortableContext
          items={tabIds}
          strategy={horizontalListSortingStrategy}
        >
          {tabList}
        </SortableContext>
        <DragOverlay dropAnimation={null}>
          {dragTab ? (
            <div
              className={`${styles.tab} ${styles.active} ${styles.dragOverlay}`}
            >
              <span
                className={styles.statusDot}
                data-status={dragTab.connectionState}
              />
              {/* `-drag` suffix: the dragged tab is still mounted in the strip,
                  so the overlay must not duplicate its test ids. */}
              <TabMarkers
                tabId={`${dragTab.id}-drag`}
                replacedAt={dragTab.replacedAt}
                static
              />
              <span className={styles.tabLabel}>{dragTab.label}</span>
              {dragTab.isWaitingForInput && (
                <span
                  role="img"
                  className={styles.waitingBadge}
                  aria-label="waiting for input"
                  data-testid={`terminal-tab-waiting-overlay-${dragTab.id}`}
                />
              )}
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    );

    const hasToolbarActions =
      showToolbarActions &&
      ((onCloseAll && tabs.length > 0) || onSplitRight || onExport);

    return (
      <div
        ref={ref}
        className={styles.tabBar}
        data-testid="terminal-tab-bar"
        onDragOver={dropTarget?.onDragOver}
        onDrop={dropTarget ? handleDropTarget : undefined}
      >
        <div className={styles.tabStrip}>
          {overflowState.canScrollLeft && (
            <button
              type="button"
              className={`${styles.scrollButton} ${styles.scrollButtonLeft}`}
              onClick={() =>
                tabListRef.current?.scrollBy({ left: -150, behavior: "smooth" })
              }
              aria-label="Scroll tabs left"
              tabIndex={-1}
              data-testid="scroll-tabs-left"
            >
              &#x2039;
            </button>
          )}
          {tabListWithDnd}
          {newTabControl}
          {overflowState.canScrollRight && (
            <button
              type="button"
              className={`${styles.scrollButton} ${styles.scrollButtonRight}`}
              onClick={() =>
                tabListRef.current?.scrollBy({ left: 150, behavior: "smooth" })
              }
              aria-label="Scroll tabs right"
              tabIndex={-1}
              data-testid="scroll-tabs-right"
            >
              &#x203a;
            </button>
          )}
        </div>
        {hasToolbarActions && (
          <div className={styles.toolbarActions}>
            {showToolbarActions && onCloseAll && tabs.length > 0 && (
              <button
                className={styles.actionButton}
                onClick={onCloseAll}
                aria-label="Close all sessions"
                title="Close all sessions"
              >
                &#x2715;&#x2715;
              </button>
            )}
            {showToolbarActions && onSplitRight && (
              <button
                type="button"
                className={styles.actionButton}
                onClick={onSplitRight}
                disabled={canSplitRight === false}
                aria-label="Split editor right"
                title="Move active tab to the right pane"
                data-testid="terminal-split-right"
              >
                <svg
                  className={styles.splitIcon}
                  width={15}
                  height={15}
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={1.7}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M4 4h16v16H4z" />
                  <path d="M12 4v16" />
                </svg>
              </button>
            )}
            {showToolbarActions && onExport && (
              <button
                className={styles.actionButton}
                onClick={onExport}
                aria-label="Export session"
                title="Export session"
              >
                {"\u21E3"}
              </button>
            )}
          </div>
        )}
        {contextMenu && ctxTab && (
          <TabContextMenu
            tabId={contextMenu.tabId}
            isPinned={ctxTab.isPinned ?? false}
            x={contextMenu.x}
            y={contextMenu.y}
            tabCount={tabs.length}
            maxTabsReached={maxTabsReached}
            onDuplicate={
              onDuplicateTab
                ? () => onDuplicateTab(contextMenu.tabId)
                : undefined
            }
            onRename={
              onTabRename
                ? () => {
                    const t = tabs.find((x) => x.id === contextMenu.tabId);
                    if (t) enterEditMode(t.id, t.label);
                  }
                : undefined
            }
            onPin={
              onTabPin
                ? () => onTabPin(contextMenu.tabId, !(ctxTab.isPinned ?? false))
                : undefined
            }
            onClose={() => onTabClose(contextMenu.tabId)}
            onCloseOthers={
              onCloseOthers ? () => onCloseOthers(contextMenu.tabId) : undefined
            }
            onCloseAll={onCloseAll}
            onDismissRestartNotice={
              onDismissRestartNotice && ctxTab.replacedAt
                ? () => onDismissRestartNotice(contextMenu.tabId)
                : undefined
            }
            onDismiss={dismissCtxMenu}
          />
        )}
      </div>
    );
  },
);
