/**
 * React Router configuration — single source of truth for all routes.
 *
 * Route tree:
 *   /                                → RedirectToWorkspace (resolve last-used or default)
 *   /ws/:workspaceId/                → WorkspaceLayout → App (default view = kanban)
 *   /ws/:workspaceId/issues/:issueId → WorkspaceLayout → App (issue-detail view)
 *   /test/*                          → TestFixtures (dev only, preserved)
 *   *                                → NotFound (404 page)
 */

import { createBrowserRouter } from "react-router-dom";

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
          } = await import("@/TestFixtures");
          return {
            Component: () => {
              const path = window.location.pathname;
              if (path === "/test/issue-detail-panel")
                return <IssueDetailPanelFixture />;
              if (path === "/test/error-boundary")
                return <ErrorTriggerFixture />;
              if (path === "/test/toast") return <ToastTestFixture />;
              return <NotFound />;
            },
          };
        },
      },
    ]
  : [];

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
        index: true,
        element: <App />,
      },
      {
        path: "issues/:issueId",
        element: <App />,
      },
    ],
  },
  ...devRoutes,
  {
    path: "*",
    element: <NotFound />,
  },
]);
