/**
 * EmptyState component for first-run and empty data experiences.
 * Provides context-specific guidance for workspaces, issues, and agents.
 */

import styles from "./EmptyState.module.css";

export type EmptyStateVariant = "no-workspaces" | "no-issues" | "no-agents";

export interface EmptyStateProps {
  variant: EmptyStateVariant;
  className?: string;
}

interface VariantContent {
  icon: JSX.Element;
  title: string;
  description: JSX.Element;
}

function getVariantContent(variant: EmptyStateVariant): VariantContent {
  switch (variant) {
    case "no-workspaces":
      return {
        icon: (
          <svg
            width="40"
            height="40"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
          </svg>
        ),
        title: "No workspaces configured",
        description: (
          <>
            Add workspaces to your{" "}
            <code className={styles.code}>loom.yaml</code> config to manage
            multiple repositories. Run{" "}
            <code className={styles.code}>loom init</code> to get started.
          </>
        ),
      };
    case "no-issues":
      return {
        icon: (
          <svg
            width="40"
            height="40"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2" />
            <rect x="9" y="3" width="6" height="4" rx="1" />
          </svg>
        ),
        title: "No issues yet",
        description: (
          <>
            Create your first issue with the New issue action. Issues will
            appear here as you add them. Use{" "}
            <code className={styles.code}>loom data ready</code> to see the
            board populate.
          </>
        ),
      };
    case "no-agents":
      return {
        icon: (
          <svg
            width="40"
            height="40"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <rect x="4" y="4" width="16" height="16" rx="2" />
            <line x1="8" y1="2" x2="8" y2="4" />
            <line x1="16" y1="2" x2="16" y2="4" />
            <line x1="8" y1="20" x2="8" y2="22" />
            <line x1="16" y1="20" x2="16" y2="22" />
            <circle cx="9" cy="10" r="1.5" />
            <circle cx="15" cy="10" r="1.5" />
            <path d="M9 15h6" />
          </svg>
        ),
        title: "No agents running",
        description: (
          <>
            Agents will appear here when launched. Start an agent with{" "}
            <code className={styles.code}>loom spawn</code> or use the terminal
            to interact directly.
          </>
        ),
      };
  }
}

export function EmptyState({
  variant,
  className,
}: EmptyStateProps): JSX.Element {
  const { icon, title, description } = getVariantContent(variant);

  const rootClassName = className
    ? `${styles.emptyState} ${className}`
    : styles.emptyState;

  return (
    <div
      className={rootClassName}
      role="status"
      aria-label={title}
      data-testid="empty-state"
      data-variant={variant}
    >
      <div className={styles.iconWrapper}>{icon}</div>
      <h3 className={styles.title}>{title}</h3>
      <p className={styles.description}>{description}</p>
    </div>
  );
}
