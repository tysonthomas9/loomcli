/**
 * TabContextMenu — right-click context menu for terminal tabs.
 * Provides Duplicate, Rename, Pin/Unpin, Close, Close Others, Close All actions.
 */

import { useCallback, useEffect, useRef } from "react";

import styles from "./TerminalTabBar.module.css";

export interface TabContextMenuProps {
  tabId: string;
  isPinned: boolean;
  x: number;
  y: number;
  tabCount: number;
  maxTabsReached?: boolean | undefined;
  onDuplicate?: (() => void) | undefined;
  onRename?: (() => void) | undefined;
  onPin?: (() => void) | undefined;
  onClose: () => void;
  onCloseOthers?: (() => void) | undefined;
  onCloseAll?: (() => void) | undefined;
  onDismiss: () => void;
}

export function TabContextMenu({
  isPinned,
  x,
  y,
  tabCount,
  maxTabsReached,
  onDuplicate,
  onRename,
  onPin,
  onClose,
  onCloseOthers,
  onCloseAll,
  onDismiss,
}: TabContextMenuProps) {
  const menuRef = useRef<HTMLDivElement | null>(null);

  // Close on outside click, Escape, or scroll
  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        onDismiss();
      }
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onDismiss();
    };
    const handleScroll = () => onDismiss();
    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKeyDown);
    window.addEventListener("scroll", handleScroll, true);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("scroll", handleScroll, true);
    };
  }, [onDismiss]);

  const handleDuplicate = useCallback(() => {
    onDuplicate?.();
    onDismiss();
  }, [onDuplicate, onDismiss]);

  const handleRename = useCallback(() => {
    onRename?.();
    onDismiss();
  }, [onRename, onDismiss]);

  const handlePin = useCallback(() => {
    onPin?.();
    onDismiss();
  }, [onPin, onDismiss]);

  const handleClose = useCallback(() => {
    onClose();
    onDismiss();
  }, [onClose, onDismiss]);

  const handleCloseOthers = useCallback(() => {
    onCloseOthers?.();
    onDismiss();
  }, [onCloseOthers, onDismiss]);

  const handleCloseAll = useCallback(() => {
    onCloseAll?.();
    onDismiss();
  }, [onCloseAll, onDismiss]);

  return (
    <div
      ref={menuRef}
      className={styles.contextMenu}
      style={{ left: x, top: y }}
      role="menu"
      data-testid="terminal-tab-context-menu"
    >
      {onDuplicate && (
        <button
          type="button"
          className={
            maxTabsReached
              ? `${styles.contextMenuItem} ${styles.contextMenuItemDisabled}`
              : styles.contextMenuItem
          }
          onClick={handleDuplicate}
          disabled={maxTabsReached}
          role="menuitem"
          data-testid="context-menu-duplicate"
          title={maxTabsReached ? "Maximum tabs reached" : undefined}
        >
          Duplicate
        </button>
      )}
      {onRename && (
        <button
          type="button"
          className={styles.contextMenuItem}
          onClick={handleRename}
          role="menuitem"
          data-testid="context-menu-rename"
        >
          Rename
        </button>
      )}
      {onPin && (
        <button
          type="button"
          className={styles.contextMenuItem}
          onClick={handlePin}
          role="menuitem"
          data-testid="context-menu-pin"
        >
          {isPinned ? "Unpin" : "Pin"}
        </button>
      )}
      {(onDuplicate || onRename || onPin) && tabCount > 1 && (
        <div className={styles.contextMenuDivider} />
      )}
      {tabCount > 1 && (
        <button
          type="button"
          className={styles.contextMenuItem}
          onClick={handleClose}
          role="menuitem"
          data-testid="context-menu-close"
        >
          Close
        </button>
      )}
      {onCloseOthers && tabCount > 1 && (
        <button
          type="button"
          className={styles.contextMenuItem}
          onClick={handleCloseOthers}
          role="menuitem"
          data-testid="context-menu-close-others"
        >
          Close Others
        </button>
      )}
      {onCloseAll && tabCount > 1 && (
        <button
          type="button"
          className={styles.contextMenuItem}
          onClick={handleCloseAll}
          role="menuitem"
          data-testid="context-menu-close-all"
        >
          Close All
        </button>
      )}
    </div>
  );
}
