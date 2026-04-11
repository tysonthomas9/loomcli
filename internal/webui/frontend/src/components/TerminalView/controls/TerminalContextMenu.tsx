/**
 * TerminalContextMenu component.
 * Right-click context menu for terminal with Copy, Paste, Select All actions.
 * Renders via createPortal to document.body to avoid overflow clipping.
 */

import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";

import styles from "./TerminalContextMenu.module.css";

const isMac =
  typeof navigator !== "undefined" &&
  (/Mac/.test(navigator.platform) || /Mac/.test(navigator.userAgent));

const MOD = isMac ? "\u2318" : "Ctrl+";

export interface TerminalContextMenuProps {
  x: number;
  y: number;
  hasSelection: boolean;
  onCopy: () => void;
  onPaste: () => void;
  onSelectAll: () => void;
  onClose: () => void;
}

export function TerminalContextMenu({
  x,
  y,
  hasSelection,
  onCopy,
  onPaste,
  onSelectAll,
  onClose,
}: TerminalContextMenuProps): JSX.Element {
  const menuRef = useRef<HTMLDivElement>(null);

  // Dismiss on click outside, Escape, scroll, resize
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    };

    const handleClickOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        onClose();
      }
    };

    const handleDismiss = () => onClose();

    document.addEventListener("keydown", handleKeyDown, { capture: true });
    document.addEventListener("mousedown", handleClickOutside);
    window.addEventListener("scroll", handleDismiss, { capture: true });
    window.addEventListener("resize", handleDismiss);

    return () => {
      document.removeEventListener("keydown", handleKeyDown, { capture: true });
      document.removeEventListener("mousedown", handleClickOutside);
      window.removeEventListener("scroll", handleDismiss, { capture: true });
      window.removeEventListener("resize", handleDismiss);
    };
  }, [onClose]);

  // Clamp position to viewport
  const clampedX = Math.min(x, window.innerWidth - 200);
  const clampedY = Math.min(y, window.innerHeight - 120);

  return createPortal(
    <div
      ref={menuRef}
      className={styles.menu}
      style={{ left: clampedX, top: clampedY }}
      role="menu"
    >
      <button
        className={styles.item}
        data-disabled={!hasSelection}
        onClick={onCopy}
        role="menuitem"
        tabIndex={hasSelection ? 0 : -1}
      >
        Copy
        <span className={styles.shortcut}>{MOD}C</span>
      </button>
      <button
        className={styles.item}
        onClick={onPaste}
        role="menuitem"
        tabIndex={0}
      >
        Paste
        <span className={styles.shortcut}>{MOD}V</span>
      </button>
      <button
        className={styles.item}
        onClick={onSelectAll}
        role="menuitem"
        tabIndex={0}
      >
        Select All
        <span className={styles.shortcut}>{MOD}A</span>
      </button>
    </div>,
    document.body,
  );
}
