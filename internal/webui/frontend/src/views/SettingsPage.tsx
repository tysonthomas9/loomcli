import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { useRouteView } from "@/hooks/common/useRouteView";

const SettingsView = lazy(() =>
  import("@/components/SettingsView/SettingsView").then((m) => ({
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
