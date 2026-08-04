import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView, useWorkspaceContext } from "@/hooks";

const WorkspaceView = lazy(() =>
  import("@/components/WorkspaceView").then((m) => ({
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
