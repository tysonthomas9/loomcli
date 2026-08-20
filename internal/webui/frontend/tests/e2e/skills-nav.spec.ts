import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for the Skills nav section.
 *
 * Skills used to be a root row inside the Files explorer. They now have a
 * top-level destination of their own, backed by the same WorkspaceFileBrowser
 * in a "skills" mode. These tests pin the three properties that separation is
 * supposed to buy, against mocked APIs:
 *
 *   1. the rail exposes Skills, between Files and Settings;
 *   2. the Skills section shows skill roots and nothing borrowed from Files;
 *   3. the Files explorer no longer shows skills at all.
 *
 * The real-stack counterpart lives in integration/skills.integration.spec.ts,
 * which additionally drives the editor against fleet-db. This file stays
 * mocked so it can run on every pull request without a backend.
 */

const WS = "ws-skills";

// -- Mock data --

const mockWorkspaceData = {
  id: WS,
  name: "skills-test",
  path: "/workspaces/skills-test",
  repos: [
    {
      name: "repo-one",
      path: "/workspaces/skills-test/repo-one",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS,
      name: "skills-test",
      path: "/workspaces/skills-test",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: [WS],
  default_workspace: WS,
};

const ROLE = "task";

const mockSkillsCatalog = {
  groups: [
    { scope: "workspace", skills: [] },
    {
      scope: "role",
      role: ROLE,
      skills: [
        {
          name: "ui-demo",
          scope: "role",
          role: ROLE,
          description: "UI demo skill",
          content_revision: "rev-1",
          files: [{ path: "SKILL.md", revision: "rev-1", executable: false }],
          created_at: "2026-01-15T10:00:00Z",
          updated_at: "2026-01-15T10:00:00Z",
        },
      ],
    },
  ],
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

// -- Setup --

async function setupMocks(page: Page) {
  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/backends", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([
        {
          name: "shell",
          available: true,
          display_name: "Shell",
          configured: true,
        },
      ]),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  // Broad workspace handler. Registered before the skills routes below so that
  // those win: Playwright matches route handlers in reverse registration order.
  await page.route("**/api/workspaces/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;

    const json = (body: string, contentType = "application/json") =>
      route.fulfill({ status: 200, contentType, body });

    if (pathname === "/api/workspaces/active")
      return json(ok(mockWorkspaceData));

    if (pathname.match(/^\/api\/workspaces\/[^/]+\/?$/)) {
      return json(ok(mockWorkspaceData));
    }

    if (pathname.match(/\/api\/workspaces\/[^/]+\/events/)) {
      return route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
        body: 'event: connected\ndata: {"message":"connected"}\n\n',
      });
    }

    if (pathname.includes("/config/backend")) {
      return json(
        ok({
          backend: "shell",
          source: "workspace",
          available: ["shell"],
          agents: [],
        }),
      );
    }

    if (pathname.match(/\/terminal\/state$/)) {
      return json(JSON.stringify({ active_tab: "" }));
    }

    // The Files explorer reads these two directly rather than through the
    // success envelope, and throws into an error boundary if either is the
    // wrong shape — an empty array here blanks the whole page.
    if (pathname.match(/\/files\/checkouts$/)) {
      return json(JSON.stringify({ checkouts: [] }));
    }

    if (pathname.match(/\/files\/capabilities$/)) {
      return json(
        JSON.stringify({ read: true, write: true, sensitive: false }),
      );
    }

    // Everything else the two explorers poll — file trees, terminal tabs,
    // issues — is empty rather than missing, so neither section renders an
    // error state that would mask the assertions below.
    return json(ok([]));
  });

  await page.route("**/api/workspaces/*/skills", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockSkillsCatalog),
    });
  });

  await page.route("**/api/workspaces/*/skill-capabilities", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        can_edit_role_scope: true,
        workspace_scope: "read_only",
      }),
    });
  });
}

function getNavRail(page: Page) {
  return page.locator('nav[aria-label="Primary"]');
}

// exact: true matters — the rail also renders a workspace avatar button whose
// label ("Switch to skills-test") substring-matches "Skills".
function getNavButton(page: Page, label: string) {
  return getNavRail(page).getByRole("button", { name: label, exact: true });
}

