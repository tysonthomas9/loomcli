import type { ReactNode } from "react";

import {
  LoadingSkeleton,
  ErrorDisplay,
  EmptyWorkspaceBoard,
} from "@/components";
import type { Issue } from "@/types";

import styles from "./IssueViewGuard.module.css";

interface IssueViewGuardProps {
  issues: Issue[];
  isLoading: boolean;
  error: string | null;
  isMultiRepo: boolean;
  onRetry: () => void;
  loadingVariant: "columns" | "table";
  children: ReactNode;
  showEmptyState?: boolean;
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
    return (
      <ErrorDisplay
        variant={isStarting ? "loading" : "fetch-error"}
        error={new Error(error)}
        showDetails={!isStarting}
        onRetry={onRetry}
      />
    );
  }

  if (issues.length === 0 && showEmptyState) {
    return <EmptyWorkspaceBoard isMultiRepo={isMultiRepo} />;
  }

  return <>{children}</>;
}
