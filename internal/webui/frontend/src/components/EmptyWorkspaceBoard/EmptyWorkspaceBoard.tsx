/**
 * EmptyWorkspaceBoard component for board-level empty states.
 * Shown when a workspace has zero issues (or filters reduce to zero).
 * Reactively disappears when data arrives via SSE.
 *
 * The OnboardingFlow lives at the page level (KanbanPage) instead of
 * inside this component. That placement keeps the checklist visible
 * across the empty-board → has-issues transition so the final
 * "run first agent" step stays reachable after the first issue lands.
 */

import styles from "./EmptyWorkspaceBoard.module.css";

export interface EmptyWorkspaceBoardProps {
  isMultiRepo?: boolean;
  hasFiltersActive?: boolean;
}

function getContent(isMultiRepo: boolean, hasFiltersActive: boolean) {
  if (hasFiltersActive) {
    return {
      headline: "No issues match your filters",
      subtitle: "Try adjusting or clearing your filters to see issues",
    };
  }
  if (isMultiRepo) {
    return {
      headline: "No issues in this workspace",
      subtitle:
        "Create your first issue with New issue or import from your tracker",
    };
  }
  return {
    headline: "No issues yet",
    subtitle:
      "Create your first issue with New issue or import from your tracker",
  };
}

export function EmptyWorkspaceBoard({
  isMultiRepo = false,
  hasFiltersActive = false,
}: EmptyWorkspaceBoardProps): JSX.Element {
  const { headline, subtitle } = getContent(isMultiRepo, hasFiltersActive);

  return (
    <div
      className={styles.container}
      role="status"
      aria-label={headline}
      data-testid="empty-workspace-board"
    >
      <div className={styles.icon} aria-hidden="true">
        <svg
          width="48"
          height="48"
          viewBox="0 0 48 48"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          {/* Kanban board outline */}
          <rect x="4" y="6" width="40" height="36" rx="3" />
          <line x1="17.5" y1="6" x2="17.5" y2="42" />
          <line x1="30.5" y1="6" x2="30.5" y2="42" />
          {/* Card placeholders */}
          <rect x="8" y="12" width="6" height="4" rx="1" opacity="0.4" />
          <rect x="21" y="12" width="6" height="4" rx="1" opacity="0.4" />
          <rect x="34" y="12" width="6" height="4" rx="1" opacity="0.4" />
        </svg>
      </div>
      <h3 className={styles.headline}>{headline}</h3>
      <p className={styles.subtitle}>{subtitle}</p>
    </div>
  );
}
