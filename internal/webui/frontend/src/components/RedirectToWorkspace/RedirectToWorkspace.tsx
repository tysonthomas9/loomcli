/**
 * RedirectToWorkspace — root "/" route component.
 * Reads last-used workspace from localStorage or fetches from API,
 * then navigates to /ws/{id}/ with replace semantics.
 * T25 will enhance this with full name-to-ID fallback and migration logic.
 */

import { useEffect, useState } from "react";
import { useNavigate, useLocation } from "react-router-dom";

import {
  getLastWorkspaceId,
  clearLastWorkspaceId,
} from "@/utils/scopedStorage";
import { fetchWorkspace } from "@/api/workspace";

export function RedirectToWorkspace() {
  const navigate = useNavigate();
  const location = useLocation();
  const [resolving, setResolving] = useState(true);

  useEffect(() => {
    let cancelled = false;

    const lastId = getLastWorkspaceId();
    const failedId = (location.state as { failedWorkspaceId?: string } | null)
      ?.failedWorkspaceId;

    // Fetch workspace list — validates lastId and provides fallback
    fetchWorkspace()
      .then((data) => {
        if (cancelled) return;
        const workspaces = data.workspaces.filter((ws) => ws.id !== failedId);
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
        if (cancelled) return;
        // API unreachable — show empty state. Do NOT blindly redirect to
        // a potentially stale lastId; WorkspaceLayout will just 404 and
        // redirect back, creating a loop.
        setResolving(false);
      });

    return () => {
      cancelled = true;
    };
  }, [navigate, location.state]);

  if (!resolving) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          height: "100vh",
          color: "var(--text-secondary, #666)",
        }}
      >
        No workspaces found. Create one to get started.
      </div>
    );
  }

  return null;
}
