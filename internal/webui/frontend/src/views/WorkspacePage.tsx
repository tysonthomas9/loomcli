import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";

const WorkspaceView = lazy(() =>
  import("@/components/WorkspaceView").then((m) => ({
    default: m.WorkspaceView,
  })),
);

export interface WorkspacePageProps {
  isMultiRepo: boolean;
  activeView: ViewMode;
}

export function WorkspacePage({ isMultiRepo, activeView }: WorkspacePageProps) {
  if (!isMultiRepo) return null;

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Column />}>
        <WorkspaceView />
      </Suspense>
    </ErrorBoundary>
  );
}
