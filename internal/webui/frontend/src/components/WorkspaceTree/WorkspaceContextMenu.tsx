/**
 * WorkspaceContextMenu component.
 * A positioned popover with workspace actions (Rename).
 * Follows MoreFiltersMenu pattern for positioning and lifecycle.
 */

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  type KeyboardEvent,
} from "react";

import styles from "./WorkspaceContextMenu.module.css";

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
  /** Whether the target workspace is the default */
  isDefault?: boolean;
  /** Callback when Set as default is selected */
  onSetDefault?: () => void;
  /** Callback when Clear default is selected */
  onClearDefault?: () => void;
}

export function WorkspaceContextMenu({
  isOpen,
  position,
  onRename,
  onRemove,
  onClose,
  isDefault,
  onSetDefault,
  onClearDefault,
}: WorkspaceContextMenuProps): JSX.Element | null {
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

  // Clamp menu to viewport edges (useLayoutEffect to avoid flicker)
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

  const handleDefaultClick = useCallback(() => {
    if (isDefault) {
      onClearDefault?.();
    } else {
      onSetDefault?.();
    }
    onClose();
  }, [isDefault, onSetDefault, onClearDefault, onClose]);

  const handleDefaultKeyDown = useCallback(
    (e: KeyboardEvent<HTMLButtonElement>) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        handleDefaultClick();
      }
    },
    [handleDefaultClick],
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
      {(onSetDefault || onClearDefault) && (
        <button
          type="button"
          className={styles.menuItem}
          onClick={handleDefaultClick}
          onKeyDown={handleDefaultKeyDown}
          role="menuitem"
          aria-label={
            isDefault ? "Clear default workspace" : "Set as default workspace"
          }
          title={
            isDefault ? "Clear default workspace" : "Set as default workspace"
          }
          data-testid="workspace-context-menu-default"
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
              d="M7 1L8.76 4.56L12.73 5.14L9.87 7.94L10.52 11.89L7 10.04L3.48 11.89L4.13 7.94L1.27 5.14L5.24 4.56L7 1Z"
              stroke="currentColor"
              strokeWidth="1.3"
              strokeLinecap="round"
              strokeLinejoin="round"
              fill={isDefault ? "currentColor" : "none"}
            />
          </svg>
          <span>{isDefault ? "Clear default" : "Set as default"}</span>
        </button>
      )}
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
    </div>
  );
}
