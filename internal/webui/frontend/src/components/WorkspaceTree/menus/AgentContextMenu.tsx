/**
 * AgentContextMenu — context menu for sidebar agent rows.
 * Actions: Archive (hard-delete via workspace agent DELETE).
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

export interface AgentContextMenuProps {
  isOpen: boolean;
  position: { x: number; y: number };
  onArchive: () => void;
  onClose: () => void;
}

export function AgentContextMenu({
  isOpen,
  position,
  onArchive,
  onClose,
}: AgentContextMenuProps): JSX.Element | null {
  const menuRef = useRef<HTMLDivElement>(null);

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
      data-testid="agent-context-menu"
    >
      <button
        type="button"
        className={`${menuStyles.menuItem} ${menuStyles.dangerItem}`}
        onClick={handleArchiveClick}
        onKeyDown={handleKeyDown(handleArchiveClick)}
        role="menuitem"
        data-testid="agent-context-menu-archive"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          className={menuStyles.menuItemIcon}
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <rect width="20" height="5" x="2" y="3" rx="1" />
          <path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" />
          <path d="M10 12h4" />
        </svg>
        Archive
      </button>
    </div>
  );
}
