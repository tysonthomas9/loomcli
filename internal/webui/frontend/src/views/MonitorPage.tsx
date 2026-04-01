import { lazy, Suspense } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";
import type { Issue } from "@/types";

const MonitorDashboard = lazy(() =>
  import("@/components/MonitorDashboard").then((m) => ({
    default: m.MonitorDashboard,
  })),
);

export interface MonitorPageProps {
  onViewChange: (view: ViewMode) => void;
  onIssueClick: (issue: Issue) => void;
  onAgentClick: (agentName: string) => void;
  activeView: ViewMode;
}

export function MonitorPage({
  onViewChange,
  onIssueClick,
  onAgentClick,
  activeView,
}: MonitorPageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <Suspense fallback={<LoadingSkeleton.Monitor />}>
        <MonitorDashboard
          onViewChange={onViewChange}
          onIssueClick={onIssueClick}
          onAgentClick={onAgentClick}
        />
      </Suspense>
    </ErrorBoundary>
  );
}
