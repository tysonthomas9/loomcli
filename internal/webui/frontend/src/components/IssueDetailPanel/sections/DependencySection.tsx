/**
 * DependencySection component.
 * Editable dependencies section for the Issue Detail Panel.
 * Allows users to add and remove blocking dependencies.
 * Dependency items are clickable chips with status indicators for navigation.
 */

import { useState, useCallback, useRef } from "react";

import type { IssueWithDependencyMetadata, DependencyType } from "@/types";
import { formatStatusLabel } from "@/utils/issue";

import { DependencySearchPicker } from "./DependencySearchPicker";
import styles from "./DependencySection.module.css";

/** Default number of dependency chips to show before truncating. */
const DEFAULT_DEPTH_LIMIT = 5;

/**
 * Get the CSS class for a status dot based on issue status.
 */
function getStatusDotClass(status: string | undefined): string {
  switch (status) {
    case "closed":
      return styles.statusDotClosed ?? "";
    case "in_progress":
      return styles.statusDotInProgress ?? "";
    case "blocked":
      return styles.statusDotBlocked ?? "";
    default:
      return styles.statusDotOpen ?? "";
  }
}

/**
 * Props for the DependencySection component.
 */
export interface DependencySectionProps {
  /** Current issue ID (to add dependencies to) */
  issueId: string;
  /** List of current dependencies */
  dependencies: IssueWithDependencyMetadata[];
  /** Callback when dependency is added */
  onAddDependency: (dependsOnId: string, type: DependencyType) => Promise<void>;
  /** Callback when dependency is removed */
  onRemoveDependency: (dependsOnId: string) => Promise<void>;
  /** Callback when a dependency chip is clicked to navigate to it */
  onNavigateToIssue?:
    | ((issue: IssueWithDependencyMetadata) => void)
    | undefined;
  /** Whether the section is read-only */
  disabled?: boolean;
  /** Maximum dependencies to show before truncating (default: 5) */
  depthLimit?: number;
  /** Custom class name */
  className?: string;
}

/**
 * DependencySection displays and manages issue dependencies.
 *
 * Features:
 * - Display existing dependencies with remove buttons
 * - Add dependency via text input (issue ID)
 * - Loading states during add/remove operations
 * - Error message display
 */
