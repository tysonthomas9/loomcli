/**
 * AgentContextMenu — context menu for sidebar agent rows.
 * Actions: Archive (hard-delete via workspace agent DELETE), and Delete
 * where the caller offers it (Agent API rows).
 * Follows WorkspaceContextMenu pattern for positioning and lifecycle.
 */

import { useCallback, type KeyboardEvent } from "react";

import { ArchiveIcon } from "../ArchiveIcon";
import menuStyles from "./WorkspaceContextMenu.module.css";
import {
  useContextMenuLifecycle,
  type ContextMenuPosition,
} from "./useContextMenuLifecycle";

export interface AgentContextMenuProps {
  isOpen: boolean;
  position: ContextMenuPosition;
  onArchive: () => void;
  /** Shows Delete; the caller confirms before deleting. */
  onDelete?: (() => void) | undefined;
  onClose: () => void;
}

export function AgentContextMenu({
  isOpen,
  position,
  onArchive,
  onDelete,
  onClose,
}: AgentContextMenuProps): JSX.Element | null {
  const menuRef = useContextMenuLifecycle(isOpen, position, onClose);

  const handleArchiveClick = useCallback(() => {
    onArchive();
    onClose();
  }, [onArchive, onClose]);

  const handleDeleteClick = useCallback(() => {
    onDelete?.();
    onClose();
  }, [onDelete, onClose]);

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
        <ArchiveIcon className={menuStyles.menuItemIcon} />
        Archive
      </button>
      {onDelete && (
        <button
          type="button"
          className={`${menuStyles.menuItem} ${menuStyles.dangerItem}`}
          onClick={handleDeleteClick}
          onKeyDown={handleKeyDown(handleDeleteClick)}
          role="menuitem"
          data-testid="agent-context-menu-delete"
        >
          Delete
        </button>
      )}
    </div>
  );
}
