/**
 * WorkspaceLayout — route layout component for workspace-scoped pages.
 * Reads :workspaceId from the URL, passes it to WorkspaceProvider and StoreProvider,
 * and renders child routes via <Outlet />.
 */

import { useParams, useNavigate, Outlet } from "react-router-dom";
import { useEffect, useState } from "react";

import { WorkspaceProvider, StoreProvider, useIssueSessionMap } from "@/hooks";
import { IssueSessionProvider } from "@/contexts/IssueSessionContext";
import { fetchWorkspaceApi } from "@/api/workspace";
import { clearLastWorkspaceId } from "@/utils/scopedStorage";

function IssueSessionWrapper({ children }: { children: React.ReactNode }) {
  const issueSessionMap = useIssueSessionMap();
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

    // Validate workspace ID exists by fetching it directly
    let cancelled = false;
    fetchWorkspaceApi(workspaceId)
      .then((data) => {
        if (cancelled) return;
        if (data && data.id) {
          setValid(true);
        } else {
          clearLastWorkspaceId(workspaceId);
          navigate("/", {
            replace: true,
            state: { failedWorkspaceId: workspaceId },
          });
        }
        setValidating(false);
      })
      .catch((err) => {
        if (cancelled) return;
        // 404 means workspace doesn't exist — clear stale localStorage and redirect
        if (
          err &&
          typeof err === "object" &&
          "status" in err &&
          err.status === 404
        ) {
          clearLastWorkspaceId(workspaceId);
          navigate("/", {
            replace: true,
            state: { failedWorkspaceId: workspaceId },
          });
          setValidating(false);
          return;
        }
        // On other errors, proceed anyway — the workspace data fetch in the provider
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
      <StoreProvider>
        <IssueSessionWrapper>
          <Outlet />
        </IssueSessionWrapper>
      </StoreProvider>
    </WorkspaceProvider>
  );
}
