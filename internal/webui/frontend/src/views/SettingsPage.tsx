import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView } from "@/hooks";

const SettingsView = lazy(() =>
  import("@/components/SettingsView").then((m) => ({
    default: m.SettingsView,
  })),
);

export function SettingsPage() {
  const { view: activeView, navigateToView } = useRouteView();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Column />}>
        <SettingsView onNavigate={navigateToView} />
      </Suspense>
    </ErrorBoundary>
  );
}
