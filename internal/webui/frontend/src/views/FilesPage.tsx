import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView } from "@/hooks";
import styles from "./FilesPage.module.css";

const WorkspaceFileBrowser = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.WorkspaceFileBrowser,
  })),
);

export function FilesPage() {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <div className={styles.page}>
        <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
          <WorkspaceFileBrowser mode="workspace" />
        </Suspense>
      </div>
    </ErrorBoundary>
  );
}
