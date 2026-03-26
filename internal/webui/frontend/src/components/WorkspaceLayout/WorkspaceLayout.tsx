/**
 * WorkspaceLayout — route layout component for workspace-scoped pages.
 * Reads :workspaceId from the URL, passes it to WorkspaceProvider and AgentProvider,
 * and renders child routes via <Outlet />.
 */

import { useParams, useNavigate, Outlet } from "react-router-dom";
import { useEffect, useState } from "react";

import {
  WorkspaceProvider,
  AgentProvider,
  useIssueSessionMap,
  useWorkspaceContext,
} from "@/hooks";
import { IssueSessionProvider } from "@/contexts/IssueSessionContext";
import { fetchWorkspace } from "@/api/workspace";

function IssueSessionWrapper({ children }: { children: React.ReactNode }) {
  const { workspace } = useWorkspaceContext();
  const issueSessionMap = useIssueSessionMap(workspace?.id ?? "");
  return (
    <IssueSessionProvider value={issueSessionMap}>
      {children}
    </IssueSessionProvider>
  );
}

export function WorkspaceLayout() {
  const { workspaceId } = useParams<{ workspaceId: string }>();
  const navigate = useNavigate();
  const [validating, setValidating] = useState(true);
  const [valid, setValid] = useState(false);

  useEffect(() => {
    if (!workspaceId) {
      navigate("/", { replace: true });
      return;
    }

    // Validate workspace ID exists
    let cancelled = false;
    fetchWorkspace()
      .then((data) => {
        if (cancelled) return;
        const found = data.workspaces.some((ws) => ws.id === workspaceId);
        if (!found) {
          navigate("/", { replace: true });
        } else {
          setValid(true);
        }
        setValidating(false);
      })
      .catch(() => {
        if (cancelled) return;
        // On error, proceed anyway — the workspace data fetch in the provider
        // will handle errors with proper UI
        setValid(true);
        setValidating(false);
      });

    return () => {
      cancelled = true;
    };
  }, [workspaceId, navigate]);

  if (validating || !valid || !workspaceId) {
    return null;
  }

  return (
    <WorkspaceProvider workspaceId={workspaceId}>
      <AgentProvider>
        <IssueSessionWrapper>
          <Outlet />
        </IssueSessionWrapper>
      </AgentProvider>
    </WorkspaceProvider>
  );
}
