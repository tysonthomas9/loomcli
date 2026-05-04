import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

const MonitorDashboard = lazy(() =>
  import("@/components/MonitorDashboard/MonitorDashboard").then((m) => ({
    default: m.MonitorDashboard,
  })),
);

export function MonitorPage() {
  const { activeView } = useWorkspaceViewData();
  const { navigateToView, handleIssueClick, handleAgentClick } =
    useWorkspaceViewActions();

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Monitor />}>
        <MonitorDashboard
          onViewChange={navigateToView}
          onIssueClick={handleIssueClick}
          onAgentClick={handleAgentClick}
        />
      </Suspense>
    </ErrorBoundary>
  );
}
