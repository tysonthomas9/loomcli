import { lazy, Suspense } from "react";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView } from "@/hooks";

const WorkflowsDashboard = lazy(() =>
  import("@/components/WorkflowsDashboard").then((m) => ({
    default: m.WorkflowsDashboard,
  })),
);

export function WorkflowsPage() {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Observability />}>
        <WorkflowsDashboard />
      </Suspense>
    </ErrorBoundary>
  );
}
