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
import { AUTH_MODE_OPEN } from "@/types/common";

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
        const defaultWs =
          workspaces.find((ws) => ws.is_default) ?? workspaces[0];
        if (defaultWs) {
          navigate(`/ws/${defaultWs.id}/kanban`, { replace: true });
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

  if (!resolving) {
    return (
      <>
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            justifyContent: "center",
            height: "100vh",
            gap: "16px",
            color: "var(--text-secondary, #666)",
          }}
        >
          <p style={{ margin: 0, fontSize: "15px" }}>
            No workspaces found. Create one to get started.
          </p>
          <button
            onClick={() => setShowCreate(true)}
            style={{
              padding: "10px 24px",
              border: "none",
              borderRadius: "8px",
              background: "var(--bg-accent, #1976d2)",
              color: "#fff",
              fontSize: "14px",
              fontWeight: 500,
              cursor: "pointer",
            }}
          >
            Create Workspace
          </button>
        </div>
        <CreateWorkspaceModal
          isOpen={showCreate}
          onClose={() => setShowCreate(false)}
          onSuccess={(data) => {
            setShowCreate(false);
            const ws = data.workspaces[0];
            if (ws) {
              navigate(`/ws/${ws.id}/`, { replace: true });
            }
          }}
        />
      </>
    );
  }

  return null;
}
