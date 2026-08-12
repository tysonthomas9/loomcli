/**
 * AgentContextMenu — context menu for sidebar agent rows.
 * Actions: Archive (hard-delete via workspace agent DELETE).
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
  onClose: () => void;
}

export function AgentContextMenu({
  isOpen,
  position,
  onArchive,
  onClose,
}: AgentContextMenuProps): JSX.Element | null {
  const menuRef = useContextMenuLifecycle(isOpen, position, onClose);

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
        <ArchiveIcon className={menuStyles.menuItemIcon} />
        Archive
      </button>
    </div>
  );
}
