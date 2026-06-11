/**
 * Vertical drag handle on the right edge of the WorkspaceTree sidebar.
 */

import { useCallback, useEffect, useRef } from "react";

import styles from "./SidebarResizeHandle.module.css";

export interface SidebarResizeHandleProps {
  width: number;
  onDelta: (deltaPx: number) => void;
  onReset?: (() => void) | undefined;
  onDragStart?: (() => void) | undefined;
  onDragEnd?: (() => void) | undefined;
  minWidth: number;
  maxWidth: number;
}

export function SidebarResizeHandle({
  width,
  onDelta,
  onReset,
  onDragStart,
  onDragEnd,
  minWidth,
  maxWidth,
}: SidebarResizeHandleProps): JSX.Element {
  const isDragging = useRef(false);
  const lastX = useRef(0);
  const activeListeners = useRef<{
    move: (ev: PointerEvent) => void;
    up: (ev: PointerEvent) => void;
  } | null>(null);

  useEffect(() => {
    return () => {
      const listeners = activeListeners.current;
      if (!listeners) return;
      document.removeEventListener("pointermove", listeners.move);
      document.removeEventListener("pointerup", listeners.up);
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
      onDragEnd?.();
    };
  }, [onDragEnd]);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      isDragging.current = true;
      lastX.current = e.clientX;
      onDragStart?.();
      document.body.style.userSelect = "none";
      document.body.style.cursor = "col-resize";

      const handlePointerMove = (ev: PointerEvent) => {
        if (!isDragging.current) return;
        const delta = ev.clientX - lastX.current;
        lastX.current = ev.clientX;
        if (delta !== 0) onDelta(delta);
      };

      const handlePointerUp = () => {
        isDragging.current = false;
        document.body.style.userSelect = "";
        document.body.style.cursor = "";
        document.removeEventListener("pointermove", handlePointerMove);
        document.removeEventListener("pointerup", handlePointerUp);
        activeListeners.current = null;
        onDragEnd?.();
      };

      activeListeners.current = { move: handlePointerMove, up: handlePointerUp };
      document.addEventListener("pointermove", handlePointerMove);
      document.addEventListener("pointerup", handlePointerUp);
    },
    [onDelta, onDragEnd, onDragStart],
  );

  const handleDoubleClick = useCallback(() => {
    onReset?.();
  }, [onReset]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      switch (e.key) {
        case "ArrowRight":
          e.preventDefault();
          onDelta(16);
          break;
        case "ArrowLeft":
          e.preventDefault();
          onDelta(-16);
          break;
        case "Home":
          e.preventDefault();
          onDelta(minWidth - width);
          break;
        case "End":
          e.preventDefault();
          onDelta(maxWidth - width);
          break;
      }
    },
    [maxWidth, minWidth, onDelta, width],
  );

  return (
    <div
      className={styles.handle}
      role="separator"
      aria-orientation="vertical"
      aria-valuenow={width}
      aria-valuemin={minWidth}
      aria-valuemax={maxWidth}
      aria-label="Resize workspace sidebar"
      tabIndex={0}
      data-testid="workspace-tree-resize-handle"
      onPointerDown={handlePointerDown}
      onDoubleClick={handleDoubleClick}
      onKeyDown={handleKeyDown}
    />
  );
}
