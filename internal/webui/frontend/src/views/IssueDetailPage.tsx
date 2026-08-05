import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";

import { ErrorBoundary, IssueDetailView } from "@/components";
import type { Issue, IssueDetails } from "@/types";
import type { IssueContext } from "@/api/terminal";
import {
  createWorkspaceAgent,
  deleteWorkspaceAgent,
  EPIC_RUNNER_WORKFLOW_NAME,
  startWorkflowRun,
} from "@/hooks/api";
import { useLocalSettings, useWorkspaceContext } from "@/hooks/workspace";
import {
  epicRunnerRuntimePayload,
  issueRepoName,
  leadAgentRepoNames,
} from "@/utils/epicRunnerPayload";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

const MAX_EPIC_LEAD_NAME_SLUG_LENGTH = 48;

function epicLeadNameSlug(epicId: string): string {
  const slug = epicId
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, MAX_EPIC_LEAD_NAME_SLUG_LENGTH)
    .replace(/-+$/g, "");

  return slug || "epic";
}

function nextEpicLeadName(
  epicId: string,
  existingNames: Iterable<string>,
): string {
  const existing = new Set(
    Array.from(existingNames, (name) => name.toLowerCase()),
  );
  const base = `lead-${epicLeadNameSlug(epicId)}`;
  if (!existing.has(base.toLowerCase())) return base;

  for (let i = 2; ; i += 1) {
    const candidate = `${base}-${i}`;
    if (!existing.has(candidate.toLowerCase())) return candidate;
  }
}

function isAgentNameConflict(error: unknown): boolean {
  const message = error instanceof Error ? error.message.toLowerCase() : "";
  return (
    message.includes("already") ||
    message.includes("conflict") ||
    message.includes("exists")
  );
}

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
        const currentRepo = issueRepoName(issue);
        const runtimePayload = epicRunnerRuntimePayload({
          localSettings,
          repos,
          currentRepo,
        });
        const repoNames = leadAgentRepoNames(repos, currentRepo);
        const leadNames = new Set<string>(
          workspaceAgents.map((agent) => agent.name),
        );
        let leadAgentName = "";
        let createdLeadAgentName = "";
        let lastCreateError: unknown = null;

        for (let attempt = 0; attempt < 5; attempt += 1) {
          const candidate = nextEpicLeadName(issue.id, leadNames);
          leadNames.add(candidate);

          try {
            const leadAgent = await createWorkspaceAgent(workspaceId, {
              name: candidate,
              role_name: "lead",
              cross_repo: repoNames.length === 0,
              repos: repoNames,
            });
            upsertAgent?.(leadAgent);
            leadAgentName = leadAgent.name;
            createdLeadAgentName = leadAgent.name;
            break;
          } catch (err) {
            lastCreateError = err;
            if (!isAgentNameConflict(err)) throw err;
          }
        }

        if (!leadAgentName) {
          throw lastCreateError instanceof Error
            ? lastCreateError
            : new Error("Unable to create lead agent");
        }

        let run;
        try {
          run = await startWorkflowRun(workspaceId, EPIC_RUNNER_WORKFLOW_NAME, {
            epicId: issue.id,
            leadName: leadAgentName,
            requestedBy: "ui",
            ...runtimePayload,
          });
        } catch (err) {
          if (createdLeadAgentName) {
            deleteWorkspaceAgent(workspaceId, createdLeadAgentName).catch(
              (cleanupErr: unknown) => {
                console.warn(
                  "failed to delete epic-runner lead after workflow start failed",
                  cleanupErr,
                );
              },
            );
          }
          throw err;
        }
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
