/**
 * Vertical drag handle for resizing a right-hand panel from its left edge.
 */

import { ResizeHandle } from "@/components/ResizeHandle";

import styles from "./PanelWidthResizeHandle.module.css";

export interface PanelWidthResizeHandleProps {
  width: number;
  onDelta: (deltaPx: number) => void;
  onReset?: (() => void) | undefined;
  onDragStart?: (() => void) | undefined;
  onDragEnd?: (() => void) | undefined;
  minWidth: number;
  maxWidth: number;
}

export function PanelWidthResizeHandle(
  props: PanelWidthResizeHandleProps,
): JSX.Element {
  return (
    <ResizeHandle
      {...props}
      edge="left"
      ariaLabel="Resize Open Queue panel"
      testId="open-queue-resize-handle"
      className={styles.handle}
    />
  );
}
