/**
 * 17 Files + diff fleet-db acceptance — verifies workspace-scoped file and
 * git diff APIs keep working when fleet-db owns the workspace metadata.
 */
import { execSync } from "node:child_process";
import type { Page } from "@playwright/test";
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import { composeRuntime } from "./_support/compose";
import { discoverWorkspaceId } from "./_support";
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

type RouteHitRecorder = (url: string) => void;

const AGENT = "workspace";

test.describe("17 files and git diff fleet-db acceptance", () => {
  test.describe.configure({ timeout: 150_000 });

  test("file tree/read/write and git diff routes work on fleet workspace mapping", async ({
    tabs,
    recordRouteHit,
  }) => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(FLEETDB_URLS.reference),
      discoverWorkspaceId(FLEETDB_URLS.fleet),
    ]);

    await Promise.all([
      tabs.reference.goto(`${FLEETDB_URLS.reference}/ws/${referenceWs}/files`),
      tabs.fleet.goto(`${FLEETDB_URLS.fleet}/ws/${fleetWs}/files`),
    ]);
    await Promise.all([
      assertFilesView(tabs.reference),
      assertFilesView(tabs.fleet),
    ]);

    const filePath = `fleetdb-regression-file-${Date.now()}.txt`;
    const content = `fleet-db file fleetdb-regression ${new Date().toISOString()}\n`;

    const root = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/files/tree`,
      recordRouteHit,
    );
    expect(root.path, "file tree root path").toBe(".");
    expect(
      root.entries?.some((entry: any) => entry.name === ".init"),
    ).toBeTruthy();

    await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/files?path=${encodeURIComponent(filePath)}`,
      recordRouteHit,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ content }),
      },
    );

    const readBack = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/files?path=${encodeURIComponent(filePath)}`,
      recordRouteHit,
    );
    expect(readBack.content, "file read content").toBe(content);
    expect(readBack.binary, "file read binary flag").toBe(false);

    const rootAfterWrite = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/files/tree`,
      recordRouteHit,
    );
    expect(
      rootAfterWrite.entries?.some((entry: any) => entry.name === filePath),
      "written file appears in tree",
    ).toBeTruthy();

    const status = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/git/status`,
      recordRouteHit,
    );
    const targetBranch = status.target_branch || "main";
    const commitSubject = `fleetdb-regression files diff ${Date.now()}`;
    commitFleetWorktreeChange(targetBranch, filePath, commitSubject);

    const diffFiles = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/diff/files?to=HEAD&from=${encodeURIComponent(targetBranch)}`,
      recordRouteHit,
    );
    const changedFiles = diffFiles.data?.files ?? [];
    const changed = changedFiles.find((file: any) => file.path === filePath);
    expect(changed, "diff file entry").toBeTruthy();
    expect(changed.status, "diff file status").toBe("A");
    expect(changed.additions, "diff file additions").toBeGreaterThan(0);

    const patch = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/diff/file?path=${encodeURIComponent(filePath)}&to=HEAD&from=${encodeURIComponent(targetBranch)}`,
      recordRouteHit,
    );
    expect(patch.data?.patch, "diff patch").toContain(content.trim());
    expect(patch.data?.is_binary, "diff patch binary flag").toBe(false);

    const commits = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${AGENT}/diff/commits?from=${encodeURIComponent(targetBranch)}&limit=5`,
      recordRouteHit,
    );
    const commitRows = commits.data?.commits ?? [];
    expect(
      commitRows.some((commit: any) => commit.subject === commitSubject),
      "diff commits include prepared file commit",
    ).toBeTruthy();
  });
});

async function assertFilesView(page: Page): Promise<void> {
  await page.waitForLoadState("domcontentloaded").catch(() => undefined);
  await expect(page.getByLabel("Select agent")).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByLabel("Filter files")).toBeVisible({
    timeout: 15_000,
  });
}

function commitFleetWorktreeChange(
  targetBranch: string,
  filePath: string,
  commitSubject: string,
): void {
  const worktree = "/root/.loom/workspaces/FLEETDB/workspace";
  const command = [
    `cd ${shellQuote(worktree)}`,
    `git config user.email ${shellQuote("fleetdb-regression@fixture.local")}`,
    `git config user.name ${shellQuote("fleetdb-regression")}`,
    `git show-ref --verify --quiet refs/heads/${shellQuoteRef(targetBranch)} || git branch ${shellQuote(targetBranch)} HEAD`,
    `git add -- ${shellQuote(filePath)}`,
    `git commit -q -m ${shellQuote(commitSubject)} -- ${shellQuote(filePath)}`,
  ].join(" && ");
  execSync(
    `${composeRuntime()} exec loomcli-fleetdb-regression_loom-fleet_1 sh -lc ${shellQuote(command)}`,
    {
      encoding: "utf-8",
      timeout: 15_000,
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function shellQuoteRef(value: string): string {
  if (!/^[A-Za-z0-9._/-]+$/.test(value) || value.includes("..")) {
    throw new Error(`unsafe git ref: ${value}`);
  }
  return value;
}

async function apiJson(
  url: string,
  recordRouteHit: RouteHitRecorder,
  init?: RequestInit,
): Promise<any> {
  recordRouteHit(url);
  const response = await fetchReachable(url, init);
  expect(response.ok, `${url} status=${response.status}`).toBeTruthy();
  return response.json();
}

async function fetchReachable(
  url: string,
  init?: RequestInit,
): Promise<Response> {
  let last: Response | null = null;
  for (let attempt = 0; attempt < 8; attempt++) {
    const response = await fetch(url, init);
    last = response;
    if (response.ok || ![429, 500, 503].includes(response.status)) {
      return response;
    }
    await response.body?.cancel().catch(() => undefined);
    await new Promise((resolve) => setTimeout(resolve, 500 + attempt * 250));
  }
  return last ?? fetch(url, init);
}
