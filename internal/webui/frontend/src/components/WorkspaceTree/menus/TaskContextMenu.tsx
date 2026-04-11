/**
 * TaskContextMenu — context menu for task rows.
 * Actions: Rename, Mark as Done, Archive.
 * Follows WorkspaceContextMenu pattern for positioning and lifecycle.
 */

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  type KeyboardEvent,
} from "react";

import menuStyles from "./WorkspaceContextMenu.module.css";

export interface TaskContextMenuProps {
  isOpen: boolean;
  position: { x: number; y: number };
  onRename: () => void;
  onMarkDone: () => void;
  onArchive: () => void;
  onClose: () => void;
}

export function TaskContextMenu({
  isOpen,
  position,
  onRename,
  onMarkDone,
  onArchive,
  onClose,
}: TaskContextMenuProps): JSX.Element | null {
  const menuRef = useRef<HTMLDivElement>(null);

  // Close on outside click
  useEffect(() => {
    if (!isOpen) return;

    function handleClickOutside(event: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose();
      }
    }

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen, onClose]);

  // Close on Escape
  useEffect(() => {
    if (!isOpen) return;

    function handleKeyDown(event: globalThis.KeyboardEvent) {
      if (event.key === "Escape") {
        onClose();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  // Clamp menu to viewport edges
  useLayoutEffect(() => {
    if (!isOpen || !menuRef.current) return;

    const rect = menuRef.current.getBoundingClientRect();
    const el = menuRef.current;

    if (rect.right > window.innerWidth) {
      el.style.left = `${position.x - rect.width}px`;
    }
    if (rect.bottom > window.innerHeight) {
      el.style.top = `${position.y - rect.height}px`;
    }
  }, [isOpen, position]);

  const handleRenameClick = useCallback(() => {
    onRename();
    onClose();
  }, [onRename, onClose]);

  const handleMarkDoneClick = useCallback(() => {
    onMarkDone();
    onClose();
  }, [onMarkDone, onClose]);

  const handleArchiveClick = useCallback(() => {
    onArchive();
    onClose();
  }, [onArchive, onClose]);

  const handleKeyDown = useCallback(
    (action: () => void) => (e: KeyboardEvent<HTMLButtonElement>) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        action();
      }
    },
    [],
  );

  if (!isOpen) return null;

  return (
    <div
      ref={menuRef}
      className={menuStyles.menu}
      style={{ left: position.x, top: position.y }}
      role="menu"
      data-testid="task-context-menu"
    >
      <button
        type="button"
        className={menuStyles.menuItem}
        onClick={handleRenameClick}
        onKeyDown={handleKeyDown(handleRenameClick)}
        role="menuitem"
        data-testid="task-context-menu-rename"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 14 14"
          fill="none"
          className={menuStyles.menuItemIcon}
        >
          <path
            d="M10.5 1.5L12.5 3.5L4 12H2V10L10.5 1.5Z"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        Rename
      </button>
      <button
        type="button"
        className={menuStyles.menuItem}
        onClick={handleMarkDoneClick}
        onKeyDown={handleKeyDown(handleMarkDoneClick)}
        role="menuitem"
        data-testid="task-context-menu-done"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 14 14"
          fill="none"
          className={menuStyles.menuItemIcon}
        >
          <path
            d="M3 7L6 10L11 4"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        Mark as Done
      </button>
      <button
        type="button"
        className={`${menuStyles.menuItem} ${menuStyles.dangerItem}`}
        onClick={handleArchiveClick}
        onKeyDown={handleKeyDown(handleArchiveClick)}
        role="menuitem"
        data-testid="task-context-menu-archive"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 14 14"
          fill="none"
          className={menuStyles.menuItemIcon}
        >
          <path
            d="M2 3.5H12M5.5 6V10.5M8.5 6V10.5M3 3.5L3.5 11.5C3.5 12.05 3.95 12.5 4.5 12.5H9.5C10.05 12.5 10.5 12.05 10.5 11.5L11 3.5M5 3.5V2C5 1.45 5.45 1 6 1H8C8.55 1 9 1.45 9 2V3.5"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        Archive
      </button>
    </div>
  );
}
