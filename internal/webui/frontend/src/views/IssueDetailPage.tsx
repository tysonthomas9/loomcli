import { useCallback } from "react";
import { useNavigate } from "react-router-dom";

import { ErrorBoundary, IssueDetailView } from "@/components";
import type { Issue, IssueDetails } from "@/types";
import type { IssueContext } from "@/api/terminal";
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
  } = useWorkspaceViewActions();

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
        onCopyLink={handleCopyLink}
        onNavigateToIssue={handleIssueClick}
        onIssueUpdate={updateIssueDetails}
      />
    </ErrorBoundary>
  );
}
