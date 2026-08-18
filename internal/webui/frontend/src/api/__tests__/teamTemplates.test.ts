import { beforeEach, describe, expect, it, vi } from "vitest";

import { get, post } from "@/api/common";

import { applyTeamTemplate, listTeamTemplates } from "../teamTemplates";

vi.mock("@/api/common", () => ({
  get: vi.fn(),
  post: vi.fn(),
  wsUrl: (workspaceId: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`,
}));

describe("Team Templates API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads the catalog from the registry-only endpoint", async () => {
    vi.mocked(get).mockResolvedValueOnce({ templates: [] });

    await expect(listTeamTemplates()).resolves.toEqual({ templates: [] });
    expect(get).toHaveBeenCalledWith("/api/team-templates");
  });

  it("posts an apply with no dry-run body to the encoded workspace and template path", async () => {
    vi.mocked(post).mockResolvedValueOnce({
      status: "done",
      report: {
        template_id: "fullstack-app",
        revision: 1,
        schema_version: 1,
        workspace_key: "MY WS",
        dry_run: false,
        steps: [],
        created: 0,
        skipped: 0,
        diverged: 0,
        failed: 0,
        materialized: 0,
      },
    });

    await applyTeamTemplate("MY WS", "fullstack/app");

    expect(post).toHaveBeenCalledWith(
      "/api/workspaces/MY%20WS/team-templates/fullstack%2Fapp/apply",
      undefined,
      { timeout: 120_000 },
    );
  });
});
