import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";

const FileExplorer = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.FileExplorer,
  })),
);

export interface FilesPageProps {
  activeView: ViewMode;
}

export function FilesPage({ activeView }: FilesPageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
        <FileExplorer />
      </Suspense>
    </ErrorBoundary>
  );
}
