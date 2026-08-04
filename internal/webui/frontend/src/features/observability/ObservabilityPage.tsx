import { ErrorBoundary } from "@/components/ErrorBoundary";

import { ObservabilityDashboard } from "./components/ObservabilityDashboard";

export function ObservabilityPage() {
  return (
    <ErrorBoundary resetOnChange={["observability"]}>
      <ObservabilityDashboard />
    </ErrorBoundary>
  );
}
