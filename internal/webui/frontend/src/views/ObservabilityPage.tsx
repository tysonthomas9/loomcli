import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";

const ObservabilityDashboard = lazy(() =>
  import("@/components/ObservabilityDashboard").then((m) => ({
    default: m.ObservabilityDashboard,
  })),
);

export interface ObservabilityPageProps {
  activeView: ViewMode;
}

export function ObservabilityPage({ activeView }: ObservabilityPageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Observability />}>
        <ObservabilityDashboard />
      </Suspense>
    </ErrorBoundary>
  );
}
