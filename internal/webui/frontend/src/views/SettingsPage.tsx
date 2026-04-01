import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";

const SettingsView = lazy(() =>
  import("@/components/SettingsView").then((m) => ({
    default: m.SettingsView,
  })),
);

export interface SettingsPageProps {
  onNavigate: (view: ViewMode) => void;
  activeView: ViewMode;
}

export function SettingsPage({ onNavigate, activeView }: SettingsPageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Column />}>
        <SettingsView onNavigate={onNavigate} />
      </Suspense>
    </ErrorBoundary>
  );
}