/** The primary view buttons, in DOM order. */
const VIEW_BUTTONS = [
  "Workspaces",
  "Pull Requests",
  "Terminal",
  "Files",
  "Skills",
  "Settings",
];

/** Tree section headings are <h2>; the <h1> breadcrumb also reads "Skills". */
function sectionHeading(page: Page, name: string) {
  return page.getByRole("heading", { level: 2, name, exact: true });
}

async function goto(page: Page, view: string) {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/ws/${WS}/${view}`, { waitUntil: "domcontentloaded" });
  await expect(getNavRail(page)).toBeVisible();
}

// -- Tests --

test.describe("Skills nav section", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("rail exposes Skills between Files and Settings", async ({ page }) => {
    await goto(page, "kanban");

    // Filtered rather than indexed: the rail interleaves workspace avatar and
    // "Add workspace" buttons among the view buttons.
    const labels = await getNavRail(page)
      .getByRole("button")
      .evaluateAll((els) =>
        els.map((el) => el.getAttribute("aria-label") ?? ""),
      );
    expect(labels.filter((l) => VIEW_BUTTONS.includes(l))).toEqual(
      VIEW_BUTTONS,
    );
  });

  test("clicking Skills routes to the section", async ({ page }) => {
    await goto(page, "kanban");

    await getNavButton(page, "Skills").click();

    await expect(page).toHaveURL(new RegExp(`/ws/${WS}/skills`));
    await expect(getNavButton(page, "Skills")).toHaveAttribute(
      "data-active",
      "true",
    );
    await expect(getNavButton(page, "Files")).not.toHaveAttribute(
      "data-active",
    );
  });

  test("section shows skill roots and nothing borrowed from Files", async ({
    page,
  }) => {
    await goto(page, "skills");

    await expect(sectionHeading(page, "Skills")).toBeVisible();
    // The catalog's role group becomes a root.
    await expect(
      page.getByRole("button", { name: new RegExp(`^${ROLE}`) }),
    ).toBeVisible();

    for (const absent of ["Agents", "Repos", "Workspace files"]) {
      await expect(sectionHeading(page, absent)).toHaveCount(0);
    }
  });

  test("section has no Files/Changes lens", async ({ page }) => {
    await goto(page, "skills");
    await expect(sectionHeading(page, "Skills")).toBeVisible();

    // Changes is a second view of a checkout, and no checkout sits behind this
    // section — so the toggle is not rendered and the lens cannot be reached.
    await expect(
      page.getByRole("tablist", { name: "File explorer lens" }),
    ).toHaveCount(0);
    await expect(page.getByRole("tab", { name: /^Changes/ })).toHaveCount(0);
  });

  test("workspace scope is read-only", async ({ page }) => {
    await goto(page, "skills");

    const workspaceAdd = page.getByRole("button", {
      name: "New skill in Workspace",
    });
    await expect(workspaceAdd).toBeVisible();
    await expect(workspaceAdd).toBeDisabled();
  });

  test("Files explorer no longer shows skills", async ({ page }) => {
    await goto(page, "files");

    // Positive anchor first: the Files explorer really did render, so the
    // absence assertion below is about skills and not about a blank page.
    await expect(sectionHeading(page, "Workspace files")).toBeVisible();
    await expect(sectionHeading(page, "Skills")).toHaveCount(0);
  });

  test("the two sections keep separate tab sets", async ({ page }) => {
    await goto(page, "skills");
    await expect(sectionHeading(page, "Skills")).toBeVisible();

    // Open a skill file in the Skills section.
    await page.getByRole("button", { name: new RegExp(`^${ROLE}`) }).click();
    await page.getByText("ui-demo", { exact: true }).click();
    await page.getByText("SKILL.md", { exact: true }).click();
    await expect(page.getByRole("tab", { name: /SKILL\.md/ })).toBeVisible();

    // The Files explorer does not inherit it.
    await getNavButton(page, "Files").click();
    await expect(sectionHeading(page, "Workspace files")).toBeVisible();
    await expect(page.getByRole("tab", { name: /SKILL\.md/ })).toHaveCount(0);

    // And the Skills section still has it on return.
    await getNavButton(page, "Skills").click();
    await expect(page.getByRole("tab", { name: /SKILL\.md/ })).toBeVisible();
  });
});
