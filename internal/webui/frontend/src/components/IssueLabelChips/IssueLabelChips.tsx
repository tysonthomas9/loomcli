import type { ReactNode } from "react";

import styles from "./IssueLabelChips.module.css";

const RECOMMENDED_LABEL = "recommended";

export interface IssueLabelChipsProps {
  labels?: readonly string[] | undefined;
  maxVisible?: number | undefined;
  recommendedAction?: ReactNode;
  className?: string | undefined;
}

function visibleLabels(labels: readonly string[]): string[] {
  const unique = Array.from(
    new Set(labels.filter((label) => label && !label.startsWith("repo:"))),
  );
  return unique.sort((left, right) => {
    if (left === RECOMMENDED_LABEL) return -1;
    if (right === RECOMMENDED_LABEL) return 1;
    return 0;
  });
}

export function IssueLabelChips({
  labels = [],
  maxVisible,
  recommendedAction,
  className,
}: IssueLabelChipsProps): JSX.Element | null {
  const filtered = visibleLabels(labels);
  if (filtered.length === 0) return null;

  const limit = Math.max(0, maxVisible ?? filtered.length);
  const shown = filtered.slice(0, limit);
  const hidden = filtered.slice(limit);

  return (
    <span
      className={[styles.chips, className].filter(Boolean).join(" ")}
      aria-label="Issue labels"
    >
      {shown.map((label) =>
        label === RECOMMENDED_LABEL ? (
          <span className={styles.recommendedGroup} key={label}>
            <span
              className={`${styles.chip} ${styles.recommended}`}
              data-label={label}
              data-variant="recommended"
            >
              Recommended
            </span>
            {recommendedAction}
          </span>
        ) : (
          <span
            className={styles.chip}
            data-label={label}
            data-variant="default"
            key={label}
          >
            {label}
          </span>
        ),
      )}
      {hidden.length > 0 ? (
        <span
          className={`${styles.chip} ${styles.overflow}`}
          aria-label={`${hidden.length} more ${hidden.length === 1 ? "label" : "labels"}`}
          title={hidden.join(", ")}
        >
          +{hidden.length}
        </span>
      ) : null}
    </span>
  );
}
