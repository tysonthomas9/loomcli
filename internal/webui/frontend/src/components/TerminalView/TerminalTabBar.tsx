/**
 * TerminalTabBar component.
 * Horizontal tab strip for switching between terminal sessions.
 * Follows WAI-ARIA tabs pattern with keyboard navigation.
 */

import { useCallback, useEffect, useRef } from "react";

import styles from "./TerminalTabBar.module.css";

export interface TerminalTab {
  id: string;
  label: string;
  connectionState: "disconnected" | "connecting" | "connected";
}

export interface TerminalTabBarProps {
  tabs: TerminalTab[];
  activeTabId: string;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onNewTab: () => void;
  onToggleFullHeight: () => void;
  isFullHeight: boolean;
}

export function TerminalTabBar({
  tabs,
  activeTabId,
  onTabChange,
  onTabClose,
  onNewTab,
  onToggleFullHeight,
  isFullHeight,
}: TerminalTabBarProps): JSX.Element {
  const tabRefs = useRef<Map<string, HTMLDivElement | null>>(new Map());
  const tabListRef = useRef<HTMLDivElement | null>(null);

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
              {tab.label}
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
