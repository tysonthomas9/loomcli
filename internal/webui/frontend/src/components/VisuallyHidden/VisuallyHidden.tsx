/**
 * VisuallyHidden component.
 * Renders content visible only to screen readers using the clip pattern.
 */

import type { ReactNode } from "react";

import styles from "./VisuallyHidden.module.css";

export interface VisuallyHiddenProps {
  children: ReactNode;
  /** Render as a different element (defaults to span) */
  as?: "span" | "div";
}

export function VisuallyHidden({
  children,
  as: Tag = "span",
}: VisuallyHiddenProps): JSX.Element {
  return <Tag className={styles.visuallyHidden}>{children}</Tag>;
}
