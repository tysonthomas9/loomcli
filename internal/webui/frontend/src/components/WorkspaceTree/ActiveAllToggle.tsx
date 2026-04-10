/**
 * ActiveAllToggle is a two-segment toggle for filtering tasks by active/all.
 * Uses a segmented-control pattern with role="radiogroup" for accessibility.
 */

import { useRef, type KeyboardEvent } from "react";

import styles from "./ActiveAllToggle.module.css";

export type ActiveFilter = "active" | "all";

export interface ActiveAllToggleProps {
  value: ActiveFilter;
  onChange: (value: ActiveFilter) => void;
}

export function ActiveAllToggle({
  value,
  onChange,
}: ActiveAllToggleProps): JSX.Element {
  const activeRef = useRef<HTMLSpanElement>(null);
  const allRef = useRef<HTMLSpanElement>(null);

  function handleKeyDown(next: ActiveFilter) {
    return (e: KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onChange(next);
      } else if (
        e.key === "ArrowRight" ||
        e.key === "ArrowDown" ||
        e.key === "ArrowLeft" ||
        e.key === "ArrowUp"
      ) {
        e.preventDefault();
        const other: ActiveFilter = next === "active" ? "all" : "active";
        onChange(other);
        (other === "active" ? activeRef : allRef).current?.focus();
      }
    };
  }

  return (
    <span className={styles.toggle} role="radiogroup" aria-label="Task filter">
      <span
        ref={activeRef}
        className={`${styles.segment} ${value === "active" ? styles.active : ""}`}
        role="radio"
        aria-checked={value === "active"}
        tabIndex={value === "active" ? 0 : -1}
        onClick={() => onChange("active")}
        onKeyDown={handleKeyDown("active")}
      >
        Active
      </span>
      <span
        ref={allRef}
        className={`${styles.segment} ${value === "all" ? styles.active : ""}`}
        role="radio"
        aria-checked={value === "all"}
        tabIndex={value === "all" ? 0 : -1}
        onClick={() => onChange("all")}
        onKeyDown={handleKeyDown("all")}
      >
        All
      </span>
    </span>
  );
}
