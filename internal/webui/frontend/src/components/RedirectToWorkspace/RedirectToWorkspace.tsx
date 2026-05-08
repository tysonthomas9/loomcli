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
import { useRegisterOnboardingAction } from "@/contexts/OnboardingActionsContext";
import { OnboardingFlow } from "@/components/OnboardingFlow";
import { WorkspaceRepoWizard } from "@/components/WorkspaceRepoWizard";
import { KeyboardShortcutProvider } from "@/hooks";
import { AUTH_MODE_OPEN } from "@/types/common";

import styles from "./RedirectToWorkspace.module.css";

export function RedirectToWorkspace() {
  const navigate = useNavigate();
  const location = useLocation();
  const { mode, isAuthenticated, isLoading } = useAuth();
  const [resolving, setResolving] = useState(true);
  const [showCreate, setShowCreate] = useState(false);

  const resolveWorkspace = useCallback(() => {
    let cancelled = false;
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
      .catch(() => {
        if (cancelled) return;
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

  // Register the workspace-repo CTA so OnboardingFlow's step-1 button
  // opens the existing CreateWorkspaceModal. WorkspaceRepoWizard (the
  // fixed-path replacement called for in the spec) lands in a follow-up.
  useRegisterOnboardingAction("open_workspace_repo_wizard", () => {
    setShowCreate(true);
  });

  if (!resolving) {
    // CreateWorkspaceModal uses useRegisterEscapeLayer which requires a
    // KeyboardShortcutProvider ancestor. The root "/" route renders
    // outside App's provider, so wrap locally.
    return (
      <KeyboardShortcutProvider>
        <main className={styles.scene}>
          {/* Visually-hidden heading preserves the existing
              accessibility/test contract ("No workspaces found") without
              competing with the OnboardingFlow's own heading. */}
          <h1 className={styles.srOnly}>
            No workspaces found. Create one to get started.
          </h1>

          {/* Imprint mark — top-left masthead. */}
          <div className={styles.imprint} aria-hidden="true">
            <span className={styles.imprintMark}>Volume I</span>
            <span className={styles.imprintTitle}>Loom &mdash; First Run</span>
          </div>

          {/* Colophon — bottom-left signature. */}
          <div className={styles.colophon} aria-hidden="true">
            <div>Bound &amp; set in {new Date().getFullYear()}</div>
            <div className={styles.colophonRule} />
          </div>

          <div className={styles.flowWrapper}>
            <OnboardingFlow context="no-workspace" />
          </div>
        </main>
        <WorkspaceRepoWizard
          isOpen={showCreate}
          onClose={() => setShowCreate(false)}
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
