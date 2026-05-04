import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { IssueViewGuard } from "@/components/IssueViewGuard/IssueViewGuard";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

const GraphView = lazy(() =>
  import("@/components/GraphView/GraphView").then((m) => ({
    default: m.GraphView,
  })),
);

export function GraphPage() {
  const {
    filteredIssues,
    issues,
    isLoading,
    error,
    retryCount,
    nextRetryAt,
    isMultiRepo,
    activeView,
  } = useWorkspaceViewData();

  const { handleIssueClick, refetch } = useWorkspaceViewActions();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <IssueViewGuard
        issues={issues}
        isLoading={isLoading}
        error={error}
        retryCount={retryCount}
        nextRetryAt={nextRetryAt}
        isMultiRepo={isMultiRepo}
        onRetry={refetch}
        loadingVariant="columns"
      >
        <Suspense fallback={<LoadingSkeleton.Graph />}>
          <GraphView issues={filteredIssues} onNodeClick={handleIssueClick} />
        </Suspense>
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
