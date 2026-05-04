import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { useRouteView } from "@/hooks/common/useRouteView";

const ObservabilityDashboard = lazy(() =>
  import("@/components/ObservabilityDashboard/ObservabilityDashboard").then(
    (m) => ({
      default: m.ObservabilityDashboard,
    }),
  ),
);

export function ObservabilityPage() {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Observability />}>
        <ObservabilityDashboard />
      </Suspense>
    </ErrorBoundary>
  );
}
