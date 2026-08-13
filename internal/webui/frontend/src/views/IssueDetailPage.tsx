import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";

import { ErrorBoundary, IssueDetailView } from "@/components";
import type { Issue, IssueDetails } from "@/types";
import type { IssueContext } from "@/api/terminal";
import { useLocalSettings, useWorkspaceContext } from "@/hooks/workspace";
import { startEpicRunnerForIssue } from "@/hooks/workspace/startEpicRunnerForIssue";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

export function IssueDetailPage() {
  const {
    issueDetails,
    isLoadingDetails,
    detailError,
    selectedIssueId,
    previousView,
  } = useWorkspaceViewData();

  const {
    handleApprove,
    handleReject,
    handleCopyLink,
    handleIssueClick,
    updateIssueDetails,
    navigateToView,
    setPendingIssueContext,
    showToast,
  } = useWorkspaceViewActions();
  const {
    workspaceId,
    repos,
    agents: workspaceAgents,
    upsertAgent,
  } = useWorkspaceContext();
  const { settings: localSettings } = useLocalSettings();
  const [isStartingEpicRun, setIsStartingEpicRun] = useState(false);

  const navigate = useNavigate();

  const handleBackFromDetail = useCallback(() => {
    if (window.history.length > 1) {
      navigate(-1);
    } else {
      navigateToView("kanban");
    }
  }, [navigate, navigateToView]);

  const handleOpenIssueInTerminal = useCallback(
    (issue: Issue | IssueDetails) => {
      const context: IssueContext = {
        issue_id: issue.id,
        title: issue.title,
      };
      if (issue.description) context.description = issue.description;
      if (issue.design) context.design = issue.design;
      setPendingIssueContext(context);
      navigateToView("terminal");
    },
    [navigateToView, setPendingIssueContext],
  );

  const handleRunEpic = useCallback(
    async (issue: Issue | IssueDetails) => {
      if (issue.issue_type !== "epic" || isStartingEpicRun) return;

      setIsStartingEpicRun(true);
      try {
        const { run, leadAgentName } = await startEpicRunnerForIssue({
          workspaceId,
          issue,
          repos,
          agents: workspaceAgents,
          localSettings,
          upsertAgent,
        });
        showToast(`Epic runner queued for ${leadAgentName}: ${run.run_id}`, {
          type: "success",
        });
      } catch (err) {
        showToast(
          `Epic runner failed: ${
            err instanceof Error ? err.message : "Unable to start workflow"
          }`,
          { type: "error" },
        );
      } finally {
        setIsStartingEpicRun(false);
      }
    },
    [
      isStartingEpicRun,
      localSettings,
      repos,
      showToast,
      upsertAgent,
      workspaceAgents,
      workspaceId,
    ],
  );

  return (
    <ErrorBoundary resetOnChange={[selectedIssueId]}>
      <IssueDetailView
        issue={issueDetails}
        isLoading={isLoadingDetails}
        error={detailError}
        previousView={previousView}
        onBack={handleBackFromDetail}
        onApprove={handleApprove}
        onReject={handleReject}
        onOpenInTerminal={handleOpenIssueInTerminal}
        onRunEpic={handleRunEpic}
        isRunningEpic={isStartingEpicRun}
        onCopyLink={handleCopyLink}
        onNavigateToIssue={handleIssueClick}
        onIssueUpdate={updateIssueDetails}
      />
    </ErrorBoundary>
  );
}
