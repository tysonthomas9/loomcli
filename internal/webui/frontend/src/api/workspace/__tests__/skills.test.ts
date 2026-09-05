import { beforeEach, describe, expect, it, vi } from "vitest";

import { del, get, patch, post, put } from "@/api/common";
import { ApiError } from "@/types/common";
import {
  createSkill,
  deleteSkill,
  deleteSkillFile,
  getSkill,
  getSkillCapabilities,
  getSkillFile,
  listSkills,
  mapSkillApiError,
  patchSkill,
  putSkillFile,
} from "../skills";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    del: vi.fn(),
    get: vi.fn(),
    patch: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  };
});

const mockDel = vi.mocked(del);
const mockGet = vi.mocked(get);
const mockPatch = vi.mocked(patch);
const mockPost = vi.mocked(post);
const mockPut = vi.mocked(put);
const workspace = { kind: "workspace" } as const;
const role = { kind: "role", role: "code reviewer" } as const;

describe("skills API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists the catalog and reads workspace and role details", async () => {
    mockGet.mockResolvedValue({ groups: [] });
    await listSkills("ws/id");
    await getSkill("ws/id", workspace, "review/code");
    await getSkill("ws/id", role, "review/code");

    expect(mockGet).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/ws%2Fid/skills",
    );
    expect(mockGet).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/ws%2Fid/skills/review%2Fcode",
    );
    expect(mockGet).toHaveBeenNthCalledWith(
      3,
      "/api/workspaces/ws%2Fid/roles/code%20reviewer/skills/review%2Fcode",
    );
  });

  it("uses role and refusal-capable workspace whole-skill routes", async () => {
    mockPost.mockResolvedValue({});
    mockPatch.mockResolvedValue({});
    mockDel.mockResolvedValue(undefined);

    const request = { name: "audit", description: "Audit" };
    await createSkill("ws", workspace, request);
    await createSkill("ws", role, request);
    await patchSkill(
      "ws",
      workspace,
      "audit",
      { description: "Workspace" },
      "rev-w",
    );
    await patchSkill("ws", role, "audit", { description: "Role" }, "rev-r");
    await deleteSkill("ws", workspace, "audit", "delete-w");
    await deleteSkill("ws", role, "audit", "delete-r");

    expect(mockPost).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/ws/skills",
      request,
    );
    expect(mockPost).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/ws/roles/code%20reviewer/skills",
      request,
    );
    expect(mockPatch).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/ws/skills/audit",
      { description: "Workspace" },
      { headers: { "If-Match": '"rev-w"' } },
    );
    expect(mockPatch).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/ws/roles/code%20reviewer/skills/audit",
      { description: "Role" },
      { headers: { "If-Match": '"rev-r"' } },
    );
    expect(mockDel).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/ws/skills/audit",
      { headers: { "If-Match": '"delete-w"' } },
    );
    expect(mockDel).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/ws/roles/code%20reviewer/skills/audit",
      { headers: { "If-Match": '"delete-r"' } },
    );
  });

  it("reads, conditionally writes, creates, and deletes bundled files", async () => {
    mockGet
      .mockResolvedValueOnce({
        path: "SKILL.md",
        content: "",
        executable: false,
        revision: "r",
        skill_ref: "workspace:audit",
      })
      .mockResolvedValueOnce({
        path: "scripts/run.sh",
        content: "",
        executable: false,
        revision: "r",
        skill_ref: "role:code reviewer:audit",
      });
    mockPut.mockResolvedValue({});
    mockDel.mockResolvedValue(undefined);

    await getSkillFile("ws", workspace, "audit", "SKILL.md");
    await getSkillFile("ws", role, "audit", "scripts/run.sh");
    await putSkillFile(
      "ws",
      role,
      "audit",
      "scripts/run.sh",
      { content: "echo ok", executable: true },
      { ifMatch: "file-rev" },
    );
    await putSkillFile(
      "ws",
      role,
      "audit",
      "notes/new file.md",
      { content: "", executable: false },
      { createOnly: true },
    );
    await deleteSkillFile("ws", workspace, "audit", "old.txt", "old-w");
    await deleteSkillFile("ws", role, "audit", "old.txt", "old-r");

    const roleBase =
      "/api/workspaces/ws/roles/code%20reviewer/skills/audit/files";
    expect(mockGet).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/ws/skills/audit/files/SKILL.md",
    );
    expect(mockGet).toHaveBeenNthCalledWith(2, `${roleBase}/scripts/run.sh`);
    expect(mockPut).toHaveBeenNthCalledWith(
      1,
      `${roleBase}/scripts/run.sh`,
      { content: "echo ok", executable: true },
      { headers: { "If-Match": '"file-rev"' } },
    );
    expect(mockPut).toHaveBeenNthCalledWith(
      2,
      `${roleBase}/notes/new%20file.md`,
      { content: "", executable: false },
      { headers: { "If-None-Match": "*" } },
    );
    expect(mockDel).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/ws/skills/audit/files/old.txt",
      { headers: { "If-Match": '"old-w"' } },
    );
    expect(mockDel).toHaveBeenNthCalledWith(2, `${roleBase}/old.txt`, {
      headers: { "If-Match": '"old-r"' },
    });
  });

  it("loads the exact capability route", async () => {
    mockGet.mockResolvedValue({
      can_edit_role_scope: true,
      workspace_scope: "read_only",
    });
    await getSkillCapabilities("ws");
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/ws/skill-capabilities",
    );
  });

  it.each([
    [403, "workspace_scope_readonly", undefined],
    [412, "precondition_failed", "latest-rev"],
    [409, "skill_provenance_conflict", undefined],
    [428, "precondition_required", undefined],
    [400, "invalid_precondition", undefined],
  ] as const)("maps %i %s responses", (status, code, revision) => {
    const failure = mapSkillApiError(
      new ApiError(status, "request failed", {
        code,
        error: `${code} message`,
        ...(revision ? { revision } : {}),
      }),
    );
    expect(failure).toEqual({
      status,
      code,
      message: `${code} message`,
      ...(revision ? { revision } : {}),
    });
  });

  it("keeps provenance owner and source metadata on a 409", () => {
    expect(
      mapSkillApiError(
        new ApiError(409, "Conflict", {
          code: "skill_provenance_conflict",
          error: "the skill is owned by another actor",
          owner: "pack-sync",
          source: "pack:standards",
        }),
      ),
    ).toMatchObject({
      status: 409,
      code: "skill_provenance_conflict",
      message: "the skill is owned by another actor",
      owner: "pack-sync",
      source: "pack:standards",
    });
  });
});
