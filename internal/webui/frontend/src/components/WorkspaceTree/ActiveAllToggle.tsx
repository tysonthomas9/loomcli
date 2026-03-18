/**
 * ActiveAllToggle is a two-segment toggle for filtering tasks by active/all.
 * Uses a segmented-control pattern with role="radiogroup" for accessibility.
 */

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
  return (
    <div className={styles.toggle} role="radiogroup" aria-label="Task filter">
      <button
        type="button"
        className={`${styles.segment} ${value === "active" ? styles.active : ""}`}
        role="radio"
        aria-checked={value === "active"}
        onClick={() => onChange("active")}
      >
        Active
      </button>
      <button
        type="button"
        className={`${styles.segment} ${value === "all" ? styles.active : ""}`}
        role="radio"
        aria-checked={value === "all"}
        onClick={() => onChange("all")}
      >
        All
      </button>
    </div>
  );
}
