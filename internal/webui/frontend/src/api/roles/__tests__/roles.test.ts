import { beforeEach, describe, expect, it, vi } from "vitest";

import { get, patch } from "@/api/common";

import { getRole, listRoles, updateRolePrompt } from "../roles";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return { ...actual, get: vi.fn(), patch: vi.fn() };
});

const mockGet = vi.mocked(get);
const mockPatch = vi.mocked(patch);

const role = {
  name: "reviewer",
  description: "Reviews changes",
  kind: "worker" as const,
  sourceKind: "file" as const,
  editable: true,
  editableReason: "" as const,
  updatedAt: "2026-08-14T12:00:00Z",
};

const detail = {
  role,
  sourceKind: "file" as const,
  sourceBody: "Review carefully.",
  editable: true,
  editableReason: "" as const,
  revision: "2026-08-14T12:00:00Z",
  activationNote: "Takes effect on next spawn.",
};

describe("roles API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists and gets roles with encoded workspace and role names", async () => {
    mockGet
      .mockResolvedValueOnce({ success: true, data: [role], total: 1 })
      .mockResolvedValueOnce({ success: true, data: detail });

    expect(await listRoles("Workspace A")).toEqual([role]);
    expect(await getRole("Workspace A", "reviewer/custom")).toEqual(detail);
    expect(mockGet).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/Workspace%20A/roles",
    );
    expect(mockGet).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/Workspace%20A/roles/reviewer%2Fcustom",
    );
  });

  it("patches only prompt and expectedRevision", async () => {
    mockPatch.mockResolvedValueOnce({ success: true, data: detail });
    const request = {
      prompt: "Review carefully.",
      expectedRevision: "2026-08-14T12:00:00Z",
    };
    await updateRolePrompt("WS", "reviewer", request);
    expect(mockPatch).toHaveBeenCalledWith(
      "/api/workspaces/WS/roles/reviewer",
      request,
    );
  });
});
