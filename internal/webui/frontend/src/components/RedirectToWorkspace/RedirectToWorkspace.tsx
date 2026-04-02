/**
 * RedirectToWorkspace — root "/" route component.
 * Reads last-used workspace from localStorage or fetches from API,
 * then navigates to /ws/{id}/ with replace semantics.
 * When no workspaces exist, shows a create button.
 *
 * Waits for the auth token to be acquired before fetching workspaces,
 * preventing a race condition where the API call fires before the JWT
 * is exchanged and returns 401.
 */

import { useEffect, useState, useCallback } from "react";
import { useNavigate } from "react-router-dom";

import {
  getLastWorkspaceId,
  clearLastWorkspaceId,
} from "@/utils/scopedStorage";
import { fetchWorkspace, invalidateWorkspaceCache } from "@/api/workspace";
import { getAuthState, onAuthStateChange } from "@/api/client";
import { useAuth } from "@/contexts/AuthContext";
import { CreateWorkspaceModal } from "@/components/CreateWorkspaceModal";

export function RedirectToWorkspace() {
  const navigate = useNavigate();
  const { mode } = useAuth();
  const [resolving, setResolving] = useState(true);
  const [showCreate, setShowCreate] = useState(false);

  const resolveWorkspace = useCallback(() => {
    const lastId = getLastWorkspaceId();

    invalidateWorkspaceCache();
    fetchWorkspace()
      .then((data) => {
        const workspaces = data.workspaces;
        if (workspaces.length === 0) {
          if (lastId) clearLastWorkspaceId(lastId);
          setResolving(false);
          return;
        }

        // If localStorage has a valid workspace, use it
        if (lastId && workspaces.some((ws) => ws.id === lastId)) {
          navigate(`/ws/${lastId}/`, { replace: true });
          return;
        }

        // Stale or missing — clear and use default
        if (lastId) clearLastWorkspaceId(lastId);
        const defaultWs =
          workspaces.find((ws) => ws.is_default) ?? workspaces[0];
        if (defaultWs) {
          navigate(`/ws/${defaultWs.id}/`, { replace: true });
        } else {
          setResolving(false);
        }
      })
      .catch(() => {
        // Network error — try localStorage as best-effort if available
        if (lastId) {
          navigate(`/ws/${lastId}/`, { replace: true });
          return;
        }
        setResolving(false);
      });
  }, [navigate]);

  useEffect(() => {
    // No auth mode — fetch immediately
    if (mode === "none") {
      resolveWorkspace();
      return;
    }

    // External auth — wait for JWT to be acquired before fetching
    if (getAuthState() === "authenticated") {
      resolveWorkspace();
      return;
    }

    const unsub = onAuthStateChange((state) => {
      if (state === "authenticated") {
        unsub();
        resolveWorkspace();
      }
    });

    return unsub;
  }, [mode, resolveWorkspace]);

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
          onSuccess={(data, _name) => {
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
