import { expect, test, type APIRequestContext } from "@playwright/test";

// Skills tree e2e — mirrors workspace-files.integration.spec.ts. Runs against
// the self-contained, fleet-db-backed loom serve started by
// scripts/start-e2e-server.sh (skipped in LOOM_LOCAL_SERVER mode).
//
// @smoke pins the HTTP contract that needs NO fleet-db provisioning — the A3
// workspace-read-only refusals and the role-name traversal guard (both are
// rejected in the loomcli handler before the store is touched) plus the
// capability/catalog/CORS surface.
//
// The role-scoped tests (CRUD + the Skills section UI) require a role that
// EXISTS in fleet-db: `checkScopeTarget` refuses a role-scoped skill whose
// role is not projected (the same referential-integrity wiring AgentService
// uses). `task` is one of the built-in roles seeded for every workspace
// (seedBuiltInRoles: plan/task/lead), so it is present in the self-contained
// harness. The tests still probe and skip cleanly if a harness ever omits it.

const WORKSPACE_ID = "E2E-WS";
const API = `/api/workspaces/${WORKSPACE_ID}`;
const ROLE = "task"; // a built-in role (seedBuiltInRoles), present per workspace
const ROLE_SCOPE_UNAVAILABLE = `role scope unavailable: the ${ROLE} role is not projected in fleet-db for this workspace`;

const quoted = (revision: string): string =>
  revision.startsWith('"') ? revision : `"${revision}"`;

async function deleteRoleSkillIfPresent(
  request: APIRequestContext,
  role: string,
  name: string,
): Promise<void> {
  const detail = await request.get(`${API}/roles/${role}/skills/${name}`);
  if (detail.status() === 404) return;
  expect(detail.ok(), await detail.text()).toBeTruthy();
  const revision = (await detail.json()).content_revision as string;
  const deleted = await request.delete(`${API}/roles/${role}/skills/${name}`, {
    headers: { "If-Match": quoted(revision) },
  });
  expect(deleted.ok(), await deleted.text()).toBeTruthy();
}

// Attempts to create a role skill; returns false (and cleans up) when the
// backing store refuses because the role is not projected in fleet-db.
async function ensureRoleSkill(
  request: APIRequestContext,
  name: string,
  description: string,
  content: string,
): Promise<boolean> {
  await deleteRoleSkillIfPresent(request, ROLE, name);
  const created = await request.post(`${API}/roles/${ROLE}/skills`, {
    data: { name, description, content },
  });
  if (created.status() === 404) return false; // role/workspace not projected
  expect(created.status(), await created.text()).toBe(201);
  return true;
}

test.describe.configure({ mode: "serial" });