export function DependencySection({
  issueId,
  dependencies,
  onAddDependency,
  onRemoveDependency,
  onNavigateToIssue,
  disabled = false,
  depthLimit = DEFAULT_DEPTH_LIMIT,
  className,
}: DependencySectionProps): JSX.Element {
  const [isAdding, setIsAdding] = useState(false);
  const [savingId, setSavingId] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isExpanded, setIsExpanded] = useState(false);

  // Reset expanded state when navigating to a different issue
  const prevIssueIdRef = useRef(issueId);
  if (prevIssueIdRef.current !== issueId) {
    prevIssueIdRef.current = issueId;
    if (isExpanded) setIsExpanded(false);
  }

  const handleStartAdd = useCallback(() => {
    if (disabled) return;
    setIsAdding(true);
    setError(null);
  }, [disabled]);

  const handleCancelAdd = useCallback(() => {
    setIsAdding(false);
    setError(null);
  }, []);

  const handleSelectDependency = useCallback(
    async (selectedId: string) => {
      const trimmedId = selectedId.trim();

      if (!trimmedId) {
        setError("Please enter an issue ID");
        return;
      }

      // Prevent self-dependency
      if (trimmedId === issueId) {
        setError("Cannot add self as dependency");
        return;
      }

      // Check if already a dependency
      if (dependencies.some((dep) => dep.id === trimmedId)) {
        setError("Already a dependency");
        return;
      }

      setError(null);
      setSavingId(trimmedId);

      try {
        await onAddDependency(trimmedId, "blocks");
        // Success - reset form
        setIsAdding(false);
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to add dependency";
        setError(message);
      } finally {
        setSavingId(null);
      }
    },
    [issueId, dependencies, onAddDependency],
  );

  const handleRemove = useCallback(
    async (depId: string) => {
      if (disabled || removingId) return;

      setError(null);
      setRemovingId(depId);

      try {
        await onRemoveDependency(depId);
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to remove dependency";
        setError(message);
      } finally {
        setRemovingId(null);
      }
    },
    [disabled, removingId, onRemoveDependency],
  );

  const rootClassName = [styles.dependencySection, className]
    .filter(Boolean)
    .join(" ");
  const isBusy = savingId !== null || removingId !== null;

  return (
    <section className={rootClassName} data-testid="dependency-section">
      {/* Header */}
      <div className={styles.header}>
        <h3 className={styles.sectionTitle}>
          Blocked By {dependencies.length > 0 && `(${dependencies.length})`}
        </h3>
        {!disabled && !isAdding && (
          <button
            type="button"
            className={styles.addButton}
            onClick={handleStartAdd}
            disabled={isBusy}
            aria-label="Add dependency"
            data-testid="add-dependency-button"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 14 14"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M7 2V12M2 7H12"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
            Add
          </button>
        )}
      </div>

      {/* Error display */}
      {error && (
        <div
          className={styles.error}
          role="alert"
          data-testid="dependency-error"
        >
          {error}
        </div>
      )}

      {/* Add dependency search picker */}
      {isAdding && (
        <div className={styles.addForm} data-testid="add-dependency-form">
          <DependencySearchPicker
            issueId={issueId}
            existingDependencyIds={dependencies.map((d) => d.id)}
            onSelect={handleSelectDependency}
            onCancel={handleCancelAdd}
            isSaving={savingId !== null}
          />
        </div>
      )}

      {/* Dependency list */}
      {dependencies.length > 0 ? (
        <ul className={styles.dependencyList} data-testid="dependency-list">
          {(isExpanded || dependencies.length <= depthLimit
            ? dependencies
            : dependencies.slice(0, depthLimit)
          ).map((dep) => {
            const statusClass =
              dep.status === "closed" ? styles.dependencyClosed : "";
            const isRemoving = removingId === dep.id;
            const isClickable = !!onNavigateToIssue;

            return (
              <li
                key={dep.id}
                className={`${styles.dependencyChip} ${statusClass} ${isRemoving ? styles.removing : ""} ${isClickable ? styles.clickable : ""}`}
                data-testid={`dependency-item-${dep.id}`}
                onClick={isClickable ? () => onNavigateToIssue(dep) : undefined}
                role={isClickable ? "button" : undefined}
                tabIndex={isClickable ? 0 : undefined}
                onKeyDown={
                  isClickable
                    ? (e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          onNavigateToIssue(dep);
                        }
                      }
                    : undefined
                }
              >
                <span
                  className={`${styles.statusDot} ${getStatusDotClass(dep.status)}`}
                  aria-label={dep.status ?? "open"}
                />
                <span className={styles.dependencyId}>{dep.id}</span>
                <span className={styles.dependencyTitle}>{dep.title}</span>
                {dep.dependency_type && (
                  <span className={styles.dependencyType}>
                    {formatStatusLabel(dep.dependency_type.replace(/-/g, "_"))}
                  </span>
                )}
                {!disabled && (
                  <button
                    type="button"
                    className={styles.removeButton}
                    onClick={(e) => {
                      e.stopPropagation();
                      handleRemove(dep.id);
                    }}
                    disabled={isBusy}
                    aria-label={`Remove dependency ${dep.id}`}
                    data-testid={`remove-dependency-${dep.id}`}
                  >
                    {isRemoving ? (
                      <span className={styles.spinner} />
                    ) : (
                      <svg
                        width="14"
                        height="14"
                        viewBox="0 0 14 14"
                        fill="none"
                        aria-hidden="true"
                      >
                        <path
                          d="M3 3L11 11M11 3L3 11"
                          stroke="currentColor"
                          strokeWidth="2"
                          strokeLinecap="round"
                        />
                      </svg>
                    )}
                  </button>
                )}
              </li>
            );
          })}
          {dependencies.length > depthLimit && (
            <li className={styles.showMoreItem}>
              <button
                type="button"
                className={styles.showMoreButton}
                onClick={() => setIsExpanded(!isExpanded)}
                data-testid="show-more-dependencies"
              >
                {isExpanded ? "Show less" : `Show all (${dependencies.length})`}
              </button>
            </li>
          )}
        </ul>
      ) : (
        !isAdding && (
          <p className={styles.emptyMessage} data-testid="no-dependencies">
            No blocking dependencies
          </p>
        )
      )}
    </section>
  );
}
