import { describe, it, expect, vi, beforeEach } from "vitest";
import { get } from "@/api/common";
import { getFileCapabilities, listFileCheckouts } from "../files";
vi.mock("@/api/common", () => ({
  get: vi.fn(),
  wsUrl: (ws: string, path: string) => `/api/workspaces/${ws}${path}`,
}));
const fetch = vi.mocked(get);
const empty = { checkouts: [], partial: false, limit_hit: false, errors: [] };
describe("file metadata transport contract", () => {
  beforeEach(() => {
    fetch.mockReset();
  });
  it.each([
    null,
    {},
    [],
    { read: true, write: true },
    { read: true, write: "yes", sensitive: false },
  ])("rejects malformed capabilities %j", async (data) => {
    fetch.mockResolvedValue(data);
    await expect(getFileCapabilities("ws")).rejects.toThrow();
  });
  it.each([
    null,
    {},
    { checkouts: [] },
    { ...empty, checkouts: null },
    { ...empty, checkouts: [null] },
    {
      ...empty,
      checkouts: [{ kind: "repo", repo: "x", exists: true, change_count: -1 }],
    },
    { ...empty, partial: null },
  ])("rejects malformed checkouts %j", async (data) => {
    fetch.mockResolvedValue(data);
    await expect(listFileCheckouts("ws")).rejects.toThrow();
  });
  it("passes abort signal and accepts explicit empty or valid partial metadata", async () => {
    const signal = new AbortController().signal;
    fetch.mockResolvedValue(empty);
    await expect(listFileCheckouts("ws", { signal })).resolves.toEqual(empty);
    expect(fetch).toHaveBeenCalledWith("/api/workspaces/ws/files/checkouts", {
      signal,
    });
    fetch.mockResolvedValue({ ...empty, partial: true });
    await expect(listFileCheckouts("ws")).resolves.toMatchObject({
      partial: true,
    });
    fetch.mockResolvedValue({ read: false, write: false, sensitive: false });
    await getFileCapabilities("ws", { signal });
    expect(fetch).toHaveBeenLastCalledWith(
      "/api/workspaces/ws/files/capabilities",
      { signal },
    );
  });
});
