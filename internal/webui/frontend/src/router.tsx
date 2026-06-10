/**
 * React Router configuration — single source of truth for all routes.
 *
 * Route tree:
 *   /                                      → RedirectToWorkspace (resolve last-used or default)
 *   /ws/:workspaceId/                      → WorkspaceLayout → App (shell/layout)
 *     /kanban                              → KanbanPage (lazy)
 *     /table                               → TablePage (lazy)
 *     /graph                               → GraphPage (lazy)
 *     /monitor                             → MonitorPage (lazy)
 *     /observability                       → ObservabilityPage (lazy)
 *     /terminal                            → TerminalView (always-mounted in shell, route renders null)
 *     /settings                            → SettingsPage (lazy)
 *     /workspace                           → WorkspacePage (lazy)
 *     /files                               → FilesPage (lazy)
 *     /issues/:issueId                     → IssueDetailPage (lazy)
 *   /test/*                                → TestFixtures (dev only, preserved)
 *   *                                      → NotFound (404 page)
 */

import { createBrowserRouter, Navigate } from "react-router-dom";

import App from "@/App";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { WorkspaceLayout } from "@/components/WorkspaceLayout";
import { RedirectToWorkspace } from "@/components/RedirectToWorkspace";
import { NotFound } from "@/components/NotFound";
import { KeyboardShortcutProvider } from "@/hooks/ui/useKeyboardShortcuts";

const devRoutes = import.meta.env.DEV
  ? [
      {
        path: "test/*",
        lazy: async () => {
          const {
            IssueDetailPanelFixture,
            ErrorTriggerFixture,
            ToastTestFixture,
            SessionNamePromptFixture,
            HelpPopoverFixture,
            PasteConfirmFixture,
            WorkspaceTreeFixture,
            SplitDetailSummaryFixture,
          } = await import("@/TestFixtures");
          return {
            Component: () => {
              const path = window.location.pathname;
              let fixture: JSX.Element;
              if (path === "/test/issue-detail-panel")
                fixture = <IssueDetailPanelFixture />;
              else if (path === "/test/error-boundary")
                fixture = <ErrorTriggerFixture />;
              else if (path === "/test/toast") fixture = <ToastTestFixture />;
              else if (path === "/test/session-name-prompt")
                fixture = <SessionNamePromptFixture />;
              else if (path === "/test/help-popover")
                fixture = <HelpPopoverFixture />;
              else if (path === "/test/paste-confirm")
                fixture = <PasteConfirmFixture />;
              else if (path === "/test/workspace-tree")
                fixture = <WorkspaceTreeFixture />;
              else if (path === "/test/split-detail-summary")
                fixture = <SplitDetailSummaryFixture />;
              else return <NotFound />;
              return (
                <ErrorBoundary>
                  <KeyboardShortcutProvider>{fixture}</KeyboardShortcutProvider>
                </ErrorBoundary>
              );
            },
          };
        },
      },
    ]
  : [];

/**
 * View route children under /ws/:workspaceId.
 * App acts as the layout shell (pathless route); child routes use lazy()
 * to code-split each view page into its own chunk. Views read shared state
 * from WorkspaceViewContext (provided by App), not from Outlet context.
 *
 * Terminal is NOT lazy-loaded — it's always-mounted in the App shell to
 * preserve WebSocket connections across view switches. Its route exists
 * solely for URL matching; the Component renders null.
 */
const viewRoutes = [
  { index: true, element: <Navigate to="kanban" replace /> },
  {
    path: "kanban",
    lazy: () =>
      import("@/views/KanbanPage").then((m) => ({ Component: m.KanbanPage })),
  },
  {
    path: "list",
    lazy: () =>
      import("@/views/ListPage").then((m) => ({ Component: m.ListPage })),
  },
  {
    path: "table",
    lazy: () =>
      import("@/views/TablePage").then((m) => ({ Component: m.TablePage })),
  },
  {
    path: "graph",
    lazy: () =>
      import("@/views/GraphPage").then((m) => ({ Component: m.GraphPage })),
  },
  {
    path: "monitor",
    lazy: () =>
      import("@/views/MonitorPage").then((m) => ({ Component: m.MonitorPage })),
  },
  {
    path: "observability",
    lazy: () =>
      import("@/views/ObservabilityPage").then((m) => ({
        Component: m.ObservabilityPage,
      })),
  },
  {
    path: "terminal",
    Component: () => null,
  },
  {
    path: "agents",
    lazy: () =>
      import("@/views/AgentsPage").then((m) => ({ Component: m.AgentsPage })),
  },
  {
    path: "prs",
    lazy: () =>
      import("@/views/PRsPage").then((m) => ({ Component: m.PRsPage })),
  },
  {
    path: "settings",
    lazy: () =>
      import("@/views/SettingsPage").then((m) => ({
        Component: m.SettingsPage,
      })),
  },
  {
    path: "workspace",
    lazy: () =>
      import("@/views/WorkspacePage").then((m) => ({
        Component: m.WorkspacePage,
      })),
  },
  {
    path: "files",
    lazy: () =>
      import("@/views/FilesPage").then((m) => ({ Component: m.FilesPage })),
  },
  {
    path: "issues/:issueId",
    lazy: () =>
      import("@/views/IssueDetailPage").then((m) => ({
        Component: m.IssueDetailPage,
      })),
  },
  {
    path: "agents/:agentName",
    lazy: () =>
      import("@/views/AgentsPage").then((m) => ({ Component: m.AgentsPage })),
  },
  { path: "*", element: <Navigate to="kanban" replace /> },
];

export const router = createBrowserRouter([
  {
    path: "/",
    element: <RedirectToWorkspace />,
  },
  {
    path: "/ws/:workspaceId",
    element: <WorkspaceLayout />,
    children: [
      {
        element: <App />,
        children: viewRoutes,
      },
    ],
  },
  ...devRoutes,
  {
    path: "*",
    element: <NotFound />,
  },
]);
