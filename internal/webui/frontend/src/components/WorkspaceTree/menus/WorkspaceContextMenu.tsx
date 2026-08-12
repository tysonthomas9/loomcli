/**
 * WorkspaceContextMenu component.
 * A positioned popover with workspace actions (Rename).
 * Follows MoreFiltersMenu pattern for positioning and lifecycle.
 */

import { useCallback, type KeyboardEvent } from "react";

import styles from "./WorkspaceContextMenu.module.css";
import { useContextMenuLifecycle } from "./useContextMenuLifecycle";

export interface WorkspaceContextMenuProps {
  /** Whether the menu is open */
  isOpen: boolean;
  /** Absolute position of the menu */
  position: { x: number; y: number };
  /** Callback when Rename is selected */
  onRename: () => void;
  /** Callback when Remove is selected */
  onRemove: () => void;
  /** Callback to close the menu */
  onClose: () => void;
  /**
   * Whether to render the Remove action. Hidden for the active workspace,
   * which can't be removed from under the view it's currently showing.
   * Defaults to true.
   */
  showRemove?: boolean;
}

export function WorkspaceContextMenu({
  isOpen,
  position,
  onRename,
  onRemove,
  onClose,
  showRemove = true,
}: WorkspaceContextMenuProps): JSX.Element | null {
  const menuRef = useContextMenuLifecycle(isOpen, position, onClose);

  const handleRenameClick = useCallback(() => {
    onRename();
    onClose();
  }, [onRename, onClose]);

  const handleRemoveClick = useCallback(() => {
    onRemove();
    onClose();
  }, [onRemove, onClose]);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLButtonElement>) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        handleRenameClick();
      }
    },
    [handleRenameClick],
  );

  const handleRemoveKeyDown = useCallback(
    (e: KeyboardEvent<HTMLButtonElement>) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        handleRemoveClick();
      }
    },
    [handleRemoveClick],
  );

  if (!isOpen) return null;

  return (
    <div
      ref={menuRef}
      className={styles.menu}
      style={{ left: position.x, top: position.y }}
      role="menu"
      data-testid="workspace-context-menu"
    >
      <button
        type="button"
        className={styles.menuItem}
        onClick={handleRenameClick}
        onKeyDown={handleKeyDown}
        role="menuitem"
        aria-label="Rename workspace"
        title="Rename workspace"
        data-testid="workspace-context-menu-rename"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 14 14"
          fill="none"
          className={styles.menuItemIcon}
          aria-hidden="true"
        >
          <path
            d="M10.5 1.5L12.5 3.5L4 12H2V10L10.5 1.5Z"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span>Rename</span>
      </button>
      {showRemove && (
        <button
          type="button"
          className={`${styles.menuItem} ${styles.dangerItem}`}
          onClick={handleRemoveClick}
          onKeyDown={handleRemoveKeyDown}
          role="menuitem"
          aria-label="Remove workspace"
          title="Remove workspace"
          data-testid="workspace-context-menu-remove"
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 14 14"
            fill="none"
            className={styles.menuItemIcon}
            aria-hidden="true"
          >
            <path
              d="M2 3.5H12M5.5 6V10.5M8.5 6V10.5M3 3.5L3.5 11.5C3.5 12.05 3.95 12.5 4.5 12.5H9.5C10.05 12.5 10.5 12.05 10.5 11.5L11 3.5M5 3.5V2C5 1.45 5.45 1 6 1H8C8.55 1 9 1.45 9 2V3.5"
              stroke="currentColor"
              strokeWidth="1.3"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          <span>Remove</span>
        </button>
      )}
    </div>
  );
}
