import { useCallback, useEffect, useMemo, useState } from "react";

import {
  fetchWorkspaceApi,
  runOnboardingFirstTask,
  type WorkspaceAgentInfo,
} from "@/api/workspace";
import {
  AIBackendSetupList,
  type AIBackendSetupAction,
} from "@/components/AIBackendSetupList";
import type { OnboardingStep } from "@/components/OnboardingFlow";
import type { ToastOptions } from "@/hooks/ui";
import type { ViewMode } from "@/types";
import type { BackendInfo } from "@/utils/workspace";
import {
  ONBOARDING_AGENT_NAME,
  ONBOARDING_AGENT_ROLE,
  ONBOARDING_ISSUE_DESCRIPTION,
  ONBOARDING_ISSUE_TITLE,
} from "@/utils/onboardingDefaults";
import {
  dismissOnboarding,
  isOnboardingDismissed,
  ONBOARDING_RESTART_EVENT,
  type OnboardingRestartDetail,
} from "@/utils/onboardingState";
import { requestCliSetup } from "@/utils/cliSetup";

type OnboardingAction = "confirming-agent" | "running-first-task";

interface RepositoryIdentity {
  name?: string;
  source_repo_id?: string;
}

export interface WorkspaceOnboardingOptions {
  workspaceId: string;
  agents: readonly WorkspaceAgentInfo[] | undefined;
  repositories: readonly RepositoryIdentity[];
  hasOnboardingRepository: boolean;
  issueCount: number;
  agentTaskCount: number;
  backends: BackendInfo[];
  defaultBackend: string | undefined;
  backendsLoading: boolean;
  backendConfigLoading: boolean;
  backendsError: string | null;
  savingDefaultBackend: boolean;
  updateDefaultBackend: (backend: string) => Promise<boolean>;
  refetchBackends: () => void;
  refetchIssues: () => void | Promise<void>;
  refetchWorkspace: () => void;
  closeAllPanels: () => void;
  navigateToView: (view: ViewMode) => void;
  showToast: (message: string, options?: ToastOptions) => string;
  openAgentCreator: () => void;
}

export interface WorkspaceOnboarding {
  shouldShow: boolean;
  hasPlanner: boolean;
  steps: OnboardingStep[];
  dismiss: () => void;
  beginAgentConfirmation: () => void;
  finishAgentConfirmation: () => void;
}

function isOnboardingPlannerAgent(agent: WorkspaceAgentInfo): boolean {
  const roleName = agent.role_name?.trim();
  if (roleName) {
    return roleName === ONBOARDING_AGENT_ROLE;
  }
  return agent.name === ONBOARDING_AGENT_NAME;
}

function getOnboardingPlannerName(
  agents: readonly WorkspaceAgentInfo[] | undefined,
): string | undefined {
  const agentList = agents ?? [];
  return (
    agentList.find(
      (agent) =>
        agent.name === ONBOARDING_AGENT_NAME &&
        (!agent.role_name || agent.role_name === ONBOARDING_AGENT_ROLE),
    )?.name ?? agentList.find(isOnboardingPlannerAgent)?.name
  );
}

function getSingleRepoSourceRepo(
  repositories: readonly RepositoryIdentity[],
): string | undefined {
  if (repositories.length !== 1) return undefined;
  const repository = repositories[0];
  return repository?.source_repo_id || repository?.name || undefined;
}

