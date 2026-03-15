/**
 * TerminalTabBar component.
 * Horizontal tab strip for switching between terminal sessions.
 * Follows WAI-ARIA tabs pattern with keyboard navigation.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import type { ConnectionState } from "./TerminalInstance";
import styles from "./TerminalTabBar.module.css";

export interface TerminalTab {
  id: string;
  label: string;
  connectionState: ConnectionState;
  brandColor?: string;
  hasUnread?: boolean;
}

export interface TerminalTabBarProps {
  tabs: TerminalTab[];
  activeTabId: string;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onNewTab: () => void;
  onToggleFullHeight: () => void;
  isFullHeight: boolean;
  onTabRename?: (tabId: string, newLabel: string) => void;
  onDuplicateTab?: (tabId: string) => void;
  maxTabsReached?: boolean;
  onCloseAll?: () => void;
}

interface ContextMenuState {
  tabId: string;
  x: number;
  y: number;
}

export function TerminalTabBar({
  tabs,
  activeTabId,
  onTabChange,
  onTabClose,
  onNewTab,
  onToggleFullHeight,
  isFullHeight,
  onTabRename,
  onDuplicateTab,
  maxTabsReached,
  onCloseAll,
}: TerminalTabBarProps): JSX.Element {
  const tabRefs = useRef<Map<string, HTMLDivElement | null>>(new Map());
  const tabListRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const contextMenuRef = useRef<HTMLDivElement | null>(null);

  const [editingTabId, setEditingTabId] = useState<string | null>(null);
  const [draftLabel, setDraftLabel] = useState("");
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);

  const setTabRef = useCallback(
    (id: string) => (el: HTMLDivElement | null) => {
      if (el) {
        tabRefs.current.set(id, el);
      } else {
        tabRefs.current.delete(id);
      }
    },
    [],
  );

  // Scroll active tab into view when it changes, but only if focus is
  // already within the tablist (e.g. after keyboard navigation).
  useEffect(() => {
    const activeEl = tabRefs.current.get(activeTabId);
    if (!activeEl) return;

    const tabList = tabListRef.current;
    if (tabList && tabList.contains(document.activeElement)) {
      activeEl.focus();
      activeEl.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }, [activeTabId]);

  // Clear editing state if the tab being edited is removed
  useEffect(() => {
    if (editingTabId && !tabs.some((t) => t.id === editingTabId)) {
      setEditingTabId(null);
    }
  }, [tabs, editingTabId]);

  // Focus and select input when entering edit mode
  useEffect(() => {
    if (editingTabId && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [editingTabId]);

  const enterEditMode = useCallback(
    (tabId: string, currentLabel: string) => {
      if (!onTabRename) return;
      setEditingTabId(tabId);
      setDraftLabel(currentLabel);
    },
    [onTabRename],
  );

  const confirmEdit = useCallback(() => {
    if (!editingTabId) return;
    const trimmed = draftLabel.trim();
    const originalTab = tabs.find((t) => t.id === editingTabId);
    if (trimmed && trimmed !== originalTab?.label) {
      onTabRename?.(editingTabId, trimmed);
    }
    setEditingTabId(null);
  }, [editingTabId, draftLabel, tabs, onTabRename]);

  const cancelEdit = useCallback(() => {
    setEditingTabId(null);
  }, []);

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

      const currentIndex = tabs.findIndex((t) => t.id === activeTabId);
      let newIndex = currentIndex;

      switch (event.key) {
        case "ArrowLeft":
          newIndex = currentIndex > 0 ? currentIndex - 1 : tabs.length - 1;
          break;
        case "ArrowRight":
          newIndex = currentIndex < tabs.length - 1 ? currentIndex + 1 : 0;
          break;
        case "Home":
          newIndex = 0;
          break;
        case "End":
          newIndex = tabs.length - 1;
          break;
        default:
          return;
      }

      event.preventDefault();
      if (newIndex !== currentIndex) {
        const newTab = tabs[newIndex];
        if (newTab) {
          onTabChange(newTab.id);
          tabRefs.current.get(newTab.id)?.focus();
        }
      }
    },
    [tabs, activeTabId, onTabChange],
  );

  const handleCloseClick = useCallback(
    (tabId: string, e: React.MouseEvent) => {
      e.stopPropagation();
      onTabClose(tabId);
    },
    [onTabClose],
  );

  const handleContextMenu = useCallback(
    (tabId: string, e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      // Position the menu, clamping to viewport edges
      const x = Math.min(e.clientX, window.innerWidth - 160);
      const y = Math.min(e.clientY, window.innerHeight - 120);
      setContextMenu({ tabId, x, y });
    },
    [],
  );

  const handleContextMenuDuplicate = useCallback(() => {
    if (contextMenu && onDuplicateTab) {
      onDuplicateTab(contextMenu.tabId);
    }
    setContextMenu(null);
  }, [contextMenu, onDuplicateTab]);

  const handleContextMenuRename = useCallback(() => {
    if (contextMenu) {
      const tab = tabs.find((t) => t.id === contextMenu.tabId);
      if (tab) enterEditMode(tab.id, tab.label);
    }
    setContextMenu(null);
  }, [contextMenu, tabs, enterEditMode]);

  const handleContextMenuClose = useCallback(() => {
    if (contextMenu) {
      onTabClose(contextMenu.tabId);
    }
    setContextMenu(null);
  }, [contextMenu, onTabClose]);

  // Close context menu on outside click, Escape, or scroll
  useEffect(() => {
    if (!contextMenu) return;
    const handleClick = (e: MouseEvent) => {
      if (
        contextMenuRef.current &&
        !contextMenuRef.current.contains(e.target as Node)
      ) {
        setContextMenu(null);
      }
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setContextMenu(null);
    };
    const handleScroll = () => setContextMenu(null);
    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKeyDown);
    window.addEventListener("scroll", handleScroll, true);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("scroll", handleScroll, true);
    };
  }, [contextMenu]);

  return (
    <div className={styles.tabBar} data-testid="terminal-tab-bar">
      <div
        ref={tabListRef}
        className={styles.tabList}
        role="tablist"
        aria-label="Terminal tabs"
        onKeyDown={handleKeyDown}
      >
        {tabs.map((tab) => {
          const isActive = tab.id === activeTabId;
          const tabClassName = isActive
            ? `${styles.tab} ${styles.active}`
            : styles.tab;

          return (
            <div
              key={tab.id}
              ref={setTabRef(tab.id)}
              role="tab"
              aria-selected={isActive}
              tabIndex={isActive ? 0 : -1}
              className={tabClassName}
              onClick={() => onTabChange(tab.id)}
              onContextMenu={(e) => handleContextMenu(tab.id, e)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onTabChange(tab.id);
                }
                if (e.shiftKey && e.key === "F10") {
                  e.preventDefault();
                  const rect = (
                    e.currentTarget as HTMLElement
                  ).getBoundingClientRect();
                  setContextMenu({
                    tabId: tab.id,
                    x: rect.left,
                    y: rect.bottom,
                  });
                }
              }}
              data-testid={`terminal-tab-${tab.id}`}
            >
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
              {tabs.length > 1 && (
                <button
                  type="button"
                  tabIndex={-1}
                  className={styles.closeButton}
                  onClick={(e) => handleCloseClick(tab.id, e)}
                  aria-label={`Close ${tab.label}`}
                  data-testid={`terminal-tab-close-${tab.id}`}
                >
                  ×
                </button>
              )}
            </div>
          );
        })}
      </div>
      <button
        className={styles.actionButton}
        onClick={onNewTab}
        aria-label="New terminal tab"
        data-testid="terminal-new-tab-button"
      >
        +
      </button>
      {onCloseAll && tabs.length > 0 && (
        <button
          className={styles.actionButton}
          onClick={onCloseAll}
          aria-label="Close all sessions"
          data-testid="terminal-close-all-button"
          title="Close all sessions"
        >
          &#x2715;&#x2715;
        </button>
      )}
      <button
        className={styles.actionButton}
        onClick={onToggleFullHeight}
        aria-label="Toggle full height"
        aria-pressed={isFullHeight}
        data-testid="terminal-fullheight-toggle"
      >
        {isFullHeight ? "\u2921" : "\u2922"}
      </button>
      {contextMenu && (
        <div
          ref={contextMenuRef}
          className={styles.contextMenu}
          style={{ left: contextMenu.x, top: contextMenu.y }}
          role="menu"
          data-testid="terminal-tab-context-menu"
        >
          {onDuplicateTab && (
            <button
              type="button"
              className={
                maxTabsReached
                  ? `${styles.contextMenuItem} ${styles.contextMenuItemDisabled}`
                  : styles.contextMenuItem
              }
              onClick={handleContextMenuDuplicate}
              disabled={maxTabsReached}
              role="menuitem"
              data-testid="context-menu-duplicate"
              title={maxTabsReached ? "Maximum tabs reached" : undefined}
            >
              Duplicate
            </button>
          )}
          {onTabRename && (
            <button
              type="button"
              className={styles.contextMenuItem}
              onClick={handleContextMenuRename}
              role="menuitem"
              data-testid="context-menu-rename"
            >
              Rename
            </button>
          )}
          {(onDuplicateTab || onTabRename) && tabs.length > 1 && (
            <div className={styles.contextMenuDivider} />
          )}
          {tabs.length > 1 && (
            <button
              type="button"
              className={styles.contextMenuItem}
              onClick={handleContextMenuClose}
              role="menuitem"
              data-testid="context-menu-close"
            >
              Close
            </button>
          )}
        </div>
      )}
    </div>
  );
}
