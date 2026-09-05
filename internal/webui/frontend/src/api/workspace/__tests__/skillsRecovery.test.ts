import { describe, it, expect, vi } from "vitest";
import { get } from "@/api/common";
import { listSkills, getSkillCapabilities } from "../skills";
vi.mock("@/api/common", async (original) => ({
  ...(await original<typeof import("@/api/common")>()),
  get: vi.fn(),
}));
const mockGet = vi.mocked(get);
const skill = {
  name: "review",
  scope: "workspace",
  description: "",
  content_revision: "opaque",
  files: [],
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-05T00:00:00Z",
};
const group = { scope: "workspace", skills: [skill] };
describe("skills recovery API", () => {
  it("forwards cancellation and preserves canonical catalog", async () => {
    const signal = new AbortController().signal;
    mockGet.mockResolvedValue({ groups: [group] });
    await expect(listSkills("ws", { signal })).resolves.toEqual({
      groups: [group],
    });
    expect(mockGet).toHaveBeenLastCalledWith("/api/workspaces/ws/skills", {
      signal,
    });
  });
  it.each([
    undefined,
    null,
    {},
    { groups: null },
    { groups: [null] },
    { groups: [{ ...group, skills: null }] },
    { groups: [{ scope: "role", skills: [] }] },
    { groups: [{ ...group, skills: [{ ...skill, files: null }] }] },
    { groups: [{ ...group, skills: [{ ...skill, scope: "role" }] }] },
    {
      groups: [
        {
          ...group,
          skills: [
            {
              ...skill,
              files: [{ path: "a", revision: "r", executable: "yes" }],
            },
          ],
        },
      ],
    },
  ])("rejects malformed catalog %j", async (data) => {
    mockGet.mockResolvedValue(data);
    await expect(listSkills("ws")).rejects.toThrow("Invalid skills catalog");
  });
  it("accepts empty and role catalogs", async () => {
    for (const data of [
      { groups: [] },
      {
        groups: [
          {
            scope: "role",
            role: "worker",
            skills: [{ ...skill, scope: "role", role: "worker" }],
          },
        ],
      },
    ]) {
      mockGet.mockResolvedValue(data);
      await expect(listSkills("ws")).resolves.toEqual(data);
    }
  });
  it.each([
    undefined,
    {},
    { can_edit_role_scope: true },
    { can_edit_role_scope: "true", workspace_scope: "read_only" },
    { can_edit_role_scope: true, workspace_scope: "write" },
  ])("rejects malformed capability %j", async (data) => {
    mockGet.mockResolvedValue(data);
    await expect(getSkillCapabilities("ws")).rejects.toThrow(
      "Invalid skill capabilities",
    );
  });
  it("forwards capability cancellation with a valid refusal", async () => {
    const signal = new AbortController().signal;
    const data = { can_edit_role_scope: false, workspace_scope: "read_only" };
    mockGet.mockResolvedValue(data);
    await expect(getSkillCapabilities("ws", { signal })).resolves.toEqual(data);
    expect(mockGet).toHaveBeenLastCalledWith(
      "/api/workspaces/ws/skill-capabilities",
      { signal },
    );
  });
  it("propagates source failure", async () => {
    mockGet.mockRejectedValue(new Error("offline"));
    await expect(listSkills("ws")).rejects.toThrow("offline");
  });
});
