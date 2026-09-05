import { describe, it, expect, vi } from "vitest";
import { get } from "@/api/common";
import { listScopedDir, readScopedFile } from "../files";
import { getSkillFile } from "../skills";
vi.mock("@/api/common", async (original) => ({
  ...(await original<typeof import("@/api/common")>()),
  get: vi.fn(),
}));
const mockGet = vi.mocked(get);
const scope = { scope: "workspace" } as const;
const file = {
  path: "a.txt",
  content: "",
  size: 0,
  binary: false,
  truncated: false,
  version: "opaque",
};
const entry = {
  name: "a.txt",
  is_dir: false,
  size: 0,
  mod_time: "2026-09-05T00:00:00Z",
};
describe("file content recovery API", () => {
  it("forwards cancellation and accepts a canonical empty directory", async () => {
    const signal = new AbortController().signal;
    mockGet.mockResolvedValue({ path: ".", entries: [] });
    await expect(listScopedDir("ws", scope, "", { signal })).resolves.toEqual({
      path: ".",
      entries: [],
    });
    expect(mockGet.mock.calls.at(-1)?.[1]).toEqual({ signal });
  });
  it.each([
    undefined,
    null,
    {},
    { path: ".", entries: null },
    { path: "other", entries: [] },
    { path: ".", entries: [null] },
    { path: ".", entries: [{ ...entry, is_dir: "yes" }] },
    { path: ".", entries: [{ ...entry, name: "../a" }] },
    { path: ".", entries: [entry, entry] },
    { path: ".", entries: [{ ...entry, mod_time: "invalid" }] },
  ])("rejects incomplete directory %j", async (data) => {
    mockGet.mockResolvedValue(data);
    await expect(listScopedDir("ws", scope)).rejects.toThrow(
      "Invalid directory",
    );
  });
  it("accepts text, binary and truncated previews with explicit metadata", async () => {
    for (const data of [
      file,
      { ...file, binary: true, content: undefined },
      { ...file, size: 100, truncated: true, content: "prefix" },
    ]) {
      mockGet.mockResolvedValue(data);
      await expect(readScopedFile("ws", scope, "a.txt")).resolves.toEqual(data);
    }
  });
  it.each([
    undefined,
    {},
    { ...file, path: "other" },
    { ...file, content: undefined },
    { ...file, version: "" },
    { ...file, truncated: undefined },
    { ...file, size: -1 },
    { ...file, binary: "false" },
  ])("rejects incomplete current file %j", async (data) => {
    mockGet.mockResolvedValue(data);
    await expect(readScopedFile("ws", scope, "a.txt")).rejects.toThrow(
      "Invalid file read",
    );
  });
  it("allows a read-only revision preview with no mutable version", async () => {
    mockGet.mockResolvedValue({ ...file, version: "" });
    await expect(
      readScopedFile("ws", scope, "a.txt", "commit-sha"),
    ).resolves.toMatchObject({ version: "" });
  });
  it("requires skill identity, revision and explicit empty content", async () => {
    const data = {
      path: "SKILL.md",
      content: "",
      executable: false,
      revision: "rev",
      skill_ref: "workspace:audit",
    };
    mockGet.mockResolvedValue(data);
    await expect(
      getSkillFile("ws", { kind: "workspace" }, "audit", "SKILL.md"),
    ).resolves.toEqual(data);
    for (const invalid of [
      { ...data, content: undefined },
      { ...data, skill_ref: "workspace:other" },
      { ...data, revision: "" },
      { ...data, path: "other" },
    ]) {
      mockGet.mockResolvedValue(invalid);
      await expect(
        getSkillFile("ws", { kind: "workspace" }, "audit", "SKILL.md"),
      ).rejects.toThrow("Invalid skill file");
    }
  });
});
