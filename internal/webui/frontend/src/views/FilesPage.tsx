import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView, useWorkspaceContext } from "@/hooks";

const WorkspaceFileBrowser = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.WorkspaceFileBrowser,
  })),
);

export function FilesPage() {
  const { view: activeView } = useRouteView();
  const { workspaceId } = useWorkspaceContext();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
        {/* Key by workspace so switching remounts with that workspace's tabs. */}
        <WorkspaceFileBrowser key={workspaceId} />
      </Suspense>
    </ErrorBoundary>
  );
}
