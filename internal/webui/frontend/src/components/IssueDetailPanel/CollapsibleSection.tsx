import { useState } from "react";

import styles from "./IssueDetailPanel.module.css";

/**
 * Props for CollapsibleSection.
 */
export interface CollapsibleSectionProps {
  title: string;
  count?: number;
  defaultExpanded?: boolean;
  children: React.ReactNode;
  testId?: string;
}

/**
 * Collapsible section with chevron indicator.
 */
export function CollapsibleSection({
  title,
  count,
  defaultExpanded = true,
  children,
  testId,
}: CollapsibleSectionProps): JSX.Element {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded);

  return (
    <section className={styles.collapsibleSection} data-testid={testId}>
      <button
        type="button"
        className={styles.collapsibleHeader}
        onClick={() => setIsExpanded(!isExpanded)}
        aria-expanded={isExpanded}
      >
        <span className={styles.collapsibleTitle}>
          {title}
          {count !== undefined && (
            <span className={styles.collapsibleCount}>({count})</span>
          )}
        </span>
        <svg
          className={`${styles.chevron} ${isExpanded ? styles.chevronExpanded : ""}`}
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M6 4l4 4-4 4"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>
      {isExpanded && (
        <div className={styles.collapsibleContent}>{children}</div>
      )}
    </section>
  );
}
