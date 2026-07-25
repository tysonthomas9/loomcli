/**
 * RedirectToWorkspace — root "/" route component.
 * Reads last-used workspace from localStorage or fetches from API,
 * then navigates to /ws/{id}/ with replace semantics.
 * When no workspaces exist, shows a create button.
 *
 * Waits for auth to settle before fetching workspaces, preventing a race
 * where the API call fires before the JWT is exchanged and returns 401.
 */

import { useEffect, useState, useCallback } from "react";
import { useNavigate, useLocation } from "react-router-dom";

import {
  getLastWorkspaceId,
  clearLastWorkspaceId,
} from "@/utils/scopedStorage";
import { fetchWorkspaceApi } from "@/hooks/api";
import { useAuth } from "@/contexts/AuthContext";
import { CreateWorkspaceModal } from "@/components/CreateWorkspaceModal";
import { ErrorDisplay } from "@/components/ErrorDisplay";
import {
  OnboardingFlow,
  type OnboardingStep,
} from "@/components/OnboardingFlow";
import { KeyboardShortcutProvider } from "@/hooks";
import { AUTH_MODE_OPEN } from "@/types/common";
import {
  ONBOARDING_REPO_URL,
  ONBOARDING_WORKSPACE_NAME,
} from "@/utils/onboardingDefaults";

export function RedirectToWorkspace() {
  const navigate = useNavigate();
  const location = useLocation();
  const { mode, isAuthenticated, isLoading } = useAuth();
  const [resolving, setResolving] = useState(true);
  const [workspaceError, setWorkspaceError] = useState<Error | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const resolveWorkspace = useCallback(() => {
    let cancelled = false;
    setResolving(true);
    setWorkspaceError(null);
    const lastId = getLastWorkspaceId();
    const failedId = (location.state as { failedWorkspaceId?: string } | null)
      ?.failedWorkspaceId;

    fetchWorkspaceApi()
      .then((data) => {
        if (cancelled) return;
        const workspaces = data.workspaces.filter((ws) => ws.id !== failedId);
        if (workspaces.length === 0) {
          if (lastId) clearLastWorkspaceId(lastId);
          setResolving(false);
          return;
        }

        if (lastId && workspaces.some((ws) => ws.id === lastId)) {
          navigate(`/ws/${lastId}/kanban`, { replace: true });
          return;
        }

        if (lastId) clearLastWorkspaceId(lastId);
        const firstWorkspace = workspaces[0];
        if (firstWorkspace) {
          navigate(`/ws/${firstWorkspace.id}/kanban`, { replace: true });
        } else {
          setResolving(false);
        }
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setWorkspaceError(
          error instanceof Error
            ? error
            : new Error("The workspace request failed"),
        );
        setResolving(false);
      });

    return () => {
      cancelled = true;
    };
  }, [navigate, location.state]);

  useEffect(() => {
    if (mode === AUTH_MODE_OPEN) {
      return resolveWorkspace();
    }
    if (isLoading || !isAuthenticated) return;
    return resolveWorkspace();
  }, [mode, isAuthenticated, isLoading, resolveWorkspace]);

  if (!resolving && workspaceError) {
    return (
      <div
        style={{
          display: "grid",
          placeItems: "center",
          minHeight: "100vh",
          padding: "32px",
          background: "var(--color-bg-secondary)",
        }}
      >
        <ErrorDisplay
          variant="connection-error"
          title="Could not load workspaces"
          description="Superfactory could not reach the local workspace service. Your workspace data has not been removed. Try again after the service recovers."
          error={workspaceError}
          onRetry={() => {
            resolveWorkspace();
          }}
        />
      </div>
    );
  }

  if (!resolving) {
    const onboardingSteps: OnboardingStep[] = [
      {
        id: "workspace-repo",
        title: "Create workspace with repo",
        description:
          "Create the first workspace and clone the sample repo in one fixed setup step.",
        status: "current",
        actionLabel: "Create Workspace",
        onAction: () => setShowCreate(true),
      },
      {
        id: "verify-repo",
        title: "Verify repository",
        description:
          "Confirm the repo is cloned, readable, and ready for agent work.",
        status: "blocked",
      },
      {
        id: "setup-backend",
        title: "Set up AI CLI",
        description:
          "Install, login, or configure the local CLI from an inline terminal when needed.",
        status: "blocked",
      },
      {
        id: "create-agent",
        title: "Create agent",
        description:
          "Add the first agent definition with the sample repo and backend preselected.",
        status: "blocked",
      },
      {
        id: "create-issue",
        title: "Create first issue",
        description:
          "Create the initial task so the agent has concrete work to run.",
        status: "blocked",
      },
    ];

    // CreateWorkspaceModal uses useRegisterEscapeLayer which requires a
    // KeyboardShortcutProvider ancestor. The root "/" route renders
    // outside App's provider, so wrap locally.
    return (
      <KeyboardShortcutProvider>
        <div
          style={{
            display: "grid",
            alignItems: "center",
            justifyContent: "center",
            minHeight: "100vh",
            padding: "32px 0",
            background: "var(--color-bg-secondary)",
          }}
        >
          <OnboardingFlow
            title="No workspaces found"
            subtitle="Start with the guided onboarding flow. Step 1 creates a workspace and clones the sample repo; the remaining steps stay visible so setup can continue after creation."
            steps={onboardingSteps}
          />
        </div>
        <CreateWorkspaceModal
          isOpen={showCreate}
          onClose={() => setShowCreate(false)}
          initialValues={{
            name: ONBOARDING_WORKSPACE_NAME,
            urlInput: ONBOARDING_REPO_URL,
          }}
          onSuccess={(data, createdName) => {
            setShowCreate(false);
            const ws = data.workspaces.find(
              (item) => item.name === createdName,
            );
            if (ws) {
              navigate(`/ws/${ws.id}/`, { replace: true });
            } else {
              resolveWorkspace();
            }
          }}
        />
      </KeyboardShortcutProvider>
    );
  }

  return null;
}
