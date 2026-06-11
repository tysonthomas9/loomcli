/**
 * Shared vertical drag handle for resizing a panel from one of its edges.
 *
 * `edge` is the side of the panel the handle sits on, which determines the
 * grow direction: a right-edge handle (left sidebar) grows the panel as the
 * pointer moves right; a left-edge handle (right panel) grows it as the
 * pointer moves left. Keyboard arrows follow the same spatial mapping, and
 * Home/End jump to the spatially leftmost/rightmost extent.
 */

import { useCallback, useEffect, useRef } from "react";

export interface ResizeHandleProps {
  width: number;
  minWidth: number;
  maxWidth: number;
  /** Which edge of the resized panel this handle sits on. */
  edge: "left" | "right";
  onDelta: (deltaPx: number) => void;
  onReset?: (() => void) | undefined;
  onDragStart?: (() => void) | undefined;
  onDragEnd?: (() => void) | undefined;
  ariaLabel: string;
  testId: string;
  className: string | undefined;
}

export function ResizeHandle({
  width,
  minWidth,
  maxWidth,
  edge,
  onDelta,
  onReset,
  onDragStart,
  onDragEnd,
  ariaLabel,
  testId,
  className,
}: ResizeHandleProps): JSX.Element {
  const isDragging = useRef(false);
  const lastX = useRef(0);
  const activeListeners = useRef<{
    move: (ev: PointerEvent) => void;
    up: (ev: PointerEvent) => void;
  } | null>(null);
  // Callbacks live in a ref so an identity change mid-drag (e.g. the parent
  // re-rendering from its own onDragStart state) can't tear down the active
  // document listeners.
  const callbacksRef = useRef({ onDelta, onDragStart, onDragEnd });
  callbacksRef.current = { onDelta, onDragStart, onDragEnd };

  useEffect(() => {
    return () => {
      const listeners = activeListeners.current;
      if (!listeners) return;
      activeListeners.current = null;
      document.removeEventListener("pointermove", listeners.move);
      document.removeEventListener("pointerup", listeners.up);
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
      callbacksRef.current.onDragEnd?.();
    };
  }, []);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      isDragging.current = true;
      lastX.current = e.clientX;
      callbacksRef.current.onDragStart?.();
      document.body.style.userSelect = "none";
      document.body.style.cursor = "col-resize";

      const handlePointerMove = (ev: PointerEvent) => {
        if (!isDragging.current) return;
        const moved = ev.clientX - lastX.current;
        lastX.current = ev.clientX;
        const delta = edge === "right" ? moved : -moved;
        if (delta !== 0) callbacksRef.current.onDelta(delta);
      };

      const handlePointerUp = () => {
        isDragging.current = false;
        document.body.style.userSelect = "";
        document.body.style.cursor = "";
        document.removeEventListener("pointermove", handlePointerMove);
        document.removeEventListener("pointerup", handlePointerUp);
        activeListeners.current = null;
        callbacksRef.current.onDragEnd?.();
      };

      activeListeners.current = {
        move: handlePointerMove,
        up: handlePointerUp,
      };
      document.addEventListener("pointermove", handlePointerMove);
      document.addEventListener("pointerup", handlePointerUp);
    },
    [edge],
  );

  const handleDoubleClick = useCallback(() => {
    onReset?.();
  }, [onReset]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      const grow = edge === "right" ? "ArrowRight" : "ArrowLeft";
      const shrink = edge === "right" ? "ArrowLeft" : "ArrowRight";
      switch (e.key) {
        case grow:
          e.preventDefault();
          onDelta(16);
          break;
        case shrink:
          e.preventDefault();
          onDelta(-16);
          break;
        case "Home":
          e.preventDefault();
          onDelta((edge === "right" ? minWidth : maxWidth) - width);
          break;
        case "End":
          e.preventDefault();
          onDelta((edge === "right" ? maxWidth : minWidth) - width);
          break;
      }
    },
    [edge, maxWidth, minWidth, onDelta, width],
  );

  return (
    <div
      className={className}
      role="separator"
      aria-orientation="vertical"
      aria-valuenow={width}
      aria-valuemin={minWidth}
      aria-valuemax={maxWidth}
      aria-label={ariaLabel}
      tabIndex={0}
      data-testid={testId}
      onPointerDown={handlePointerDown}
      onDoubleClick={handleDoubleClick}
      onKeyDown={handleKeyDown}
    />
  );
}
