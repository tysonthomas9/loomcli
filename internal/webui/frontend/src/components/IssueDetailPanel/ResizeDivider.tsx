/**
 * ResizeDivider component.
 * Draggable horizontal divider between details and terminal sections.
 * Uses pointer events with setPointerCapture for cross-browser support.
 */

import { useRef, useCallback, useEffect } from "react";

import styles from "./ResizeDivider.module.css";

export interface ResizeDividerProps {
  onDragDelta: (deltaY: number) => void;
  onDoubleClick: () => void;
  ratio: number;
}

export function ResizeDivider({
  onDragDelta,
  onDoubleClick,
  ratio,
}: ResizeDividerProps): JSX.Element {
  const isDraggingRef = useRef(false);
  const lastYRef = useRef(0);
  const rafRef = useRef(0);
  const dividerRef = useRef<HTMLDivElement>(null);

  // Cancel any pending RAF on unmount to avoid state updates after unmount
  useEffect(() => {
    return () => {
      cancelAnimationFrame(rafRef.current);
    };
  }, []);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      isDraggingRef.current = true;
      lastYRef.current = e.clientY;
      e.currentTarget.setPointerCapture(e.pointerId);
      dividerRef.current?.setAttribute("data-dragging", "true");
    },
    [],
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!isDraggingRef.current) return;
      const currentY = e.clientY;
      const lastY = lastYRef.current;
      lastYRef.current = currentY;
      cancelAnimationFrame(rafRef.current);
      rafRef.current = requestAnimationFrame(() => {
        onDragDelta(currentY - lastY);
      });
    },
    [onDragDelta],
  );

  const handlePointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!isDraggingRef.current) return;
      isDraggingRef.current = false;
      e.currentTarget.releasePointerCapture(e.pointerId);
      dividerRef.current?.setAttribute("data-dragging", "false");
      cancelAnimationFrame(rafRef.current);
    },
    [],
  );

  const handleLostCapture = useCallback(() => {
    if (!isDraggingRef.current) return;
    isDraggingRef.current = false;
    dividerRef.current?.setAttribute("data-dragging", "false");
    cancelAnimationFrame(rafRef.current);
  }, []);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      switch (e.key) {
        case "ArrowUp":
          e.preventDefault();
          onDragDelta(-20);
          break;
        case "ArrowDown":
          e.preventDefault();
          onDragDelta(20);
          break;
        case "Home":
          e.preventDefault();
          onDragDelta(-9999);
          break;
        case "End":
          e.preventDefault();
          onDragDelta(9999);
          break;
      }
    },
    [onDragDelta],
  );

  return (
    <div
      ref={dividerRef}
      className={styles.divider}
      role="separator"
      aria-orientation="horizontal"
      aria-valuenow={Math.round(ratio * 100)}
      aria-valuemin={15}
      aria-valuemax={85}
      tabIndex={0}
      data-dragging="false"
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onLostPointerCapture={handleLostCapture}
      onDoubleClick={onDoubleClick}
      onKeyDown={handleKeyDown}
    >
      <div className={styles.dividerBar} />
    </div>
  );
}
