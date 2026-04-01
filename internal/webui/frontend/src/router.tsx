/**
 * React Router configuration — single source of truth for all routes.
 *
 * Route tree:
 *   /                                      → RedirectToWorkspace (resolve last-used or default)
 *   /ws/:workspaceId/                      → WorkspaceLayout → App (shell/layout)
 *     /kanban                              → KanbanPage (via App switch)
 *     /table                               → TablePage
 *     /graph                               → GraphPage
 *     /monitor                             → MonitorPage
 *     /observability                       → ObservabilityPage
 *     /terminal                            → TerminalView (always-mounted in shell)
 *     /settings                            → SettingsPage
 *     /workspace                           → WorkspacePage
 *     /files                               → FilesPage
 *     /issues/:issueId                     → IssueDetailPage
 *   /test/*                                → TestFixtures (dev only, preserved)
 *   *                                      → NotFound (404 page)
 */

import { createBrowserRouter, Navigate } from "react-router-dom";

import App from "@/App";
import { WorkspaceLayout } from "@/components/WorkspaceLayout";
import { RedirectToWorkspace } from "@/components/RedirectToWorkspace";
import { NotFound } from "@/components/NotFound";

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
            WelcomeBannerFixture,
            HelpPopoverFixture,
            SearchBarFixture,
            AgentsSidebarFixture,
            WorkspaceTreeFixture,
            SplitDetailSummaryFixture,
            PasteConfirmDialogFixture,
          } = await import("@/TestFixtures");
          return {
            Component: () => {
              const path = window.location.pathname;
              if (path === "/test/issue-detail-panel")
                return <IssueDetailPanelFixture />;
              if (path === "/test/error-boundary")
                return <ErrorTriggerFixture />;
              if (path === "/test/toast") return <ToastTestFixture />;
              if (path === "/test/session-name-prompt")
                return <SessionNamePromptFixture />;
              if (path === "/test/welcome-banner")
                return <WelcomeBannerFixture />;
              if (path === "/test/help-popover") return <HelpPopoverFixture />;
              if (path === "/test/search-bar") return <SearchBarFixture />;
              if (path === "/test/agents-sidebar")
                return <AgentsSidebarFixture />;
              if (path === "/test/workspace-tree")
                return <WorkspaceTreeFixture />;
              if (path === "/test/split-detail-summary")
                return <SplitDetailSummaryFixture />;
              if (path === "/test/paste-confirm")
                return <PasteConfirmDialogFixture />;
              return <NotFound />;
            },
          };
        },
      },
    ]
  : [];

/**
 * View route children under /ws/:workspaceId.
 * App acts as the layout shell (pathless route); child routes provide URL
 * segments for matching. App derives the active view from the route path
 * and renders the corresponding view component.
 */
const viewRoutes = [
  { index: true, element: <Navigate to="kanban" replace /> },
  { path: "kanban" },
  { path: "table" },
  { path: "graph" },
  { path: "monitor" },
  { path: "observability" },
  { path: "terminal" },
  { path: "settings" },
  { path: "workspace" },
  { path: "files" },
  { path: "issues/:issueId" },
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
