/**
 * Vertical drag handle on the right edge of the WorkspaceTree sidebar.
 */

import { ResizeHandle } from "@/components/ResizeHandle";

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

export function SidebarResizeHandle(
  props: SidebarResizeHandleProps,
): JSX.Element {
  return (
    <ResizeHandle
      {...props}
      edge="right"
      ariaLabel="Resize workspace sidebar"
      testId="workspace-tree-resize-handle"
      className={styles.handle}
    />
  );
}
