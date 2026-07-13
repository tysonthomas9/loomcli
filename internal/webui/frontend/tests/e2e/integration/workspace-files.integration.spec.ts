import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { expect, test, type APIRequestContext } from "@playwright/test";

const WORKSPACE_ID = "E2E-WS";
const FILES_BASE = `/api/workspaces/${WORKSPACE_ID}/files`;
const TEST_DIR = path.dirname(fileURLToPath(import.meta.url));
const CHECKOUT = path.resolve(
  TEST_DIR,
  "../../../../../../tmp/e2e-workspace/.loom-config/workspaces/e2e-ws/e2e-workspace",
);

async function writeFile(
  request: APIRequestContext,
  filePath: string,
  content: string,
  headers?: Record<string, string>,
) {
  return request.put(
    `${FILES_BASE}?scope=workspace&path=${encodeURIComponent(filePath)}`,
    { data: { content }, headers },
  );
}

async function seedWorkspace(request: APIRequestContext): Promise<void> {
  const mkdir = await request.post(
    `${FILES_BASE}/mkdir?scope=workspace&path=${encodeURIComponent(".loom/terminal-worktrees")}`,
  );
  expect(mkdir.ok()).toBeTruthy();

  for (const [filePath, content] of [
    [
      "e2e-workspace/README.md",
      "# Workspace Files E2E\n\nalpha searchable token\nsecond line\n",
    ],
    [
      "e2e-workspace/notes.txt",
      "alpha searchable token\nreplace alpha safely\n",
    ],
    [
      ".loom/terminal-worktrees/visible.txt",
      "intentionally visible namespace\n",
    ],
  ]) {
    const response = await writeFile(request, filePath, content);
    expect(response.ok(), await response.text()).toBeTruthy();
  }

  execFileSync("git", ["add", "README.md", "notes.txt"], { cwd: CHECKOUT });
  execFileSync("git", ["commit", "-m", "Add file browser E2E fixtures"], {
    cwd: CHECKOUT,
  });
}

test.describe.configure({ mode: "serial" });