export function useWorkspaceOnboarding(
  options: WorkspaceOnboardingOptions,
): WorkspaceOnboarding {
  const {
    workspaceId,
    agents,
    repositories,
    hasOnboardingRepository,
    issueCount,
    agentTaskCount,
    backends,
    defaultBackend,
    backendsLoading,
    backendConfigLoading,
    backendsError,
    savingDefaultBackend,
    updateDefaultBackend,
    refetchBackends,
    refetchIssues,
    refetchWorkspace,
    closeAllPanels,
    navigateToView,
    showToast,
    openAgentCreator,
  } = options;
  const [dismissed, setDismissed] = useState(false);
  const [action, setAction] = useState<OnboardingAction | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const plannerName = getOnboardingPlannerName(agents);
  const hasRepository = repositories.length > 0;
  const hasAgent = (agents?.length ?? 0) > 0;
  const hasPlanner = Boolean(plannerName);
  const hasIssue = issueCount > 0 || agentTaskCount > 0;
  const defaultBackendStatus = backends.find(
    (backend) => backend.name === defaultBackend,
  );
  const defaultBackendReady = defaultBackendStatus?.available === true;
  const complete = hasRepository && hasAgent && hasIssue && defaultBackendReady;
  const shouldShow =
    !dismissed &&
    !complete &&
    (repositories.length === 0 || hasOnboardingRepository) &&
    (!hasRepository || !hasAgent || !hasIssue);

  const dismiss = useCallback(() => {
    dismissOnboarding(workspaceId);
    setDismissed(true);
  }, [workspaceId]);

  const handleBackendSetupAction = useCallback(
    async (backend: BackendInfo, setupAction: AIBackendSetupAction) => {
      if (setupAction === "set-default") {
        const ok = await updateDefaultBackend(backend.name);
        if (ok) {
          showToast(`${backend.displayName} set as default`, {
            type: "success",
          });
          refetchBackends();
        } else {
          showToast(`Failed to set ${backend.displayName} as default`, {
            type: "error",
          });
        }
        return;
      }
      requestCliSetup(backend, setupAction);
      navigateToView("terminal");
    },
    [navigateToView, refetchBackends, showToast, updateDefaultBackend],
  );

  const runFirstTask = useCallback(async () => {
    if (action !== null) return;

    setAction("running-first-task");
    setActionError(null);
    try {
      let onboardingAgent = plannerName;
      try {
        const latestWorkspace = await fetchWorkspaceApi(workspaceId);
        onboardingAgent = getOnboardingPlannerName(latestWorkspace.agents);
      } catch {
        // Fall back to the already-rendered workspace snapshot.
      }
      if (!onboardingAgent) {
        throw new Error("Planner agent is not available yet.");
      }

      const sourceRepo = getSingleRepoSourceRepo(repositories);
      const result = await runOnboardingFirstTask(workspaceId, {
        agent_name: onboardingAgent,
        title: ONBOARDING_ISSUE_TITLE,
        description: ONBOARDING_ISSUE_DESCRIPTION,
        issue_type: "task",
        priority: 2,
        ...(sourceRepo ? { source_repo: sourceRepo } : {}),
      });

      closeAllPanels();
      navigateToView("kanban");
      await refetchIssues();
      refetchWorkspace();
      const actionVerb = result.started ? "Started" : "Queued";
      showToast(`${actionVerb} ${onboardingAgent} on ${result.issue.id}`, {
        type: "success",
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "failed to start first task";
      setActionError(message);
      showToast(`First task did not start: ${message}`, { type: "error" });
    } finally {
      setAction(null);
    }
  }, [
    action,
    closeAllPanels,
    navigateToView,
    plannerName,
    refetchIssues,
    refetchWorkspace,
    repositories,
    showToast,
    workspaceId,
  ]);

  const steps = useMemo<OnboardingStep[]>(
    () => [
      {
        id: "setup-backend",
        title: "Set up AI CLIs",
        description: defaultBackendReady
          ? `${defaultBackendStatus?.displayName ?? "The default CLI"} is ready.`
          : "Install, login, or choose a ready CLI.",
        status: defaultBackendReady ? "complete" : "actionable",
        detail: (
          <AIBackendSetupList
            backends={backends}
            defaultBackend={defaultBackend}
            isLoading={backendsLoading || backendConfigLoading}
            error={backendsError}
            isSavingDefault={savingDefaultBackend}
            onAction={handleBackendSetupAction}
          />
        ),
      },
      {
        id: "workspace-repo",
        title: "Create workspace with repo",
        description: hasRepository
          ? "The sample repo is attached to this workspace."
          : "Add the sample repo from the workspace tree; the URL is prefilled for first-run setup.",
        status: hasRepository ? "complete" : "current",
      },
      {
        id: "verify-repo",
        title: "Verify repository",
        description: hasRepository
          ? "The repo is visible to Loom and ready for the next setup step."
          : "Repository checks run after a repo has been attached.",
        status: hasRepository ? "complete" : "blocked",
      },
      {
        id: "create-agent",
        title: "Create agent",
        description:
          action === "confirming-agent"
            ? "Confirming the planner agent is visible to the workspace."
            : hasAgent
              ? "The first agent definition exists for this workspace."
              : "Create a prefilled planner agent for the sample repo.",
        status:
          action === "confirming-agent"
            ? "pending"
            : hasAgent
              ? "complete"
              : hasRepository && defaultBackendReady
                ? "current"
                : "blocked",
        actionLabel:
          action === "confirming-agent" ? "Confirming..." : "Create Agent",
        actionDisabled: action !== null,
        onAction: () => {
          setActionError(null);
          openAgentCreator();
        },
      },
      {
        id: "create-issue",
        title: "Create first issue",
        description:
          action === "running-first-task"
            ? "Creating the task, assigning the planner, and starting work."
            : hasIssue
              ? "The first issue is ready for agent work."
              : "Create and run the prefilled sample task.",
        status:
          action === "running-first-task"
            ? "pending"
            : hasIssue
              ? "complete"
              : hasPlanner && defaultBackendReady
                ? "current"
                : "blocked",
        actionLabel:
          action === "running-first-task" ? "Starting..." : "Create & Run",
        actionDisabled: action !== null,
        onAction: runFirstTask,
        detail: actionError ? <p role="alert">{actionError}</p> : undefined,
      },
    ],
    [
      action,
      actionError,
      backendConfigLoading,
      backends,
      backendsError,
      backendsLoading,
      defaultBackend,
      defaultBackendReady,
      defaultBackendStatus,
      handleBackendSetupAction,
      hasAgent,
      hasIssue,
      hasPlanner,
      hasRepository,
      openAgentCreator,
      runFirstTask,
      savingDefaultBackend,
    ],
  );

  useEffect(() => {
    setDismissed(isOnboardingDismissed(workspaceId));
  }, [workspaceId]);

  useEffect(() => {
    const handleRestart = (event: Event) => {
      const detail = (event as CustomEvent<OnboardingRestartDetail>).detail;
      if (!detail?.workspaceId || detail.workspaceId === workspaceId) {
        setDismissed(false);
      }
    };
    window.addEventListener(ONBOARDING_RESTART_EVENT, handleRestart);
    return () => {
      window.removeEventListener(ONBOARDING_RESTART_EVENT, handleRestart);
    };
  }, [workspaceId]);

  useEffect(() => {
    if (complete) {
      setDismissed(false);
    }
  }, [complete]);

  return {
    shouldShow,
    hasPlanner,
    steps,
    dismiss,
    beginAgentConfirmation: () => {
      setAction("confirming-agent");
      setActionError(null);
    },
    finishAgentConfirmation: () => setAction(null),
  };
}
