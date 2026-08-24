/**
 * Workflows management view — mocked E2E (DEV-V5-40).
 *
 * Drives the authoring / approve / activate / rollback / adopt-update lifecycle
 * against mocked HTTP so it runs under the default (no-backend) chromium project.
 */

import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

import { setupFleetMocks, workspacePath } from "./helpers/fleet";

const WORKFLOWS = {
  workflows: [
    {
      driver_id: "epic-runner",
      name: "epic-runner",
      status: "active",
      active_version_id: "epic-runner-v-2",
      built_in: true,
      approved: true,
      effective_trust: "trusted",
      provenance: "packaged_builtin",
    },
    {
      driver_id: "my-flow",
      name: "my-flow",
      status: "draft",
      built_in: false,
      approved: false,
    },
  ],
};

const EPIC_VERSIONS = {
  driver_id: "epic-runner",
  versions: [
    {
      version: { version_id: "epic-runner-v-2", driver_id: "epic-runner", version: 2 },
      active: true,
      approved: true,
      effective_trust: "trusted",
      provenance: "packaged_builtin",
      selected_by: "system",
      bundle_verified: true,
    },
    {
      version: { version_id: "epic-runner-v-1", driver_id: "epic-runner", version: 1 },
      active: false,
      approved: false,
      effective_trust: "trusted",
      provenance: "packaged_builtin",
      bundle_verified: true,
    },
  ],
  builtin: {
    packaged_version_id: "epic-runner-v-3",
    packaged_source_digest: "sha256:cafebabecafebabe",
    packaged_artifact_digest: "sha256:art",
    track: "pinned",
    update_available: true,
    previous_active_version_id: "",
  },
};

// installWorkflowMocks intercepts the workflow endpoints; it is registered after
// setupFleetMocks so Playwright checks it first for matching URLs.
async function installWorkflowMocks(page: Page, posts: string[]) {
  await page.route("**/api/workspaces/default/workflows**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    const json = (data: unknown) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(data),
      });

    if (method === "GET" && path === "/api/workspaces/default/workflows") {
      return json(WORKFLOWS);
    }
    if (
      method === "GET" &&
      path === "/api/workspaces/default/workflows/epic-runner/versions"
    ) {
      return json(EPIC_VERSIONS);
    }
    if (method === "GET" && path.endsWith("/versions")) {
      return json({ driver_id: "my-flow", versions: [] });
    }
    if (method === "POST") {
      posts.push(path);
      return json({
        driver: { driver_id: "epic-runner", name: "epic-runner", status: "active" },
        version: { version_id: "epic-runner-v-1", driver_id: "epic-runner", version: 1 },
        active: true,
        approved: true,
        effective_trust: "trusted",
      });
    }
    return route.fallback();
  });
}

test.describe("Workflows view", () => {
  test("lists workflows, shows versions, and drives the lifecycle actions", async ({
    page,
  }) => {
    const posts: string[] = [];
    await setupFleetMocks(page, []);
    await installWorkflowMocks(page, posts);

    await page.goto(workspacePath("/ws/default/workflows"));
    await page.waitForLoadState("domcontentloaded");

    // The dashboard + list render, both workflows present.
    await expect(page.getByTestId("workflows-dashboard")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId("workflow-item-epic-runner")).toBeVisible();
    await expect(page.getByTestId("workflow-item-my-flow")).toBeVisible();

    // epic-runner auto-selected → versions table + both rows.
    await expect(page.getByTestId("versions-table")).toBeVisible();
    await expect(page.getByTestId("version-row-epic-runner-v-2")).toBeVisible();
    await expect(page.getByTestId("version-row-epic-runner-v-1")).toBeVisible();

    // Built-in update banner is shown (pinned track, update available).
    await expect(page.getByTestId("builtin-update-banner")).toHaveAttribute(
      "data-variant",
      "update",
    );

    // Approve the unapproved v1.
    await page.getByTestId("approve-epic-runner-v-1").click();
    await expect
      .poll(() => posts.some((p) => p.endsWith("/versions/epic-runner-v-1/approve")))
      .toBe(true);

    // Activate v1.
    await page.getByTestId("activate-epic-runner-v-1").click();
    await expect
      .poll(() => posts.some((p) => p.endsWith("/versions/epic-runner-v-1/activate")))
      .toBe(true);

    // Adopt the built-in update (auto-track sync).
    await page.getByTestId("adopt-builtin-update").click();
    await expect
      .poll(() => posts.some((p) => p.endsWith("/builtin/sync")))
      .toBe(true);
  });

  test("selecting a custom workflow shows its (empty) version history", async ({
    page,
  }) => {
    await setupFleetMocks(page, []);
    await installWorkflowMocks(page, []);

    await page.goto(workspacePath("/ws/default/workflows"));
    await page.waitForLoadState("domcontentloaded");

    await page.getByTestId("workflow-item-my-flow").click();
    await expect(page.getByTestId("versions-empty")).toBeVisible();
    await expect(page.getByTestId("author-version-panel")).toBeVisible();
  });

  test("is reachable by clicking the Workflows nav-rail button", async ({
    page,
  }) => {
    await setupFleetMocks(page, []);
    await installWorkflowMocks(page, []);

    // Start on the default (kanban) view, then navigate via the primary nav rail.
    await page.goto(workspacePath("/"));
    await page.waitForLoadState("domcontentloaded");

    const nav = page.getByRole("navigation", { name: "Primary" });
    await nav.getByRole("button", { name: "Workflows" }).click();

    await expect(page.getByTestId("workflows-dashboard")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page).toHaveURL(/\/workflows$/);
  });
});