test.describe("Real workspace file browser", () => {
  test.beforeAll(async ({ playwright }) => {
    const request = await playwright.request.newContext({
      baseURL: `http://127.0.0.1:${process.env.E2E_FRONTEND_PORT || "3100"}`,
    });
    try {
      await seedWorkspace(request);
    } finally {
      await request.dispose();
    }
  });

  test("@smoke enforces boundaries and versioned mutation contracts", async ({
    request,
  }) => {
    const capabilities = await request.get(`${FILES_BASE}/capabilities`);
    expect(capabilities.ok(), await capabilities.text()).toBeTruthy();
    expect(await capabilities.json()).toEqual({
      read: true,
      write: true,
      sensitive: true,
    });

    const root = await request.get(`${FILES_BASE}/tree?scope=workspace`);
    expect(root.ok(), await root.text()).toBeTruthy();
    expect((await root.json()).entries.map((entry: { name: string }) => entry.name)).toEqual(
      expect.arrayContaining([".loom", "e2e-workspace"]),
    );

    const repo = await request.get(
      `${FILES_BASE}/tree?scope=workspace&path=${encodeURIComponent("e2e-workspace")}`,
    );
    expect(repo.ok(), await repo.text()).toBeTruthy();
    const repoNames = (await repo.json()).entries.map(
      (entry: { name: string }) => entry.name,
    );
    expect(repoNames).toEqual(expect.arrayContaining(["README.md", "notes.txt"]));
    expect(repoNames).not.toContain(".git");

    const terminalWorktrees = await request.get(
      `${FILES_BASE}/tree?scope=workspace&path=${encodeURIComponent(".loom/terminal-worktrees")}`,
    );
    expect(terminalWorktrees.ok(), await terminalWorktrees.text()).toBeTruthy();
    expect((await terminalWorktrees.json()).entries).toEqual(
      expect.arrayContaining([expect.objectContaining({ name: "visible.txt" })]),
    );

    for (const hiddenPath of [
      "e2e-workspace/.git/config",
      "e2e-workspace/.GIT/config",
    ]) {
      const hidden = await request.get(
        `${FILES_BASE}?scope=workspace&path=${encodeURIComponent(hiddenPath)}`,
      );
      expect(hidden.status()).toBe(403);
    }

    const index = await request.get(`${FILES_BASE}/index?scope=workspace`);
    expect(index.ok(), await index.text()).toBeTruthy();
    expect((await index.json()).paths).toEqual(
      expect.arrayContaining([
        ".loom/terminal-worktrees/visible.txt",
        "e2e-workspace/README.md",
      ]),
    );

    const search = await request.post(`${FILES_BASE}/search?scope=workspace`, {
      data: { query: "alpha", regex: false, case_sensitive: false },
    });
    expect(search.ok(), await search.text()).toBeTruthy();
    expect((await search.json()).results).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: "e2e-workspace/README.md" }),
        expect.objectContaining({ path: "e2e-workspace/notes.txt" }),
      ]),
    );

    const created = await writeFile(
      request,
      "e2e-workspace/new-file.txt",
      "created once\n",
      { "If-None-Match": "*" },
    );
    expect(created.ok(), await created.text()).toBeTruthy();
    const duplicateCreate = await writeFile(
      request,
      "e2e-workspace/new-file.txt",
      "must not replace\n",
      { "If-None-Match": "*" },
    );
    expect(duplicateCreate.status()).toBe(412);

    const untrackedAtHead = await request.get(
      `${FILES_BASE}?scope=workspace&path=${encodeURIComponent("e2e-workspace/new-file.txt")}&rev=HEAD`,
    );
    expect(untrackedAtHead.status()).toBe(404);

    const stat = await request.get(
      `${FILES_BASE}/stat?scope=workspace&path=${encodeURIComponent("e2e-workspace/new-file.txt")}`,
    );
    expect(stat.ok(), await stat.text()).toBeTruthy();
    const sourceVersion = (await stat.json()).version as string;

    const missingMoveVersion = await request.patch(
      `${FILES_BASE}/move?scope=workspace`,
      { data: { from: "e2e-workspace/new-file.txt", to: "e2e-workspace/moved.txt" } },
    );
    expect(missingMoveVersion.status()).toBe(428);

    const moved = await request.patch(`${FILES_BASE}/move?scope=workspace`, {
      data: {
        from: "e2e-workspace/new-file.txt",
        to: "e2e-workspace/moved.txt",
        source_version: sourceVersion,
      },
    });
    expect(moved.ok(), await moved.text()).toBeTruthy();

    const missingDeleteVersion = await request.delete(
      `${FILES_BASE}?scope=workspace&path=${encodeURIComponent("e2e-workspace/moved.txt")}`,
    );
    expect(missingDeleteVersion.status()).toBe(428);

    const movedStat = await request.get(
      `${FILES_BASE}/stat?scope=workspace&path=${encodeURIComponent("e2e-workspace/moved.txt")}`,
    );
    const movedVersion = (await movedStat.json()).version as string;
    const deleted = await request.delete(
      `${FILES_BASE}?scope=workspace&path=${encodeURIComponent("e2e-workspace/moved.txt")}`,
      { headers: { "If-Match": `"${movedVersion}"` } },
    );
    expect(deleted.ok(), await deleted.text()).toBeTruthy();

    const history = await request.get(
      `${FILES_BASE}/history?scope=workspace&path=${encodeURIComponent("e2e-workspace/README.md")}`,
    );
    expect(history.ok(), await history.text()).toBeTruthy();
    expect((await history.json()).entries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ summary: "Add file browser E2E fixtures" }),
      ]),
    );

    const hostileOrigin = await request.get(
      `http://127.0.0.1:${process.env.E2E_PORT || "8090"}${FILES_BASE}/capabilities`,
      { headers: { Origin: "http://attacker.example" } },
    );
    expect(hostileOrigin.status()).toBe(403);
  });

  test("@regression edits, searches, and detects an external dirty conflict", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`/ws/${WORKSPACE_ID}/files`);

    await expect(page.getByText("File permissions unavailable")).toHaveCount(0);
    await page.getByRole("button", { name: "e2e-workspace" }).click();
    await page.getByText("README.md", { exact: true }).click();

    const editor = page.getByRole("textbox").last();
    await expect(editor).toContainText("alpha searchable token");
    await editor.click();
    await page.keyboard.press("Control+End");
    await page.keyboard.type("browser save line");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Modified", { exact: true })).toHaveCount(0);

    await page.keyboard.press("Control+Shift+f");
    await page.getByRole("textbox", { name: "Search files" }).fill("alpha");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByText("e2e-workspace/README.md")).toBeVisible();
    await page.getByRole("button", { name: "Close search" }).click();

    await editor.click();
    await page.keyboard.press("Control+End");
    await page.keyboard.type(" dirty local draft");
    execFileSync("sh", ["-c", "printf 'external terminal content\\n' > README.md"], {
      cwd: CHECKOUT,
    });
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));

    await expect(
      page.getByText("This file changed outside the editor."),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Reload" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Compare" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Overwrite" })).toBeVisible();
    await page.getByRole("button", { name: "Reload" }).click();
    await expect(editor).toContainText("external terminal content");

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByLabel("File history")).toContainText(
      "Add file browser E2E fixtures",
    );
  });
});
