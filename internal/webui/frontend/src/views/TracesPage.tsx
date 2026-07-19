import { lazy, Suspense } from "react";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView } from "@/hooks";

const TracesView = lazy(() =>
  import("@/components/TracesView").then((m) => ({
    default: m.TracesView,
  })),
);

export function TracesPage(): JSX.Element {
  const { view: activeView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Observability />}>
        <TracesView />
      </Suspense>
    </ErrorBoundary>
  );
}
