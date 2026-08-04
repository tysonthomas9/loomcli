/**
 * SplitDivider component.
 * Draggable vertical divider for resizing split terminal panes.
 * Uses pointer events for cross-browser drag support.
 */

import { useCallback, useEffect, useRef } from "react";

import {
  MIN_SPLIT_RATIO,
  MAX_SPLIT_RATIO,
  DEFAULT_SPLIT_RATIO,
} from "@/components/TerminalView/tabs";
import styles from "./SplitDivider.module.css";

interface SplitDividerProps {
  onRatioChange: (ratio: number) => void;
  containerRef: React.RefObject<HTMLDivElement | null>;
}

export function SplitDivider({
  onRatioChange,
  containerRef,
}: SplitDividerProps): JSX.Element {
  const isDragging = useRef(false);
  const dividerRef = useRef<HTMLDivElement>(null);
  const activeListenersRef = useRef<{
    move: (e: PointerEvent) => void;
    up: () => void;
  } | null>(null);

  // Clean up document listeners on unmount (prevents leak if unmounted mid-drag)
  useEffect(() => {
    return () => {
      if (activeListenersRef.current) {
        document.removeEventListener(
          "pointermove",
          activeListenersRef.current.move,
        );
        document.removeEventListener(
          "pointerup",
          activeListenersRef.current.up,
        );
        document.body.style.userSelect = "";
        document.body.style.cursor = "";
        activeListenersRef.current = null;
      }
    };
  }, []);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      isDragging.current = true;
      const target = e.currentTarget as HTMLElement;
      target.setPointerCapture(e.pointerId);
      document.body.style.userSelect = "none";
      document.body.style.cursor = "col-resize";

      const handlePointerMove = (ev: PointerEvent) => {
        if (!isDragging.current || !containerRef.current) return;
        const rect = containerRef.current.getBoundingClientRect();
        const x = ev.clientX - rect.left;
        const ratio = Math.min(
          MAX_SPLIT_RATIO,
          Math.max(MIN_SPLIT_RATIO, x / rect.width),
        );
        onRatioChange(ratio);
      };

      const handlePointerUp = () => {
        isDragging.current = false;
        document.body.style.userSelect = "";
        document.body.style.cursor = "";
        document.removeEventListener("pointermove", handlePointerMove);
        document.removeEventListener("pointerup", handlePointerUp);
        activeListenersRef.current = null;
      };

      activeListenersRef.current = {
        move: handlePointerMove,
        up: handlePointerUp,
      };
      document.addEventListener("pointermove", handlePointerMove);
      document.addEventListener("pointerup", handlePointerUp);
    },
    [onRatioChange, containerRef],
  );

  const handleDoubleClick = useCallback(() => {
    onRatioChange(DEFAULT_SPLIT_RATIO);
  }, [onRatioChange]);

  return (
    <div
      ref={dividerRef}
      className={styles.divider}
      onPointerDown={handlePointerDown}
      onDoubleClick={handleDoubleClick}
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize split panes"
      tabIndex={0}
      data-testid="split-divider"
    />
  );
}
