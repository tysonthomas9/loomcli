/**
 * useContextMenuLifecycle — the open/close/positioning behavior every sidebar
 * context menu needs: close on an outside mousedown, close on Escape, and clamp
 * the menu inside the viewport once it has measured itself. Returns the ref to
 * attach to the menu element.
 */

import { useEffect, useLayoutEffect, useRef, type RefObject } from "react";

export interface ContextMenuPosition {
  x: number;
  y: number;
}

export function useContextMenuLifecycle(
  isOpen: boolean,
  position: ContextMenuPosition,
  onClose: () => void,
): RefObject<HTMLDivElement> {
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

    const el = menuRef.current;
    const rect = el.getBoundingClientRect();

    if (rect.right > window.innerWidth) {
      el.style.left = `${position.x - rect.width}px`;
    }
    if (rect.bottom > window.innerHeight) {
      el.style.top = `${position.y - rect.height}px`;
    }
  }, [isOpen, position]);

  return menuRef;
}
