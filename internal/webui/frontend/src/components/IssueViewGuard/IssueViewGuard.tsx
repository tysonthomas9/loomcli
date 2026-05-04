import type { ReactNode } from "react";

import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { ErrorDisplay } from "@/components/ErrorDisplay/ErrorDisplay";
import { EmptyWorkspaceBoard } from "@/components/EmptyWorkspaceBoard/EmptyWorkspaceBoard";
import type { Issue } from "@/types";

import styles from "./IssueViewGuard.module.css";

function buildAutoRetryDescription(
  retryCount: number,
  nextRetryAt: number | null,
): string {
  if (nextRetryAt === null) {
    return `Retrying automatically (attempt ${retryCount})...`;
  }
  const secondsRemaining = Math.max(
    0,
    Math.ceil((nextRetryAt - Date.now()) / 1000),
  );
  if (secondsRemaining === 0) {
    return `Retrying automatically (attempt ${retryCount})...`;
  }
  return `Retrying automatically in ${secondsRemaining}s (attempt ${retryCount})...`;
}

interface IssueViewGuardProps {
  issues: Issue[];
  isLoading: boolean;
  error: string | null;
  isMultiRepo: boolean;
  onRetry: () => void;
  loadingVariant: "columns" | "table";
  children: ReactNode;
  showEmptyState?: boolean;
  /** Current auto-retry attempt (0 = not retrying). */
  retryCount?: number;
  /** Timestamp (ms) when next auto-retry fires, or null. */
  nextRetryAt?: number | null;
}

export function IssueViewGuard({
  issues,
  isLoading,
  error,
  isMultiRepo,
  onRetry,
  loadingVariant,
  children,
  showEmptyState = true,
  retryCount = 0,
  nextRetryAt = null,
}: IssueViewGuardProps) {
  if (isLoading) {
    return (
      <div className={styles.loadingContainer} data-testid="loading-container">
        {loadingVariant === "table" ? (
          <LoadingSkeleton.Table />
        ) : (
          <>
            <LoadingSkeleton.Column />
            <LoadingSkeleton.Column />
            <LoadingSkeleton.Column />
          </>
        )}
      </div>
    );
  }

  if (error) {
    const isStarting = error.includes("workspace is loading");
    // Only mark as "retrying" while a retry is actually scheduled
    // (nextRetryAt is non-null). Once the budget is exhausted, retryCount
    // stays > 0 but nextRetryAt is null — the UI should fall back to the
    // default fetch-error message with a manual retry button.
    const isAutoRetrying =
      retryCount > 0 && nextRetryAt !== null && !isStarting;
    return (
      <ErrorDisplay
        variant={isStarting ? "loading" : "fetch-error"}
        error={new Error(error)}
        showDetails={!isStarting}
        onRetry={onRetry}
        {...(isAutoRetrying && {
          isRetrying: true,
          description: buildAutoRetryDescription(retryCount, nextRetryAt),
        })}
      />
    );
  }

  if (issues.length === 0 && showEmptyState) {
    return <EmptyWorkspaceBoard isMultiRepo={isMultiRepo} />;
  }

  return <>{children}</>;
}