test.describe("Skills tree", () => {
  test.skip(
    !!process.env.LOOM_LOCAL_SERVER,
    "requires self-contained E2E workspace",
  );

  test("@smoke enforces the read-only scope and role-name guards", async ({
    request,
  }) => {
    const capabilities = await request.get(`${API}/skill-capabilities`);
    expect(capabilities.ok(), await capabilities.text()).toBeTruthy();
    expect(await capabilities.json()).toEqual({
      can_edit_role_scope: true,
      workspace_scope: "read_only",
    });

    // The catalog is always readable and grouped by scope.
    const catalog = await request.get(`${API}/skills`);
    expect(catalog.ok(), await catalog.text()).toBeTruthy();
    expect(Array.isArray((await catalog.json()).groups)).toBeTruthy();

    // A3: every workspace-scope mutation is refused with a stable code, before
    // the store is consulted — create, patch, delete, and per-file PUT.
    const workspaceCreate = await request.post(`${API}/skills`, {
      data: { name: "nope", description: "should fail" },
    });
    expect(workspaceCreate.status()).toBe(403);
    expect((await workspaceCreate.json()).code).toBe("workspace_scope_readonly");

    const workspacePatch = await request.patch(`${API}/skills/whatever`, {
      data: { description: "should fail" },
      headers: { "If-Match": '"x"' },
    });
    expect(workspacePatch.status()).toBe(403);

    const workspaceDelete = await request.delete(`${API}/skills/whatever`, {
      headers: { "If-Match": '"x"' },
    });
    expect(workspaceDelete.status()).toBe(403);

    const workspaceFilePut = await request.put(
      `${API}/skills/whatever/files/SKILL.md`,
      { data: { content: "hijack", executable: false } },
    );
    expect(workspaceFilePut.status()).toBe(403);

    // A3 role-name traversal guard: a `..` role must not path-clean into a
    // workspace write. The handler rejects the role name outright.
    const traversal = await request.put(
      `${API}/roles/${encodeURIComponent("..")}/skills/x/files/SKILL.md`,
      { data: { content: "escape", executable: false } },
    );
    expect([400, 403, 404]).toContain(traversal.status());
    expect(traversal.status()).not.toBe(200);

    // The file-access boundary rejects a cross-origin caller.
    const hostileOrigin = await request.get(
      `http://127.0.0.1:${process.env.E2E_PORT || "8090"}${API}/skill-capabilities`,
      { headers: { Origin: "http://attacker.example" } },
    );
    expect(hostileOrigin.status()).toBe(403);
  });

  test("@regression versioned role-skill CRUD round trip", async ({
    request,
  }) => {
    const available = await ensureRoleSkill(
      request,
      "audit",
      "Audit checklist skill",
      "# Audit\n\noriginal body\n",
    );
    test.skip(!available, ROLE_SCOPE_UNAVAILABLE);

    // The new skill surfaces in the catalog under its role group.
    const groups = (await (await request.get(`${API}/skills`)).json())
      .groups as Array<{ scope: string; role?: string; skills: { name: string }[] }>;
    const roleGroup = groups.find((g) => g.scope === "role" && g.role === ROLE);
    expect(roleGroup?.skills.map((s) => s.name)).toContain("audit");

    // Per-file ETag round trip on the SKILL.md body lane.
    const read = await request.get(
      `${API}/roles/${ROLE}/skills/audit/files/SKILL.md`,
    );
    expect(read.ok(), await read.text()).toBeTruthy();
    expect(read.headers()["etag"], "SKILL.md GET returns an ETag").toBeTruthy();
    const bodyRevision = (await read.json()).revision as string;

    const staleWrite = await request.put(
      `${API}/roles/${ROLE}/skills/audit/files/SKILL.md`,
      {
        data: { content: "# Audit\n\nstale\n", executable: false },
        headers: { "If-Match": '"not-the-current-revision"' },
      },
    );
    expect(staleWrite.status()).toBe(412);

    const goodWrite = await request.put(
      `${API}/roles/${ROLE}/skills/audit/files/SKILL.md`,
      {
        data: { content: "# Audit\n\nupdated body\n", executable: false },
        headers: { "If-Match": quoted(bodyRevision) },
      },
    );
    expect(goodWrite.ok(), await goodWrite.text()).toBeTruthy();

    // Bundled-file create is If-None-Match:* and rejects a duplicate with 412.
    const createFile = await request.put(
      `${API}/roles/${ROLE}/skills/audit/files/notes.md`,
      {
        data: { content: "extra\n", executable: false },
        headers: { "If-None-Match": "*" },
      },
    );
    expect(createFile.ok(), await createFile.text()).toBeTruthy();
    const duplicateFile = await request.put(
      `${API}/roles/${ROLE}/skills/audit/files/notes.md`,
      {
        data: { content: "collision\n", executable: false },
        headers: { "If-None-Match": "*" },
      },
    );
    expect(duplicateFile.status()).toBe(412);

    // Whole-skill mutations require the current content revision (428 without).
    const contentRevision = (
      await (await request.get(`${API}/roles/${ROLE}/skills/audit`)).json()
    ).content_revision as string;
    const patchNoPrecondition = await request.patch(
      `${API}/roles/${ROLE}/skills/audit`,
      { data: { description: "no precondition" } },
    );
    expect(patchNoPrecondition.status()).toBe(428);
    const patched = await request.patch(`${API}/roles/${ROLE}/skills/audit`, {
      data: { description: "Audit checklist (revised)" },
      headers: { "If-Match": quoted(contentRevision) },
    });
    expect(patched.ok(), await patched.text()).toBeTruthy();

    // Delete removes it from the catalog.
    await deleteRoleSkillIfPresent(request, ROLE, "audit");
    const after = (await (await request.get(`${API}/skills`)).json())
      .groups as Array<{ scope: string; role?: string; skills: { name: string }[] }>;
    const roleAfter = after.find((g) => g.scope === "role" && g.role === ROLE);
    expect(roleAfter?.skills.map((s) => s.name) ?? []).not.toContain("audit");
  });

  test("@regression renders the skills tree and saves through the editor", async ({
    page,
    request,
  }) => {
    const available = await ensureRoleSkill(
      request,
      "ui-demo",
      "UI demo skill",
      "# UI Demo\n\nseeded skill body\n",
    );
    test.skip(!available, ROLE_SCOPE_UNAVAILABLE);

    await page.setViewportSize({ width: 1440, height: 900 });
    // Skills live in their own nav section, not inside the Files explorer.
    await page.goto(`/ws/${WORKSPACE_ID}/skills`);

    // The Workspace skills root exists but its create affordance is disabled —
    // the A3 read-only line in the UI.
    const workspaceAdd = page.getByRole("button", {
      name: "New skill in Workspace",
    });
    await expect(workspaceAdd).toBeVisible();
    await expect(workspaceAdd).toBeDisabled();

    // The Skills section shows skill roots and nothing else: no Agents, no
    // Repos, no Workspace-files root borrowed from the Files explorer. Tree
    // section headings are <h2>; level 2 keeps this off the <h1> breadcrumb,
    // which also reads "Skills".
    await expect(
      page.getByRole("heading", { level: 2, name: "Skills", exact: true }),
    ).toBeVisible();
    for (const absent of ["Agents", "Repos", "Workspace files"]) {
      await expect(
        page.getByRole("heading", { level: 2, name: absent, exact: true }),
      ).toHaveCount(0);
    }

    // Nor the Files/Changes lens: Changes is a second view of a checkout, and
    // no checkout sits behind this section.
    await expect(
      page.getByRole("tablist", { name: "File explorer lens" }),
    ).toHaveCount(0);
    await expect(page.getByRole("tab", { name: /^Changes/ })).toHaveCount(0);

    // Expand the role group, then the seeded skill folder. The toggle button's
    // accessible name starts with the role label ("task …"), distinguishing it
    // from the "New skill in task" add button.
    await page.getByRole("button", { name: new RegExp(`^${ROLE}`) }).click();
    await page.getByText("ui-demo", { exact: true }).click();
    await page.getByText("SKILL.md", { exact: true }).click();

    const editor = page.getByRole("textbox").last();
    await expect(editor).toContainText("seeded skill body");

    // The metadata bar reflects scope + description for the open role skill.
    await expect(page.getByText(`Role: ${ROLE}`)).toBeVisible();
    await expect(page.getByText("UI demo skill")).toBeVisible();

    // Edit and save through the skills API round trip.
    await editor.click();
    await page.keyboard.press("Control+End");
    await page.keyboard.type("\nedited in the browser\n");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Modified", { exact: true })).toHaveCount(0);

    // The save is reflected server-side.
    await expect
      .poll(async () => {
        const response = await request.get(
          `${API}/roles/${ROLE}/skills/ui-demo/files/SKILL.md`,
        );
        if (!response.ok()) return "";
        return (await response.json()).content as string;
      })
      .toContain("edited in the browser");

    await deleteRoleSkillIfPresent(request, ROLE, "ui-demo");
  });
});
