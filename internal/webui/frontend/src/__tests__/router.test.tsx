/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the lazy-loaded route configuration in router.tsx.
 *
 * Verifies that each view route under /ws/:workspaceId resolves to the
 * correct Component via React Router's lazy(), that the terminal route
 * uses a non-lazy Component rendering null, and that index/catch-all
 * routes redirect to kanban.
 */

import { describe, it, expect, vi } from "vitest";

// ---------------------------------------------------------------------------
// Mock every lazy-loaded view page so we don't pull in real component trees.
// Each mock exports the named function that router.tsx references in its
// .then(m => ({ Component: m.XPage })) callback.
// ---------------------------------------------------------------------------

const KanbanPage = () => null;
vi.mock("@/views/boards/KanbanPage", () => ({ KanbanPage }));

const TablePage = () => null;
vi.mock("@/views/boards/TablePage", () => ({ TablePage }));

const GraphPage = () => null;
vi.mock("@/views/boards/GraphPage", () => ({ GraphPage }));

const MonitorPage = () => null;
vi.mock("@/views/MonitorPage", () => ({ MonitorPage }));

const ObservabilityPage = () => null;
vi.mock("@/views/ObservabilityPage", () => ({ ObservabilityPage }));

const TracesPage = () => null;
vi.mock("@/views/TracesPage", () => ({ TracesPage }));

const SettingsPage = () => null;
vi.mock("@/views/SettingsPage", () => ({ SettingsPage }));

const WorkspacePage = () => null;
vi.mock("@/views/WorkspacePage", () => ({ WorkspacePage }));

const FilesPage = () => null;
vi.mock("@/views/FilesPage", () => ({ FilesPage }));

const IssueDetailPage = () => null;
vi.mock("@/views/IssueDetailPage", () => ({ IssueDetailPage }));

// Mock heavy dependencies pulled in by the router module
vi.mock("@/App", () => ({ default: () => null }));
vi.mock("@/components/WorkspaceLayout", () => ({
  WorkspaceLayout: () => null,
}));
vi.mock("@/components/RedirectToWorkspace", () => ({
  RedirectToWorkspace: () => null,
}));
vi.mock("@/components/NotFound", () => ({ NotFound: () => null }));

// Mock createBrowserRouter so it just returns the route config array as-is,
// allowing us to inspect the route tree without needing a real DOM router.
vi.mock("react-router-dom", async () => {
  const actual =
    await vi.importActual<typeof import("react-router-dom")>(
      "react-router-dom",
    );
  return {
    ...actual,
    createBrowserRouter: (routes: unknown[]) => routes,
  };
});

import { router } from "@/router";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type RouteObject = {
  path?: string;
  index?: boolean;
  element?: React.ReactElement;
  lazy?: () => Promise<{ Component: React.ComponentType }>;
  Component?: React.ComponentType;
  children?: RouteObject[];
};

/**
 * Extract the viewRoutes children from the router config.
 * Structure: router[1] = /ws/:workspaceId route, its children[0] = App
 * layout route (pathless), whose children are the viewRoutes.
 */
function getViewRoutes(): RouteObject[] {
  const routes = router as unknown as RouteObject[];
  const wsRoute = routes.find((r) => r.path === "/ws/:workspaceId");
  expect(wsRoute).toBeDefined();
  const appLayoutRoute = wsRoute!.children![0];
  expect(appLayoutRoute.children).toBeDefined();
  return appLayoutRoute.children!;
}

function findRoute(path: string): RouteObject {
  const route = getViewRoutes().find((r) => r.path === path);
  expect(route).toBeDefined();
  return route!;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("router view routes", () => {
  describe("route paths match expected patterns", () => {
    it("contains all expected view paths", () => {
      const viewRoutes = getViewRoutes();
      const paths = viewRoutes.filter((r) => r.path).map((r) => r.path);

      expect(paths).toContain("kanban");
      expect(paths).toContain("table");
      expect(paths).toContain("graph");
      expect(paths).toContain("monitor");
      expect(paths).toContain("observability");
      expect(paths).toContain("traces");
      expect(paths).toContain("traces/runs/:taskRunId");
      expect(paths).toContain("terminal");
      expect(paths).toContain("settings");
      expect(paths).toContain("workspace");
      expect(paths).toContain("files");
      expect(paths).toContain("issues/:issueId");
      expect(paths).toContain("*");
    });

    it("has an index route", () => {
      const indexRoute = getViewRoutes().find((r) => r.index === true);
      expect(indexRoute).toBeDefined();
    });
  });

  describe("lazy routes resolve to the correct Component", () => {
    const lazyCases: [string, React.ComponentType][] = [
      ["kanban", KanbanPage],
      ["table", TablePage],
      ["graph", GraphPage],
      ["monitor", MonitorPage],
      ["observability", ObservabilityPage],
      ["traces", TracesPage],
      ["traces/runs/:taskRunId", TracesPage],
      ["settings", SettingsPage],
      ["workspace", WorkspacePage],
      ["files", FilesPage],
      ["issues/:issueId", IssueDetailPage],
    ];

    it.each(lazyCases)(
      "%s route lazy() resolves to its page component",
      async (path, expectedComponent) => {
        const route = findRoute(path);
        expect(route.lazy).toBeDefined();
        expect(route.Component).toBeUndefined();

        const result = await route.lazy!();
        expect(result.Component).toBe(expectedComponent);
      },
    );
  });

  describe("terminal route", () => {
    it("has a Component (not lazy) that renders null", () => {
      const route = findRoute("terminal");
      expect(route.lazy).toBeUndefined();
      expect(route.Component).toBeDefined();
      expect(route.Component!({} as never)).toBeNull();
    });
  });

  describe("index route redirects to kanban", () => {
    it("renders a Navigate element to kanban with replace", () => {
      const indexRoute = getViewRoutes().find((r) => r.index === true);
      expect(indexRoute).toBeDefined();
      expect(indexRoute!.element).toBeDefined();

      const element = indexRoute!.element as React.ReactElement;
      expect(element.props.to).toBe("kanban");
      expect(element.props.replace).toBe(true);
    });
  });

  describe("catch-all route redirects to kanban", () => {
    it("renders a Navigate element to kanban with replace", () => {
      const catchAll = findRoute("*");
      expect(catchAll.element).toBeDefined();

      const element = catchAll.element as React.ReactElement;
      expect(element.props.to).toBe("kanban");
      expect(element.props.replace).toBe(true);
    });
  });
});

describe("top-level routes", () => {
  it("has a root route that renders RedirectToWorkspace", () => {
    const routes = router as unknown as RouteObject[];
    const rootRoute = routes.find((r) => r.path === "/");
    expect(rootRoute).toBeDefined();
    expect(rootRoute!.element).toBeDefined();
  });

  it("has a workspace route at /ws/:workspaceId", () => {
    const routes = router as unknown as RouteObject[];
    const wsRoute = routes.find((r) => r.path === "/ws/:workspaceId");
    expect(wsRoute).toBeDefined();
  });

  it("has a catch-all NotFound route", () => {
    const routes = router as unknown as RouteObject[];
    const notFoundRoute = routes.find((r) => r.path === "*" && !r.children);
    expect(notFoundRoute).toBeDefined();
    expect(notFoundRoute!.element).toBeDefined();
  });
});
