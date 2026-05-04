import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { useRouteView } from "@/hooks/common/useRouteView";

const FileExplorer = lazy(() =>
  import("@/components/FileExplorer/FileExplorer").then((m) => ({
    default: m.FileExplorer,
  })),
);

export function FilesPage() {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
        <FileExplorer />
      </Suspense>
    </ErrorBoundary>
  );
}
