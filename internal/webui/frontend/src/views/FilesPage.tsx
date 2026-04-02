import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView } from "@/hooks";

const FileExplorer = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
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
