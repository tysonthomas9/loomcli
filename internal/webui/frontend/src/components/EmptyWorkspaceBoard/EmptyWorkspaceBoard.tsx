/**
 * EmptyWorkspaceBoard component for board-level empty states.
 * Shown when a workspace has zero issues (or filters reduce to zero).
 * Reactively disappears when data arrives via SSE.
 *
 * For true empty states (no filters active) the component renders an
 * inline OnboardingFlow below the existing visuals so first-time users
 * see the full setup checklist. The legacy heading and subtitle remain
 * unchanged so existing test assertions keep working.
 */

import { OnboardingFlow } from "@/components/OnboardingFlow";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";

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
  // workspaceId is "" when this component is used outside a workspace
  // context (e.g. tests). The onboarding flow only mounts when we have
  // a real workspace and we're in a true-empty state, not a filtered one.
  const { workspaceId } = useWorkspaceContext();
  const showOnboarding = !hasFiltersActive && workspaceId !== "";

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
      {showOnboarding ? (
        <OnboardingFlow context="empty-kanban" workspaceId={workspaceId} />
      ) : null}
    </div>
  );
}
