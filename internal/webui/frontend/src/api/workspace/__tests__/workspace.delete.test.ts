import { beforeEach, describe, expect, it, vi } from "vitest";

const { get, del } = vi.hoisted(() => ({ get: vi.fn(), del: vi.fn() }));
vi.mock("@/api/common", () => ({
  get,
  del,
}));

import { deleteWorkspace, previewWorkspaceDeletion } from "../workspace";

describe("workspace deletion confirmation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the server preview and sends its exact fingerprint", async () => {
    const preview = {
      items: [{ repo: "repo", path: "/ws/repo", kind: "ignored", size: 5 }],
      fingerprint: "abc",
    };
    get.mockResolvedValue(preview);
    del.mockResolvedValue({ success: true, data: null });
    expect(await previewWorkspaceDeletion("WS")).toEqual(preview);
    await deleteWorkspace("WS", preview.fingerprint);
    expect(get).toHaveBeenCalledWith("/api/workspaces/WS/delete/preview");
    expect(del).toHaveBeenCalledWith("/api/workspaces/WS", {
      headers: { "X-Loom-Delete-Fingerprint": "abc" },
    });
  });
});
