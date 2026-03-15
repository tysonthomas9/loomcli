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
}: TerminalTabBarProps): JSX.Element {
  const tabRefs = useRef<Map<string, HTMLDivElement | null>>(new Map());
  const tabListRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const [editingTabId, setEditingTabId] = useState<string | null>(null);
  const [draftLabel, setDraftLabel] = useState("");

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
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onTabChange(tab.id);
                }
              }}
              data-testid={`terminal-tab-${tab.id}`}
            >
              <span
                className={styles.statusDot}
                data-status={tab.connectionState}
                data-testid={`terminal-tab-status-${tab.id}`}
                aria-label={`Connection: ${tab.connectionState}`}
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
      <button
        className={styles.actionButton}
        onClick={onToggleFullHeight}
        aria-label="Toggle full height"
        aria-pressed={isFullHeight}
        data-testid="terminal-fullheight-toggle"
      >
        {isFullHeight ? "\u2921" : "\u2922"}
      </button>
    </div>
  );
}
