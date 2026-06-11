/**
 * Vertical drag handle for resizing a right-hand panel from its left edge.
 */

import { useCallback, useEffect, useRef } from "react";

import styles from "./PanelWidthResizeHandle.module.css";

export interface PanelWidthResizeHandleProps {
  width: number;
  onDelta: (deltaPx: number) => void;
  onReset?: (() => void) | undefined;
  minWidth: number;
  maxWidth: number;
}

export function PanelWidthResizeHandle({
  width,
  onDelta,
  onReset,
  minWidth,
  maxWidth,
}: PanelWidthResizeHandleProps): JSX.Element {
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
    };
  }, []);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      isDragging.current = true;
      lastX.current = e.clientX;
      document.body.style.userSelect = "none";
      document.body.style.cursor = "col-resize";

      const handlePointerMove = (ev: PointerEvent) => {
        if (!isDragging.current) return;
        const delta = lastX.current - ev.clientX;
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
      };

      activeListeners.current = { move: handlePointerMove, up: handlePointerUp };
      document.addEventListener("pointermove", handlePointerMove);
      document.addEventListener("pointerup", handlePointerUp);
    },
    [onDelta],
  );

  const handleDoubleClick = useCallback(() => {
    onReset?.();
  }, [onReset]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      switch (e.key) {
        case "ArrowLeft":
          e.preventDefault();
          onDelta(16);
          break;
        case "ArrowRight":
          e.preventDefault();
          onDelta(-16);
          break;
        case "Home":
          e.preventDefault();
          onDelta(maxWidth - width);
          break;
        case "End":
          e.preventDefault();
          onDelta(minWidth - width);
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
      aria-label="Resize Open Queue panel"
      tabIndex={0}
      data-testid="open-queue-resize-handle"
      onPointerDown={handlePointerDown}
      onDoubleClick={handleDoubleClick}
      onKeyDown={handleKeyDown}
    />
  );
}
