/**
 * WorkspaceLayout — route layout component for workspace-scoped pages.
 *
 * Reads :workspaceId from the URL, fetches workspace data via useWorkspace,
 * and renders WorkspaceProvider with the loaded data. Mounts under
 * AuthBootstrap → AuthGate, so by the time this component is rendered the
 * user is authenticated and api/client requests carry valid auth.
 *
 * Why a component-level fetch instead of a router loader: a loader runs
 * during navigation, before child elements mount, so it would fire before
 * AuthBootstrap has finished initializing the auth client. Cold-loading
 * /ws/<id>/ as the first navigation would 401 because there's no auth
 * context yet. The fix would be to make AuthBootstrap a route with its own
 * loader that initializes auth before child loaders run, but that requires
 * making BetterAuth's session bootstrap loader-friendly. Until that lands,
 * the component-level fetch — gated by AuthBootstrap's render tree — is the
 * correct architecture.
 *
 * Per docs/design/workspace-provider-refactor.md, the load-bearing fix for
 * the workspace-switch staleness bug is:
 *   - WorkspaceProvider is now a pure function of its workspace prop (zero
 *     useState for derived fields), so there is no "new id, old state"
 *     window.
 *   - Per-workspace preferences (selectedRepoNames) live inside
 *     PerWorkspacePrefsProvider keyed on workspace.id; that subtree remounts
 *     cleanly on workspace change while TerminalView and the WebSocket tree
 *     stay mounted outside the keyed boundary.
 *
 * The 404 redirect path mirrors the legacy validation behavior: when the
 * fetched workspace returns 404 (deleted server-side), we clear the stale
 * localStorage pointer and bounce home.
 */

import { useEffect } from "react";
import { useNavigate, useParams, Outlet } from "react-router-dom";

import {
  WorkspaceProvider,
  AgentProvider,
  useIssueSessionMap,
  useWorkspace,
  useWorkspaceContext,
} from "@/hooks";
import { IssueSessionProvider } from "@/contexts/IssueSessionContext";
import { clearLastWorkspaceId } from "@/utils/scopedStorage";

const WORKSPACE_POLL_INTERVAL_MS = 60_000;

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
  const { workspace, error } = useWorkspace({
    pollInterval: WORKSPACE_POLL_INTERVAL_MS,
    ...(workspaceId ? { workspaceId } : {}),
  });

  // Redirect to root on missing param or on 404 / not-found errors. Mirrors
  // the legacy WorkspaceLayout validation.
  useEffect(() => {
    if (!workspaceId) {
      navigate("/", { replace: true });
      return;
    }
    if (error && /not.?found|404/i.test(error)) {
      clearLastWorkspaceId(workspaceId);
      navigate("/", { replace: true });
    }
  }, [workspaceId, error, navigate]);

  // Wait for the initial fetch to populate workspace data — we can't render
  // WorkspaceProvider with a null workspace prop. After the first successful
  // load, useWorkspace keeps the previous workspace in state during a
  // refetch (stale-while-revalidate), so on workspace switch there's a brief
  // window (~200ms) where workspace.id is still the OLD value while the new
  // fetch is in flight. We deliberately allow that stale render rather than
  // returning null:
  //
  //   - flushSync on the navigate() call (in WorkspaceProvider.setActiveWorkspace)
  //     guarantees the URL and useParams update synchronously, so the
  //     staleness window is short and bounded by the fetch.
  //   - PerWorkspacePrefsProvider is keyed on workspace.id, so the moment
  //     the new workspace data arrives the inner provider remounts and
  //     per-workspace state resets cleanly.
  //   - Allowing stale render keeps TerminalView mounted throughout the
  //     switch, avoiding a WebSocket teardown + reconnect on every workspace
  //     change. A brief sidebar flash is far better UX than a blank screen.
  //
  // This is meaningfully different from the bug we're fixing: that bug was
  // PERMANENT staleness — the URL changed but the UI was frozen on the old
  // workspace forever, until the user navigated elsewhere. Here the
  // staleness window is bounded by a single network roundtrip and resolves
  // automatically.
  if (!workspace) return null;

  return (
    <WorkspaceProvider workspace={workspace}>
      <AgentProvider>
        <IssueSessionWrapper>
          <Outlet />
        </IssueSessionWrapper>
      </AgentProvider>
    </WorkspaceProvider>
  );
}
