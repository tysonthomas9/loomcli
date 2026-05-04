import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { useRouteView } from "@/hooks/common/useRouteView";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";

const WorkspaceView = lazy(() =>
  import("@/components/WorkspaceView/WorkspaceView").then((m) => ({
    default: m.WorkspaceView,
  })),
);

export function WorkspacePage() {
  const { view: activeView } = useRouteView();
  const { isMultiRepo } = useWorkspaceContext();

  if (!isMultiRepo) return null;

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Column />}>
        <WorkspaceView />
      </Suspense>
    </ErrorBoundary>
  );
}
