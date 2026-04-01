import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";
import type { Issue } from "@/types";

const GraphView = lazy(() =>
  import("@/components/GraphView").then((m) => ({ default: m.GraphView })),
);

export interface GraphPageProps {
  filteredIssues: Issue[];
  onNodeClick: (issue: Issue) => void;
  activeView: ViewMode;
}

export function GraphPage({
  filteredIssues,
  onNodeClick,
  activeView,
}: GraphPageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Graph />}>
        <GraphView issues={filteredIssues} onNodeClick={onNodeClick} />
      </Suspense>
    </ErrorBoundary>
  );
}
